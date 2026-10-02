package backend

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortableShareRoundTrip(t *testing.T) {
	source, dest := t.TempDir(), t.TempDir()
	secret := strings.Repeat("ab", 32)
	os.WriteFile(filepath.Join(source, "raw.key"), []byte(secret), 0600)
	a := newApp(source)
	p := Profile{Server: "192.0.2.1:56010", VkLink: "https://vk.ru/call/join/test", KeyFile: "raw.key", Mtu: 1420, Streams: 10, RouteMode: "full"}
	link, err := a.ExportProfile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(link, source) {
		t.Fatal("local path leaked")
	}
	b := newApp(dest)
	got, err := b.ImportProfile(link)
	if err != nil {
		t.Fatal(err)
	}
	if got.Server != p.Server || got.VkLink != p.VkLink || got.Mtu != p.Mtu || got.Streams != p.Streams || got.RouteMode != p.RouteMode {
		t.Fatal("settings did not round trip")
	}
	if got.Key != secret || got.KeyFile != "" {
		t.Fatal("inline key did not round trip")
	}
	if newApp(dest).GetSnapshot().Profile != got {
		t.Fatal("profile not saved")
	}
	if _, err = b.ImportProfile(link); err != nil {
		t.Fatal("repeated import failed", err)
	}
	if len(b.GetSnapshot().Logs) != 0 {
		t.Fatal("private link logged")
	}
	b.cancel = func() {}
	before := b.GetSnapshot()
	if _, err = b.ImportProfile(link); err != nil {
		t.Fatal("duplicate import while connected failed", err)
	}
	after := b.GetSnapshot()
	if after.ActiveID != before.ActiveID || after.Profile != before.Profile || len(after.Profiles) != len(before.Profiles) {
		t.Fatal("duplicate import changed connection or added an entry")
	}
}
func TestMalformedSharesLeaveProfileAndFilesUntouched(t *testing.T) {
	a := newApp(t.TempDir())
	before := a.GetSnapshot().Profile
	encode := func(s string) string { return sharePrefix + base64.RawURLEncoding.EncodeToString([]byte(s)) }
	valid := `{"v":1,"server":"192.0.2.1:56010","vk":"https://vk.ru/call/join/test","key":"` + strings.Repeat("ab", 32) + `","mtu":1420,"streams":10,"mode":"full"}`
	for _, link := range []string{"https://example.com", sharePrefix + "%", strings.Repeat("a", maxShareLength+1), encode(strings.Replace(valid, `"v":1`, `"v":2`, 1)), encode(strings.Replace(valid, `"mtu":1420`, `"mtu":9999`, 1)), encode(strings.Replace(valid, `"ab`, `"zz`, 1)), encode(valid + ` {}`), encode(strings.Replace(valid, `"v":1`, `"evil":"../raw.key","v":1`, 1))} {
		if _, err := a.ImportProfile(link); err == nil {
			t.Fatal("malformed share accepted")
		}
		entries, _ := os.ReadDir(a.root)
		if len(entries) != 0 || a.GetSnapshot().Profile != before {
			t.Fatal("invalid import changed state")
		}
	}
}
func TestImportRollsBackKeyWhenProfileSaveFails(t *testing.T) {
	root := t.TempDir()
	a := newApp(root)
	os.Mkdir(filepath.Join(root, "profiles.json"), 0700)
	wire := `{"v":1,"server":"192.0.2.1:56010","vk":"https://vk.ru/call/join/test","key":"` + strings.Repeat("ab", 32) + `","mtu":1420,"streams":10,"mode":"full"}`
	if _, err := a.ImportProfile(sharePrefix + base64.RawURLEncoding.EncodeToString([]byte(wire))); err == nil {
		t.Fatal("save should fail")
	}
	files, _ := filepath.Glob(filepath.Join(root, "*.key"))
	if len(files) != 0 {
		t.Fatal("orphan key retained")
	}
}
