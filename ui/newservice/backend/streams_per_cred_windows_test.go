package backend

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStreamsPerCredSavedSharedAndPassedToCore(t *testing.T) {
	root := t.TempDir()
	a := newApp(root)
	p := testProfile("192.0.2.1", "ab")
	p.Streams = 20
	p.StreamsPerCred = 10
	if err := a.SaveNamedProfile(p, "test", true); err != nil {
		t.Fatal(err)
	}
	if got := newApp(root).GetSnapshot().Profile; got != p {
		t.Fatal("setting lost on restart")
	}
	link, err := a.ExportProfile(p)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := base64.RawURLEncoding.DecodeString(link[len(sharePrefix):])
	var wire sharedProfile
	json.Unmarshal(data, &wire)
	if wire.Version != 2 || wire.StreamsPerCred != 10 {
		t.Fatal("custom setting absent from link")
	}
	b := newApp(t.TempDir())
	got, err := b.ImportProfile(link)
	if err != nil || got != p {
		t.Fatalf("import=%v", err)
	}
	args := rawClientArgs(got, got.Key, 17, t.TempDir(), "session.json")
	flags := map[string]string{}
	for i := 0; i < len(args); i += 2 {
		flags[args[i]] = args[i+1]
	}
	if flags["-n"] != "20" || flags["-streams-per-cred"] != "10" || flags["-control-interface"] != "17" || flags["-transport"] != "tcp" || flags["-bypass-file"] != "session.json" {
		t.Fatal("incorrect core arguments")
	}
}
func TestLegacyProfilesAndLinksKeepFiveStreamsPerCred(t *testing.T) {
	root := t.TempDir()
	p := testProfile("192.0.2.1", "ab")
	p.StreamsPerCred = 0
	legacy := profileBook{1, "old", []SavedProfile{{"old", "legacy", p}}}
	data, _ := json.Marshal(legacy)
	var raw map[string]any
	json.Unmarshal(data, &raw)
	profiles := raw["profiles"].([]any)
	delete(profiles[0].(map[string]any)["profile"].(map[string]any), "StreamsPerCred")
	data, _ = json.Marshal(raw)
	path := filepath.Join(root, "profiles.json")
	os.WriteFile(path, data, 0600)
	a := newApp(root)
	if a.GetSnapshot().Profile.StreamsPerCred != 5 {
		t.Fatal("legacy default changed")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(data) {
		t.Fatal("loading overwrote legacy file")
	}
	link, err := a.ExportProfile(a.GetSnapshot().Profile)
	if err != nil {
		t.Fatal(err)
	}
	decoded, _ := base64.RawURLEncoding.DecodeString(link[len(sharePrefix):])
	var wire sharedProfile
	json.Unmarshal(decoded, &wire)
	if wire.Version != 1 || wire.StreamsPerCred != 0 {
		t.Fatal("default link lost backward compatibility")
	}
	got, _, err := decodeShare(link)
	if err != nil || got.StreamsPerCred != 5 {
		t.Fatal("legacy link default lost")
	}
}
func TestStreamsPerCredRejectsInvalidValuesWithoutSaving(t *testing.T) {
	a := newApp(t.TempDir())
	p := testProfile("192.0.2.1", "ab")
	for _, n := range []int{-1, 65} {
		p.StreamsPerCred = n
		if err := a.SaveProfile(p); err == nil {
			t.Fatal("invalid value saved")
		}
		if _, err := a.ExportProfile(p); err == nil {
			t.Fatal("invalid value exported")
		}
	}
	if len(a.GetSnapshot().Profiles) != 0 {
		t.Fatal("invalid profile persisted")
	}
}
