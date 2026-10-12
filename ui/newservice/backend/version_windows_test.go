package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoreVersionParsingDoesNotMislabelOldCore(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"FturnRaw core 0.1.9\n", "0.1.9"}, {"FturnRaw core development", "development"}, {"flag provided but not defined: -version", "unknown"}, {"FturnRaw core ", "unknown"}, {"FturnRaw core 0.1.9\nextra", "unknown"}} {
		if got := parseCoreVersion(tc.input); got != tc.want {
			t.Fatalf("version %q: %q", tc.input, got)
		}
	}
}
func TestVersionHeaderRecordsUnknownCoreWithoutDiscardingOldLog(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "ui.log")
	_ = os.WriteFile(log, []byte("older session\n"), 0600)
	a := newApp(dir)
	a.appendVersionLog("connection")
	data, e := os.ReadFile(log)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(string(data), "older session\n") || !strings.Contains(string(data), "[INFO] [VERSION] connection: FturnRaw UI="+Version+"; core=unknown") {
		t.Fatalf("missing version separator: %s", data)
	}
}
func TestServiceDNSPortIsPassedToRouteScript(t *testing.T) {
	a := newApp(t.TempDir())
	a.controlDNSPort = 53000
	args := a.routeArgs("Apply", 123, "full")
	for i, v := range args {
		if v == "-ServiceDNSPort" && i+1 < len(args) && args[i+1] == "53000" {
			return
		}
	}
	t.Fatalf("missing service port: %v", args)
}
