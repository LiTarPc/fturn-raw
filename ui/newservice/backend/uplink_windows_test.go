package backend

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestUplinkProfilePersistenceShareAndArgs(t *testing.T) {
	root := t.TempDir()
	a := newApp(root)
	p := testProfile("192.0.2.1", "ab")
	p.UplinkMode = "tcp-flow"
	if err := a.SaveNamedProfile(p, "flow", true); err != nil {
		t.Fatal(err)
	}
	if got := newApp(root).GetSnapshot().Profile; got != p {
		t.Fatal("mode lost on restart")
	}
	link, err := a.ExportProfile(p)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := decodeShare(link)
	if err != nil || got != p {
		t.Fatal("mode lost in share", err)
	}
	args := rawClientArgs(p, p.Key, 17, t.TempDir(), "")
	found := false
	for i := 0; i < len(args); i += 2 {
		if args[i] == "-raw-uplink" {
			found = args[i+1] == "tcp-flow"
		}
	}
	if !found {
		t.Fatal("mode not passed to core")
	}
	data, _ := base64.RawURLEncoding.DecodeString(link[len(sharePrefix):])
	var wire sharedProfile
	json.Unmarshal(data, &wire)
	if wire.Version != 4 {
		t.Fatal("mode needs v4 share")
	}
	for _, version := range []int{1, 2, 3, 5} {
		wire.Version = version
		data, _ = json.Marshal(wire)
		if _, _, err := decodeShare(sharePrefix + base64.RawURLEncoding.EncodeToString(data)); err == nil {
			t.Fatal("downgraded/unknown mode accepted")
		}
	}
	p.UplinkMode = "invalid"
	if err := a.SaveNamedProfile(p, "bad", true); err == nil {
		t.Fatal("bad mode saved")
	}
	if _, err := a.ExportProfile(p); err == nil {
		t.Fatal("bad mode exported")
	}
	p.UplinkMode = ""
	if profileDefaults(p).UplinkMode != "packet" {
		t.Fatal("legacy mode default changed")
	}
}
