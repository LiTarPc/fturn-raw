package backend

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestRouteCommandsShareRecoveryState(t *testing.T) {
	a := newAppWithData(t.TempDir(), t.TempDir())
	for _, action := range []string{"Plan", "Recover", "Apply", "Watch", "Remove"} {
		args := a.routeArgs(action, 1234, "full")
		values := map[string]string{}
		for i := 1; i < len(args); i++ {
			if strings.HasPrefix(args[i-1], "-") {
				values[args[i-1]] = args[i]
			}
		}
		if values["-StateFile"] != filepath.Join(a.dataDir, "network", "route-state.json") || values["-Action"] != action || values["-ClientPid"] != "1234" || values["-LegacyDirectory"] != a.root {
			t.Fatalf("inconsistent recovery command: %v", args)
		}
	}
}

func TestGuardRequiresExplicitReadiness(t *testing.T) {
	for _, output := range []string{"", "WARNING: failed\n", "not READY\n"} {
		if readGuardReady(strings.NewReader(output)) == nil {
			t.Fatalf("accepted %q", output)
		}
	}
	if err := readGuardReady(strings.NewReader("READY\r\n")); err != nil {
		t.Fatal(err)
	}
	if err := readGuardReady(failedReader{}); err != io.ErrUnexpectedEOF {
		t.Fatalf("lost pipe failure: %v", err)
	}
}

type failedReader struct{}

func (failedReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
