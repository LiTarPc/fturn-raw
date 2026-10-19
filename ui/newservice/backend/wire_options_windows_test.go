package backend

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWireOptionsSavedSharedAndPassedToCore(t *testing.T) {
	for _, transport := range []string{"tcp", "udp"} {
		for _, profile := range []string{"rtpopus2", "rtpopus3"} {
			t.Run(transport+"/"+profile, func(t *testing.T) {
				root := t.TempDir()
				a := newApp(root)
				p := testProfile("192.0.2.1", "ab")
				p.Transport = transport
				p.ObfProfile = profile
				if err := a.SaveNamedProfile(p, "test", true); err != nil {
					t.Fatal(err)
				}
				if got := newApp(root).GetSnapshot().Profile; got != p {
					t.Fatal("options lost on restart")
				}
				link, err := a.ExportProfile(p)
				if err != nil {
					t.Fatal(err)
				}
				got, _, err := decodeShare(link)
				if err != nil || got != p {
					t.Fatal("options lost in share", err)
				}
				args := rawClientArgs(got, got.Key, 17, t.TempDir(), "")
				flags := map[string]string{}
				for i := 0; i < len(args); i += 2 {
					flags[args[i]] = args[i+1]
				}
				if flags["-transport"] != transport || flags["-obf-profile"] != profile {
					t.Fatal("wrong core transport/profile")
				}
				data, _ := base64.RawURLEncoding.DecodeString(link[len(sharePrefix):])
				var shared sharedProfile
				json.Unmarshal(data, &shared)
				if (transport != "tcp" || profile != "rtpopus2") && shared.Version != 3 {
					t.Fatal("new options need explicit v3 link")
				}
			})
		}
	}
}

func TestLegacyWireOptionsDefaultWithoutRewriting(t *testing.T) {
	p := testProfile("192.0.2.1", "ab")
	p.Transport = ""
	p.ObfProfile = ""
	book := profileBook{1, "old", []SavedProfile{{"old", "legacy", p}}}
	data, _ := json.Marshal(book)
	root := t.TempDir()
	path := filepath.Join(root, "profiles.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	got := newApp(root).GetSnapshot().Profile
	if got.Transport != "tcp" || got.ObfProfile != "rtpopus2" {
		t.Fatal("legacy defaults changed")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(data) {
		t.Fatal("loading rewrote legacy profile")
	}
}

func TestWireOptionsRejectInvalidAndDowngradeShares(t *testing.T) {
	a := newApp(t.TempDir())
	for _, pair := range [][2]string{{"quic", "rtpopus2"}, {"tcp", "none"}, {"udp", "auto"}} {
		p := testProfile("192.0.2.1", "ab")
		p.Transport = pair[0]
		p.ObfProfile = pair[1]
		if err := a.SaveNamedProfile(p, "bad", true); err == nil {
			t.Fatal("invalid options saved")
		}
		if _, err := a.ExportProfile(p); err == nil {
			t.Fatal("invalid options exported")
		}
	}
	if len(a.GetSnapshot().Profiles) != 0 {
		t.Fatal("invalid profile changed storage")
	}
	p := testProfile("192.0.2.1", "ab")
	p.Transport = "udp"
	p.ObfProfile = "rtpopus3"
	link, err := a.ExportProfile(p)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := base64.RawURLEncoding.DecodeString(link[len(sharePrefix):])
	var wire sharedProfile
	json.Unmarshal(data, &wire)
	for _, v := range []int{1, 2, 4} {
		wire.Version = v
		data, _ = json.Marshal(wire)
		if _, _, err := decodeShare(sharePrefix + base64.RawURLEncoding.EncodeToString(data)); err == nil {
			t.Fatal("ambiguous or unknown share accepted")
		}
	}
}
