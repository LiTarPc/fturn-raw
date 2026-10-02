package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestUserDataSurvivesDifferentExecutableFolders(t *testing.T) {
	first, second, dataDir := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "fturn-raw")
	old := newApp(first)
	p := testProfile("192.0.2.1", "ab")
	p2 := testProfile("198.51.100.2", "cd")
	if err := old.SaveNamedProfile(p, "first", true); err != nil {
		t.Fatal(err)
	}
	firstID := old.GetSnapshot().ActiveID
	if err := old.SaveNamedProfile(p2, "second", true); err != nil {
		t.Fatal(err)
	}
	if err := old.SelectProfile(firstID); err != nil {
		t.Fatal(err)
	}
	oldBook, _ := os.ReadFile(filepath.Join(first, "profiles.json"))
	settings := AppSettings{MinimizeToTray: false, AutoConnect: true}
	raw, _ := json.Marshal(struct {
		Version int `json:"version"`
		AppSettings
	}{1, settings})
	if err := os.WriteFile(filepath.Join(first, "app-settings.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	a := newAppWithData(first, dataDir)
	s := a.GetSnapshot()
	if s.State == "error" || s.DataDir != dataDir || s.ActiveID != firstID || s.Profile != p || len(s.Profiles) != 2 || s.Settings != settings {
		t.Fatalf("migration failed: state=%s count=%d", s.State, len(s.Profiles))
	}
	// A new installation carries different bundled profiles; user data remains authoritative.
	other := newApp(second)
	if err := other.SaveNamedProfile(testProfile("203.0.113.3", "ef"), "bundled", true); err != nil {
		t.Fatal(err)
	}
	a = newAppWithData(second, dataDir)
	if got := a.GetSnapshot(); got.ActiveID != firstID || got.Profile != p || len(got.Profiles) != 2 || got.Settings != settings {
		t.Fatal("new installation replaced shared user data")
	}
	if err := a.SaveNamedProfile(p2, "renamed", false); err != nil {
		t.Fatal(err)
	}
	if got := newAppWithData(first, dataDir).GetSnapshot(); got.Profile != p2 || got.Profiles[0].Name != "renamed" {
		t.Fatal("changes not shared across executable folders")
	}
	after, _ := os.ReadFile(filepath.Join(first, "profiles.json"))
	if string(after) != string(oldBook) {
		t.Fatal("legacy backup modified")
	}
}
func TestLegacyFileKeyBecomesPortableUserData(t *testing.T) {
	root, dataDir := t.TempDir(), filepath.Join(t.TempDir(), "fturn-raw")
	p := testProfile("192.0.2.1", "ab")
	key := p.Key
	p.Key = ""
	p.KeyFile = "raw.key"
	raw, _ := json.Marshal(p)
	os.WriteFile(filepath.Join(root, "connection.json"), raw, 0600)
	os.WriteFile(filepath.Join(root, "raw.key"), []byte(key), 0600)
	a := newAppWithData(root, dataDir)
	if got := a.GetSnapshot(); got.Profile.Key != key || got.Profile.KeyFile != "" || len(got.Profiles) != 1 || got.State == "error" {
		t.Fatal("legacy key was not migrated inline")
	}
	if got := newAppWithData(t.TempDir(), dataDir).GetSnapshot(); got.Profile.Key != key {
		t.Fatal("profile depends on old key file")
	}
	if original, _ := os.ReadFile(filepath.Join(root, "connection.json")); string(original) != string(raw) {
		t.Fatal("legacy connection changed")
	}
}
func TestCorruptDataIsNotReplacedByLegacyProfiles(t *testing.T) {
	root, dataDir := t.TempDir(), t.TempDir()
	old := newApp(root)
	if err := old.SaveNamedProfile(testProfile("192.0.2.1", "ab"), "valid", true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dataDir, "profiles.json")
	original := []byte("corrupt user book")
	os.WriteFile(path, original, 0600)
	a := newAppWithData(root, dataDir)
	if a.storageErr == nil {
		t.Fatal("corrupt user book silently replaced")
	}
	if err := a.SaveNamedProfile(testProfile("198.51.100.2", "cd"), "new", true); err == nil {
		t.Fatal("corrupt book overwritten")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("corrupt user data lost")
	}
}
func TestMigrationFailureDoesNotPublishPartialBook(t *testing.T) {
	root, dataDir := t.TempDir(), t.TempDir()
	source := []byte("invalid old book")
	os.WriteFile(filepath.Join(root, "profiles.json"), source, 0600)
	a := newAppWithData(root, dataDir)
	if a.storageErr == nil {
		t.Fatal("invalid legacy book accepted")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "profiles.json")); !os.IsNotExist(err) {
		t.Fatal("partial migration published")
	}
	temps, _ := filepath.Glob(filepath.Join(dataDir, ".migration-*"))
	if len(temps) != 0 {
		t.Fatal("temporary keys retained")
	}
	path := filepath.Join(dataDir, "guard.json")
	os.WriteFile(path, []byte("existing"), 0600)
	if err := createDataFile(path, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != "existing" {
		t.Fatal("no-replace publication overwrote existing data")
	}
}
func TestUnavailableUserDataDirectoryBlocksWrites(t *testing.T) {
	root := t.TempDir()
	block := filepath.Join(t.TempDir(), "not-a-directory")
	os.WriteFile(block, []byte("keep"), 0600)
	a := newAppWithData(root, filepath.Join(block, "fturn-raw"))
	if a.storageErr == nil {
		t.Fatal("unavailable user data accepted")
	}
	if err := a.SaveNamedProfile(testProfile("192.0.2.1", "ab"), "new", true); err == nil {
		t.Fatal("write to unavailable folder accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "profiles.json")); !os.IsNotExist(err) {
		t.Fatal("silently fell back to executable folder")
	}
}

// Opt-in migration performs only file operations, without starting Wails, Raw or touching routes.
func TestInstalledUserDataMigration(t *testing.T) {
	source := os.Getenv("FTURN_UI_MIGRATE_SOURCE")
	if source == "" {
		t.Skip("installed data migration is opt-in")
	}
	dataDir, err := userDataDirectory()
	if err != nil {
		t.Fatal(err)
	}
	a := newAppWithData(source, dataDir)
	s := a.GetSnapshot()
	if a.storageErr != nil {
		t.Fatal(a.storageErr)
	}
	if len(s.Profiles) == 0 {
		t.Fatal("no installed profiles migrated")
	}
	t.Logf("User data: %s; preserved profiles: %d", s.DataDir, len(s.Profiles))
}
