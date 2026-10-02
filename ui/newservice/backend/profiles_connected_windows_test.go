package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAddAndImportPreserveRunningSession(t *testing.T) {
	for _, state := range []string{"connecting", "connected", "reconnecting"} {
		t.Run(state, func(t *testing.T) {
			a := newApp(t.TempDir())
			first := testProfile("192.0.2.1", "ab")
			if err := a.SaveNamedProfile(first, "active", true); err != nil {
				t.Fatal(err)
			}
			a.state = state
			a.generation = 17
			a.ready = map[string]bool{"1": true, "3": true}
			a.rx = 100
			a.tx = 200
			cancelled := false
			a.cancel = func() { cancelled = true }
			cmd := &exec.Cmd{}
			a.cmd = cmd
			start := time.Now().Add(-65 * time.Second)
			a.clock.transition("connected", start)
			before := a.GetSnapshot()
			since := a.clock.activeSince
			second := testProfile("198.51.100.2", "cd")
			if err := a.SaveNamedProfile(second, "added", true); err != nil {
				t.Fatal(err)
			}
			third := testProfile("203.0.113.3", "ef")
			link, err := a.ExportProfile(third)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.ImportProfile(link); err != nil {
				t.Fatal(err)
			}
			duplicateLink, err := a.ExportProfile(second)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.ImportProfile(duplicateLink); err != nil {
				t.Fatal(err)
			}
			after := a.GetSnapshot()
			if after.ActiveID != before.ActiveID || after.Profile != first || after.State != state || after.Ready != 2 || after.RX != 100 || after.TX != 200 || !a.clock.activeSince.Equal(since) || a.cmd != cmd || a.generation != 17 || a.cancel == nil || cancelled {
				t.Fatal("add/import changed running session")
			}
			if len(after.Profiles) != 3 || after.Profiles[1].Profile != second || after.Profiles[2].Profile != third {
				t.Fatal("entries missing or duplicate appended")
			}
			reloaded := newApp(a.root).GetSnapshot()
			if reloaded.ActiveID != before.ActiveID || reloaded.Profile != first || len(reloaded.Profiles) != 3 {
				t.Fatal("book selection was not preserved across restart")
			}
			for _, err := range []error{a.SelectProfile(after.Profiles[1].ID), a.SaveNamedProfile(second, "replace active", false), a.DeleteProfile(before.ActiveID)} {
				if err == nil {
					t.Fatal("active-profile mutation while connected accepted")
				}
			}
		})
	}
}
func TestConnectedAddFailurePreservesSessionAndBook(t *testing.T) {
	a := newApp(t.TempDir())
	first := testProfile("192.0.2.1", "ab")
	if err := a.SaveNamedProfile(first, "active", true); err != nil {
		t.Fatal(err)
	}
	a.cancel = func() { t.Fatal("add cancelled the session") }
	a.state = "connected"
	before := a.GetSnapshot()
	path := filepath.Join(a.root, "profiles.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(a.root, "original.json")
	if err = os.Rename(path, backup); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err = a.SaveNamedProfile(testProfile("198.51.100.2", "cd"), "added", true); err == nil {
		t.Fatal("expected disk failure")
	}
	after := a.GetSnapshot()
	if after.Profile != before.Profile || after.ActiveID != before.ActiveID || after.State != before.State || len(after.Profiles) != 1 || a.cancel == nil {
		t.Fatal("disk failure changed running profile")
	}
	data, _ := os.ReadFile(backup)
	if string(data) != string(original) {
		t.Fatal("original book changed")
	}
	temps, _ := filepath.Glob(filepath.Join(a.root, ".profiles-*"))
	if len(temps) != 0 {
		t.Fatal("orphaned secret temp file")
	}
}
