package backend

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type AppSettings struct {
	MinimizeToTray   bool `json:"minimizeToTray"`
	StartWithWindows bool `json:"startWithWindows"`
	AutoConnect      bool `json:"autoConnect"`
}
type startupManager interface{ SetEnabled(bool) error }
type settingsService struct {
	mu      sync.Mutex
	path    string
	value   AppSettings
	loadErr error
	startup startupManager
	write   func(string, []byte) error
}

func newSettingsService(path string, startup startupManager) *settingsService {
	s := &settingsService{path: path, value: AppSettings{MinimizeToTray: true}, startup: startup, write: atomicSettingsWrite}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s
	}
	if err != nil {
		s.loadErr = err
		return s
	}
	value, err := decodeAppSettings(data)
	if err != nil {
		s.loadErr = err
		return s
	}
	s.value = value
	return s
}
func (s *settingsService) get() AppSettings { s.mu.Lock(); defer s.mu.Unlock(); return s.value }
func (s *settingsService) save(next AppSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return s.loadErr
	}
	data, err := json.MarshalIndent(struct {
		Version int `json:"version"`
		AppSettings
	}{1, next}, "", "  ")
	if err != nil {
		return err
	}
	changed := next.StartWithWindows != s.value.StartWithWindows
	if changed {
		if err = s.startup.SetEnabled(next.StartWithWindows); err != nil {
			return err
		}
	}
	if err = s.write(s.path, data); err != nil {
		if changed {
			err = errors.Join(err, s.startup.SetEnabled(s.value.StartWithWindows))
		}
		return err
	}
	s.value = next
	return nil
}
func atomicSettingsWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".settings-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = f.Chmod(0600)
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func (a *App) SaveAppSettings(s AppSettings) error {
	if err := a.settings.save(s); err != nil {
		return err
	}
	if !s.MinimizeToTray && a.desktop != nil {
		select {
		case <-a.startupDone:
			a.desktop.show()
		default:
		}
	}
	a.publish()
	return nil
}

func decodeAppSettings(data []byte) (AppSettings, error) {
	var disk struct {
		Version int `json:"version"`
		AppSettings
	}
	if json.Unmarshal(data, &disk) != nil || disk.Version != 1 {
		return AppSettings{}, errors.New("app-settings.json повреждён. Файл оставлен без изменений.")
	}
	return disk.AppSettings, nil
}
