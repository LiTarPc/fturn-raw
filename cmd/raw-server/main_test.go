package main

import (
	"os"
	"strings"
	"testing"
)

func TestInvalidDevicePoolRejectedBeforeOpeningTUN(t *testing.T) {
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	for _, tc := range []struct {
		args    []string
		message string
	}{
		{[]string{"-max-devices", "0"}, "max-devices"},
		{[]string{"-max-devices", "-1"}, "max devices"},
		{[]string{"-max-devices", "65"}, "max devices"},
		{[]string{"-address", "10.77.0.1/30", "-max-devices", "3"}, "client pool address"},
		{[]string{"-address", "10.77.0.3/24", "-max-devices", "3"}, "client pool address"},
		{[]string{"-client-ip", "10.77.0.254", "-max-devices", "3"}, "client pool address"},
	} {
		os.Args = append([]string{"raw-server", "-obf-key", strings.Repeat("01", 32)}, tc.args...)
		err := run()
		if err == nil || !strings.Contains(err.Error(), tc.message) {
			t.Fatalf("%v: got %v, want %q", tc.args, err, tc.message)
		}
	}
}
