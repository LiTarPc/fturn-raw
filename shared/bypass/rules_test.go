package bypassrules

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	s, e := Normalize(Settings{Sites: []string{"HTTPS://Example.ORG/path", "*.example.org", "203.0.113.47/24", "203.0.113.9", ".ru"}, Apps: []string{"Game.EXE", `C:\Games\game.exe`, "game.exe"}})
	if e != nil {
		t.Fatal(e)
	}
	if strings.Join(s.Sites, ",") != "203.0.113.0/24,203.0.113.9,example.org,ru" || len(s.Apps) != 2 {
		t.Fatalf("unexpected normalized rules: %+v", s)
	}
}
func TestInvalidRulesAndConfig(t *testing.T) {
	for _, v := range []string{"0.0.0.0/0", "::1", "example.org:443", "bad..domain", "русский.рф", "https://user:pass@example.org"} {
		if _, e := Site(v); e == nil {
			t.Errorf("accepted %q", v)
		}
	}
	for _, v := range []string{"game", "../game.exe", "*game.exe", "C:\\Games\\\ngame.exe"} {
		if _, e := Normalize(Settings{Apps: []string{v}}); e == nil {
			t.Errorf("accepted app %q", v)
		}
	}
	for _, v := range []string{`{"version":2}`, `{"version":1,"bad":true}`, `{"version":1} {}`, `{"version":1,"cidrs":["::/0"]}`} {
		if _, e := Decode(strings.NewReader(v)); e == nil {
			t.Errorf("accepted config %s", v)
		}
	}
}
func TestCIDRData(t *testing.T) {
	v, e := ParseCIDRs(strings.NewReader("203.0.113.1/24\n203.0.113.0/24\n198.51.100.0/24\n"))
	if e != nil || len(v) != 2 {
		t.Fatalf("dedup failed %v %v", v, e)
	}
	for _, v := range []string{"", "0.0.0.0/0", "::/64", "<html>bad gateway</html>", "203.0.113.0/33", strings.Repeat(" ", MaxData+1)} {
		if _, e := ParseCIDRs(strings.NewReader(v)); e == nil {
			t.Fatal("accepted invalid list")
		}
	}
}

func TestOversizedConfig(t *testing.T) {
	if _, e := Decode(strings.NewReader(`{"version":1}` + strings.Repeat(" ", MaxData))); e == nil {
		t.Fatal("oversized JSON accepted")
	}
}
