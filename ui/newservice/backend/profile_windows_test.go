package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileValidationAndRoundTrip(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "raw.key"), []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	p := Profile{Server: "192.0.2.1:56010", VkLink: "https://vk.ru/call/join/test", KeyFile: "raw.key", Mtu: 1420, Streams: 10, RouteMode: "full"}
	a := newApp(root)
	if err := a.SaveProfile(p); err != nil {
		t.Fatal(err)
	}
	if got := newApp(root).GetSnapshot(); got.Profile.Key != strings.Repeat("a", 64) || got.Profile.KeyFile != "" || got.Profile.Server != p.Server || got.State != "idle" {
		t.Fatalf("profile reload failed: %+v", got)
	}
	for _, mutate := range []func(*Profile){func(p *Profile) { p.Server = "192.0.2.1:0" }, func(p *Profile) { p.VkLink = "https://example.com/call/join/test" }, func(p *Profile) { p.Mtu = 1501 }, func(p *Profile) { p.Streams = 65 }, func(p *Profile) { p.KeyFile = "missing.key" }, func(p *Profile) { p.RouteMode = "unknown" }} {
		bad := p
		mutate(&bad)
		if _, err := validate(bad, root); err == nil {
			t.Fatal("invalid profile accepted")
		}
	}
}
func TestLogsRedactCallLinkAndStayBounded(t *testing.T) {
	a := newApp(t.TempDir())
	a.profile.VkLink = "https://vk.ru/call/join/private"
	a.appendLog(a.profile.VkLink)
	if strings.Contains(a.GetSnapshot().Logs[0], "/private") {
		t.Fatal("call link leaked in UI log")
	}
	for range 520 {
		a.appendLog("status")
	}
	if len(a.GetSnapshot().Logs) != 500 {
		t.Fatal("log buffer unbounded")
	}
}
