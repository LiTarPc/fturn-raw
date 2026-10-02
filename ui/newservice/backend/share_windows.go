package backend

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const sharePrefix = "fturnraw://"
const maxShareLength = 16 * 1024

// Separate wire format deliberately excludes local file paths and future fields.
type sharedProfile struct {
	Version   int    `json:"v"`
	Server    string `json:"server"`
	VkLink    string `json:"vk"`
	Key       string `json:"key"`
	Mtu       int    `json:"mtu"`
	Streams   int    `json:"streams"`
	RouteMode string `json:"mode"`
}

func decodeShare(link string) (Profile, []byte, error) {
	link = strings.TrimSpace(link)
	fail := func() (Profile, []byte, error) {
		return Profile{}, nil, errors.New("Некорректная ссылка конфигурации Raw.")
	}
	if len(link) > maxShareLength || !strings.HasPrefix(link, sharePrefix) {
		return fail()
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(link, sharePrefix))
	if err != nil {
		return fail()
	}
	var wire sharedProfile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&wire) != nil {
		return fail()
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || wire.Version != 1 {
		return fail()
	}
	p := Profile{Server: wire.Server, VkLink: wire.VkLink, Mtu: wire.Mtu, Streams: wire.Streams, RouteMode: wire.RouteMode}
	if err := validateFields(p); err != nil {
		return Profile{}, nil, err
	}
	key, err := hex.DecodeString(wire.Key)
	if err != nil || len(key) != 32 {
		return fail()
	}
	p.Key = hex.EncodeToString(key)
	return p, key, nil
}

// The result contains the actual key so it is portable to another computer.
// Never log, fetch or open this private configuration as a web URL.
func (a *App) ExportProfile(p Profile) (string, error) {
	key, err := validate(p, a.root)
	if err != nil {
		return "", err
	}
	wire := sharedProfile{1, p.Server, p.VkLink, key, p.Mtu, p.Streams, p.RouteMode}
	data, err := json.Marshal(wire)
	if err != nil {
		return "", err
	}
	link := sharePrefix + base64.RawURLEncoding.EncodeToString(data)
	if len(link) > maxShareLength {
		return "", errors.New("Конфигурация слишком длинная для ссылки.")
	}
	return link, nil
}
func (a *App) CopyConnectionLink(link string) error {
	if _, _, err := decodeShare(link); err != nil {
		return err
	}
	if a.ctx == nil {
		return errors.New("Копирование доступно в приложении Windows.")
	}
	return runtime.ClipboardSetText(a.ctx, link)
}
func (a *App) ImportProfile(link string) (Profile, error) {
	p, _, err := decodeShare(link)
	if err != nil {
		return Profile{}, err
	}
	a.op.Lock()
	defer a.op.Unlock()
	if err := a.canStoreProfiles(); err != nil {
		return Profile{}, err
	}
	for _, entry := range a.profiles {
		if entry.Profile == p {
			a.mu.Lock()
			running := a.cancel != nil
			a.mu.Unlock()
			if running {
				return p, nil
			}
			if err := a.persistProfiles(a.profiles, entry.ID); err != nil {
				return Profile{}, err
			}
			return p, nil
		}
	}
	if err := a.saveNamedProfile(p, p.Server, true); err != nil {
		return Profile{}, err
	}
	return p, nil
}
