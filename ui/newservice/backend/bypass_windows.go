package backend

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"fturnraw/bypassrules"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed assets/ru-aggregated.zone
var builtinRU []byte

const builtinRUDate = "2026-10-03"

type BypassSettings = bypassrules.Settings
type BypassSnapshot struct {
	Settings  BypassSettings `json:"settings"`
	RUCount   int            `json:"ruCount"`
	RUUpdated string         `json:"ruUpdated"`
	Error     string         `json:"error"`
}
type bypassService struct {
	mu                sync.Mutex
	dir               string
	value             BypassSettings
	cidrs             []string
	updated           string
	loadErr, cacheErr error
	write             func(string, []byte) error
}

func newBypassService(dir string) *bypassService {
	s := &bypassService{dir: dir, value: BypassSettings{Sites: []string{}, Apps: []string{}}, write: atomicSettingsWrite}
	data, e := os.ReadFile(filepath.Join(dir, "bypass.json"))
	if e == nil {
		c, err := bypassrules.Decode(bytes.NewReader(data))
		if err != nil {
			s.loadErr = fmt.Errorf("bypass.json повреждён: %w. Исходный файл сохранён", err)
		} else if len(c.CIDRs) != 0 {
			s.loadErr = errors.New("bypass.json не должен содержать встроенный кэш CIDR")
		} else {
			s.value = c.Settings
		}
	} else if !os.IsNotExist(e) {
		s.loadErr = e
	}
	data, e = os.ReadFile(filepath.Join(dir, "ru-cidr.txt"))
	s.updated = builtinRUDate + " · встроенный"
	if os.IsNotExist(e) {
		data = builtinRU
	} else if e != nil {
		s.cacheErr = e
		return s
	} else if st, e := os.Stat(filepath.Join(dir, "ru-cidr.txt")); e == nil {
		s.updated = st.ModTime().Format("2006-01-02 15:04")
	}
	s.cidrs, s.cacheErr = bypassrules.ParseCIDRs(bytes.NewReader(data))
	return s
}
func (s *bypassService) snapshot() BypassSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.value
	v.Sites = append([]string{}, v.Sites...)
	v.Apps = append([]string{}, v.Apps...)
	out := BypassSnapshot{Settings: v, RUCount: len(s.cidrs), RUUpdated: s.updated}
	if e := errors.Join(s.loadErr, s.cacheErr); e != nil {
		out.Error = e.Error()
	}
	return out
}
func (s *bypassService) save(v BypassSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return s.loadErr
	}
	v, e := bypassrules.Normalize(v)
	if e != nil {
		return e
	}
	if v.Enabled && v.RU && s.cacheErr != nil {
		return s.cacheErr
	}
	data, e := json.MarshalIndent(bypassrules.Config{Version: 1, Settings: v}, "", "  ")
	if e != nil {
		return e
	}
	if e = s.write(filepath.Join(s.dir, "bypass.json"), data); e != nil {
		return e
	}
	s.value = v
	return nil
}

// prepare creates an immutable per-connection snapshot; editing the global settings
// cannot change a running client. The file contains no server key or call URL.
func (s *bypassService) prepare(mode string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return "", s.loadErr
	}
	if !s.value.Enabled {
		return "", nil
	}
	if mode != "full" {
		return "", errors.New("Обход требует режима «Весь IPv4 через Raw»")
	}
	if s.value.RU && s.cacheErr != nil {
		return "", s.cacheErr
	}
	c := bypassrules.Config{Version: 1, Settings: s.value}
	if c.RU {
		c.CIDRs = s.cidrs
	}
	data, e := json.Marshal(c)
	if e != nil {
		return "", e
	}
	f, e := os.CreateTemp(s.dir, "bypass-session-*.json")
	if e != nil {
		return "", e
	}
	path := f.Name()
	e = f.Chmod(0600)
	if e == nil {
		_, e = f.Write(data)
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		os.Remove(path)
		return "", e
	}
	return path, nil
}
func (s *bypassService) update(ctx context.Context, client *http.Client, url string) error {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if e != nil {
		return e
	}
	resp, e := client.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("IPdeny: HTTP %d", resp.StatusCode)
	}
	cidrs, e := bypassrules.ParseCIDRs(io.LimitReader(resp.Body, bypassrules.MaxData+1))
	if e != nil {
		return e
	}
	data := []byte(strings.Join(cidrs, "\n") + "\n")
	s.mu.Lock()
	defer s.mu.Unlock()
	if e = s.write(filepath.Join(s.dir, "ru-cidr.txt"), data); e != nil {
		return e
	}
	s.cidrs = cidrs
	s.cacheErr = nil
	s.updated = time.Now().Format("2006-01-02 15:04")
	return nil
}
func (a *App) SaveBypassSettings(v BypassSettings) error {
	if e := a.bypass.save(v); e != nil {
		return e
	}
	a.publish()
	return nil
}
func (a *App) UpdateRUCIDR() error {
	a.op.Lock()
	defer a.op.Unlock()
	a.mu.Lock()
	running := a.cancel != nil
	ctx := a.ctx
	a.mu.Unlock()
	if running {
		return errors.New("Обновление RU CIDR доступно после отключения Raw; новые правила применятся при следующем подключении")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || req.URL.Scheme != "https" || req.URL.Host != "www.ipdeny.com" {
			return errors.New("unexpected IPdeny redirect")
		}
		return nil
	}}
	if e := a.bypass.update(ctx, client, bypassrules.RUURL); e != nil {
		return fmt.Errorf("Не удалось обновить RU CIDR; прежний список сохранён: %w", e)
	}
	a.publish()
	return nil
}
