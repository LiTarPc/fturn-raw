package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testProfile(host, key string) Profile {
	return Profile{Server: host + ":56010", VkLink: "https://vk.ru/call/join/test", Key: strings.Repeat(key, 32), Mtu: 1420, Streams: 10, RouteMode: "full"}
}
func TestProfilesMigrateImportSelectRestart(t *testing.T) {
	root := t.TempDir()
	old := testProfile("192.0.2.1", "ab")
	legacy := old
	legacy.Key = ""
	legacy.KeyFile = "raw.key"
	os.WriteFile(filepath.Join(root, "raw.key"), []byte(old.Key), 0600)
	data, _ := json.Marshal(legacy)
	os.WriteFile(filepath.Join(root, "connection.json"), data, 0600)
	a := newApp(root)
	s := a.GetSnapshot()
	if len(s.Profiles) != 1 || s.Profile != old {
		t.Fatal("legacy not migrated")
	}
	oldID := s.ActiveID
	next := testProfile("198.51.100.2", "cd")
	link, err := a.ExportProfile(next)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.ImportProfile(link); err != nil {
		t.Fatal(err)
	}
	s = a.GetSnapshot()
	newID := s.ActiveID
	if len(s.Profiles) != 2 || s.Profiles[0].Profile != old || s.Profile != next {
		t.Fatal("old profile overwritten")
	}
	if _, err = a.ImportProfile(link); err != nil || len(a.GetSnapshot().Profiles) != 2 {
		t.Fatal("duplicate import")
	}
	if err = a.SelectProfile(oldID); err != nil {
		t.Fatal(err)
	}
	b := newApp(root)
	if b.GetSnapshot().ActiveID != oldID || b.GetSnapshot().Profile != old {
		t.Fatal("selection or key not persisted")
	}
	if err = b.SelectProfile(newID); err != nil || b.GetSnapshot().Profile != next {
		t.Fatal("second key not retained")
	}
	legacyData, _ := os.ReadFile(filepath.Join(root, "connection.json"))
	if string(legacyData) != string(data) {
		t.Fatal("legacy overwritten")
	}
	files, _ := filepath.Glob(filepath.Join(root, "*.key"))
	if len(files) != 1 {
		t.Fatal("import created key files")
	}
}
func TestProfileRenameDeleteAndConnectedMutations(t *testing.T) {
	a := newApp(t.TempDir())
	p := testProfile("192.0.2.1", "ab")
	if err := a.SaveNamedProfile(p, "Первый", true); err != nil {
		t.Fatal(err)
	}
	first := a.GetSnapshot().ActiveID
	if err := a.SaveNamedProfile(p, "Переименован", false); err != nil {
		t.Fatal(err)
	}
	if a.GetSnapshot().Profiles[0].Name != "Переименован" {
		t.Fatal("rename failed")
	}
	p2 := testProfile("198.51.100.2", "cd")
	if err := a.SaveNamedProfile(p2, "Второй", true); err != nil {
		t.Fatal(err)
	}
	second := a.GetSnapshot().ActiveID
	a.cancel = func() {}
	for _, err := range []error{a.SelectProfile(first), a.DeleteProfile(second), a.SaveNamedProfile(p, "Изменён", false)} {
		if err == nil {
			t.Fatal("mutation while running")
		}
	}
	a.cancel = nil
	if err := a.DeleteProfile(second); err != nil {
		t.Fatal(err)
	}
	if a.GetSnapshot().ActiveID != first || a.GetSnapshot().Profile != p {
		t.Fatal("active deletion failed")
	}
	if err := a.DeleteProfile(first); err == nil {
		t.Fatal("deleted last profile")
	}
	if err := a.SelectProfile("missing"); err == nil {
		t.Fatal("unknown ID accepted")
	}
}
func TestProfileSaveFailurePreservesMemoryAndFile(t *testing.T) {
	a := newApp(t.TempDir())
	p := testProfile("192.0.2.1", "ab")
	if err := a.SaveNamedProfile(p, "Первый", true); err != nil {
		t.Fatal(err)
	}
	before := a.GetSnapshot()
	path := filepath.Join(a.root, "profiles.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(path, 0700)
	if err := a.SaveNamedProfile(testProfile("198.51.100.2", "cd"), "Второй", true); err == nil {
		t.Fatal("disk failure accepted")
	}
	after := a.GetSnapshot()
	if after.ActiveID != before.ActiveID || after.Profile != before.Profile || len(after.Profiles) != 1 {
		t.Fatal("memory changed before disk commit")
	}
	temps, _ := filepath.Glob(filepath.Join(a.root, ".profiles-*"))
	if len(temps) != 0 {
		t.Fatal("orphan secret temp")
	}
}
func TestCorruptProfileBookPreserved(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "profiles.json")
	data := []byte(`{"version":99}`)
	os.WriteFile(path, data, 0600)
	a := newApp(root)
	if err := a.SaveProfile(testProfile("192.0.2.1", "ab")); err == nil {
		t.Fatal("corrupt book overwritten")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(data) {
		t.Fatal("corrupt data lost")
	}
}
func TestLogsRedactKeysFromAllProfiles(t *testing.T) {
	a := newApp(t.TempDir())
	p := testProfile("192.0.2.1", "ab")
	a.SaveNamedProfile(p, "Один", true)
	p2 := testProfile("198.51.100.2", "cd")
	a.SaveNamedProfile(p2, "Два", true)
	a.appendLog(p.Key + " " + p2.Key)
	data, _ := os.ReadFile(filepath.Join(a.root, "ui.log"))
	if strings.Contains(string(data), p.Key) || strings.Contains(string(data), p2.Key) {
		t.Fatal("key logged")
	}
}
