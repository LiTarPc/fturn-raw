package backend

import (
	"bytes"
	"context"
	"errors"
	"fturnraw/bypassrules"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGlobalBypassAndImmutableSession(t *testing.T) {
	dir := t.TempDir()
	s := newBypassService(dir)
	if got := s.snapshot(); got.Settings.Enabled || got.RUCount < 8000 || got.Error != "" {
		t.Fatal("built-in RU data invalid")
	}
	v := BypassSettings{Enabled: true, RU: true, Sites: []string{"EXAMPLE.org"}, Apps: []string{"game.exe"}}
	if e := s.save(v); e != nil {
		t.Fatal(e)
	}
	path, e := s.prepare("full")
	if e != nil {
		t.Fatal(e)
	}
	defer os.Remove(path)
	before, _ := os.ReadFile(path)
	c, e := bypassrules.Decode(bytes.NewReader(before))
	if e != nil || len(c.CIDRs) < 8000 || c.Sites[0] != "example.org" {
		t.Fatal("invalid session")
	}
	v.Sites = []string{"other.org"}
	if e = s.save(v); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("running session mutated")
	}
	fresh := newBypassService(dir).snapshot()
	if fresh.Settings.Sites[0] != "other.org" {
		t.Fatal("AppData persistence failed")
	}
	fresh.Settings.Sites[0] = "corrupted"
	if s.snapshot().Settings.Sites[0] != "other.org" {
		t.Fatal("snapshot aliases mutable settings")
	}
	if _, e = s.prepare("tunnel"); e == nil {
		t.Fatal("bypass accepted non-full route")
	}
}
func TestCIDRUpdateKeepsOldDataOnFailure(t *testing.T) {
	dir := t.TempDir()
	s := newBypassService(dir)
	old := s.snapshot().RUCount
	body := "<html>failure</html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
	defer srv.Close()
	if s.update(context.Background(), srv.Client(), srv.URL) == nil || s.snapshot().RUCount != old {
		t.Fatal("bad data replaced cache")
	}
	body = "203.0.113.0/24\n203.0.113.1/24\n"
	if e := s.update(context.Background(), srv.Client(), srv.URL); e != nil {
		t.Fatal(e)
	}
	if s.snapshot().RUCount != 1 || newBypassService(dir).snapshot().RUCount != 1 {
		t.Fatal("update not persisted")
	}
	s.write = func(string, []byte) error { return errors.New("disk failure") }
	body = "198.51.100.0/24\n"
	if s.update(context.Background(), srv.Client(), srv.URL) == nil || s.cidrs[0] != "203.0.113.0/24" {
		t.Fatal("failed write changed memory cache")
	}
}
func TestDamagedBypassFilePreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bypass.json")
	os.WriteFile(path, []byte("bad data"), 0600)
	s := newBypassService(dir)
	if s.save(BypassSettings{}) == nil {
		t.Fatal("corrupt configuration overwritten")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "bad data" {
		t.Fatal("original not preserved")
	}
}
func TestBypassEditingDoesNotDisconnect(t *testing.T) {
	a := newApp(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.cancel = cancel
	a.state = "connected"
	if e := a.SaveBypassSettings(BypassSettings{Enabled: true, Apps: []string{"game.exe"}}); e != nil {
		t.Fatal(e)
	}
	if ctx.Err() != nil || a.GetSnapshot().State != "connected" {
		t.Fatal("editing disrupted connection")
	}
}

func TestCIDRUpdateDuringConnectionPreservesSession(t *testing.T) {
	a := newApp(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.cancel, a.state = cancel, "connected"
	if err := a.bypass.save(BypassSettings{Enabled: true, RU: true}); err != nil {
		t.Fatal(err)
	}
	path, err := a.bypass.prepare("full")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte("203.0.113.0/24\n"))
	}))
	defer srv.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	done := make(chan error, 1)
	go func() { done <- a.updateRUCIDR(srv.Client(), srv.URL) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("update did not start while connected")
	}
	if !a.op.TryLock() {
		t.Fatal("updater blocked disconnect/recovery")
	}
	a.op.Unlock()
	if err := a.bypass.update(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("overlapping update was accepted")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != nil || a.GetSnapshot().State != "connected" {
		t.Fatal("update disrupted connection")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("active bypass snapshot changed")
	}
	if got := newBypassService(a.dataDir).snapshot(); got.RUCount != 1 || got.Error != "" {
		t.Fatal("new cache not persisted")
	}
	fresh, err := a.bypass.prepare("full")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(fresh)
	if err != nil {
		t.Fatal(err)
	}
	c, err := bypassrules.Decode(bytes.NewReader(b))
	if err != nil || len(c.CIDRs) != 1 || c.CIDRs[0] != "203.0.113.0/24" {
		t.Fatal("next connection missed new cache")
	}
}
