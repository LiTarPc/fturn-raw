package backend

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type fakeStartup struct {
	calls []bool
	err   error
}

func (s *fakeStartup) SetEnabled(v bool) error { s.calls = append(s.calls, v); return s.err }
func TestSettingsPersistAndRollbackStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app-settings.json")
	startup := &fakeStartup{}
	service := newSettingsService(path, startup)
	if service.get() != (AppSettings{MinimizeToTray: true}) {
		t.Fatal("unsafe defaults")
	}
	next := AppSettings{MinimizeToTray: true, StartWithWindows: true, AutoConnect: true}
	service.write = func(string, []byte) error { return errors.New("disk full") }
	if service.save(next) == nil {
		t.Fatal("expected disk error")
	}
	if !reflect.DeepEqual(startup.calls, []bool{true, false}) || service.get() != (AppSettings{MinimizeToTray: true}) {
		t.Fatal("startup rollback or memory rollback failed")
	}
	service.write = atomicSettingsWrite
	if err := service.save(next); err != nil {
		t.Fatal(err)
	}
	if got := newSettingsService(path, &fakeStartup{}).get(); got != next {
		t.Fatalf("reload: %+v", got)
	}
	startup.err = errors.New("task scheduler denied")
	if service.save(AppSettings{}) == nil {
		t.Fatal("expected startup error")
	}
	if got := newSettingsService(path, &fakeStartup{}).get(); got != next {
		t.Fatal("failed startup changed persisted settings")
	}
}
func TestCorruptSettingsArePreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app-settings.json")
	original := []byte("broken settings")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	startup := &fakeStartup{}
	service := newSettingsService(path, startup)
	if service.save(AppSettings{StartWithWindows: true}) == nil {
		t.Fatal("corruption overwritten")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) || len(startup.calls) != 0 {
		t.Fatal("corruption changed disk or OS startup")
	}
}
