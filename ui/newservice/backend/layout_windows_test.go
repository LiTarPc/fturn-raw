package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCleanLayoutPreservesIdentityAndArchivesLegacyFiles(t *testing.T) {
	root, dataDir := t.TempDir(), t.TempDir()
	runtimeDir := filepath.Join(root, "runtime")
	os.Mkdir(runtimeDir, 0700)
	os.WriteFile(filepath.Join(runtimeDir, "raw-client.exe"), []byte("mock"), 0600)
	if runtimeDirectory(root) != runtimeDir {
		t.Fatal("nested runtime not selected")
	}
	files := map[string]string{"client_config.json": `{"client_id":"persistent-identity"}`, "vk_persona.json": `{"seed":"persistent-identity","generation":2}`, "connection.json": `{}`}
	for name, data := range files {
		os.WriteFile(filepath.Join(root, name), []byte(data), 0600)
	}
	os.WriteFile(filepath.Join(root, "raw.key"), []byte("keep-private-key"), 0600)
	os.WriteFile(filepath.Join(root, "notes.txt"), []byte("keep-notes"), 0600)
	if err := migrateCoreState(root, dataDir); err != nil {
		t.Fatal(err)
	}
	if err := archiveLegacyData(root, dataDir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"client_config.json", "vk_persona.json"} {
		data, err := os.ReadFile(filepath.Join(coreStateDirectory(dataDir), name))
		if err != nil || string(data) != files[name] {
			t.Fatalf("identity not preserved: %s", name)
		}
	}
	for name, data := range files {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("legacy file retained: %s", name)
		}
		backups, _ := filepath.Glob(filepath.Join(dataDir, "legacy-install", name+"-*.bak"))
		if len(backups) != 1 {
			t.Fatal("backup missing")
		}
		b, _ := os.ReadFile(backups[0])
		if string(b) != data {
			t.Fatal("backup differs")
		}
	}
	for _, name := range []string{"raw.key", "notes.txt"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatal("unrelated file lost")
		}
	}
	// Later copies cannot replace the existing identity.
	os.WriteFile(filepath.Join(root, "client_config.json"), []byte(`{"client_id":"different"}`), 0600)
	if err := migrateCoreState(root, dataDir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(coreStateDirectory(dataDir), "client_config.json"))
	if string(b) != files["client_config.json"] {
		t.Fatal("identity replaced")
	}
}
func TestAuthPauseBlocksConnectBeforeRoutesOrCore(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	a := newAppWithData(root, data)
	until := time.Now().Add(time.Minute).Unix()
	os.Mkdir(coreStateDirectory(data), 0700)
	b, _ := json.Marshal(map[string]int64{"until": until})
	os.WriteFile(filepath.Join(coreStateDirectory(data), "vk_auth_pause.json"), b, 0600)
	if a.GetSnapshot().AuthRetryAt != until {
		t.Fatal("pause absent from snapshot")
	}
	if err := a.Connect(); err == nil || !strings.Contains(err.Error(), "VK") {
		t.Fatalf("guard error=%v", err)
	}
	if a.cancel != nil || a.cmd != nil {
		t.Fatal("process started during pause")
	}
	for _, b := range []string{`{"until":1}`, `{"until":9999999999999}`, strings.Repeat(" ", 1025)} {
		os.WriteFile(filepath.Join(coreStateDirectory(data), "vk_auth_pause.json"), []byte(b), 0600)
		if a.GetSnapshot().AuthRetryAt != 0 {
			t.Fatal("invalid pause accepted")
		}
	}
}

func TestLegacyBackupConflictKeepsOriginal(t *testing.T) {
	root, dataDir := t.TempDir(), t.TempDir()
	os.Mkdir(filepath.Join(root, "runtime"), 0700)
	os.WriteFile(filepath.Join(root, "runtime", "raw-client.exe"), []byte("test"), 0600)
	source := filepath.Join(root, "connection.json")
	data := []byte(`{"server":"original"}`)
	os.WriteFile(source, data, 0600)
	if err := archiveLegacyData(root, dataDir); err != nil {
		t.Fatal(err)
	}
	backups, _ := filepath.Glob(filepath.Join(dataDir, "legacy-install", "*.bak"))
	if len(backups) != 1 {
		t.Fatal("missing backup")
	}
	os.WriteFile(backups[0], []byte("different backup"), 0600)
	os.WriteFile(source, data, 0600)
	if err := archiveLegacyData(root, dataDir); err == nil {
		t.Fatal("corrupt existing backup accepted")
	}
	got, err := os.ReadFile(source)
	if err != nil || string(got) != string(data) {
		t.Fatal("original removed before backup verification")
	}
}
