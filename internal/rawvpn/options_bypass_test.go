package rawvpn

import "testing"

func TestParseBypassFile(t *testing.T) {
	o, rest, e := ParseClientOptions([]string{"-bypass-file", `C:\Config\bypass.json`, "-peer", "192.0.2.1:56010"})
	if e != nil || o.BypassFile != `C:\Config\bypass.json` || len(rest) != 2 {
		t.Fatal("bypass flag not separated", e)
	}
}
