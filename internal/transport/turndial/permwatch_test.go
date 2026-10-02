package turndial

import (
	"sync/atomic"
	"testing"

	"github.com/pion/logging"
)

func newTestWatch(threshold int) (*permWatchFactory, *atomic.Int32) {
	var fired atomic.Int32
	f := &permWatchFactory{
		inner:     logging.NewDefaultLoggerFactory(),
		threshold: threshold,
		onDead:    func() { fired.Add(1) },
	}
	return f, &fired
}

func TestPermWatchFiresAfterThreshold(t *testing.T) {
	f, fired := newTestWatch(2)
	log := f.NewLogger(turncScope)

	log.Warnf(permFailMarker+": %s", "boom")
	if fired.Load() != 0 {
		t.Fatalf("fired too early after 1 fail: %d", fired.Load())
	}
	log.Warnf(permFailMarker+": %s", "boom")
	if fired.Load() != 1 {
		t.Fatalf("expected 1 fire after threshold, got %d", fired.Load())
	}
	log.Warnf(permFailMarker+": %s", "boom")
	if fired.Load() != 1 {
		t.Fatalf("fired more than once: %d", fired.Load())
	}
}

func TestPermWatchRecyclesAfterRepeatedSavedBinding400(t *testing.T) {
	f, fired := newTestWatch(2)
	log := f.NewLogger(turncScope)

	msg := permSavedBind400Marker + " %s on channel %d; keeping binding ready"
	log.Warnf(msg, "192.0.2.1:56660", 16384)
	if fired.Load() != 0 {
		t.Fatalf("fired too early after first 400: %d", fired.Load())
	}
	log.Warnf(msg, "192.0.2.1:56660", 16384)
	if fired.Load() != 1 {
		t.Fatalf("expected recycle after repeated 400, got %d", fired.Load())
	}
	log.Warnf(msg, "192.0.2.1:56660", 16384)
	if fired.Load() != 1 {
		t.Fatalf("recycled more than once: %d", fired.Load())
	}
}

func TestPermWatchResetOnSuccess(t *testing.T) {
	f, fired := newTestWatch(2)
	log := f.NewLogger(turncScope)

	log.Warnf(permFailMarker + ": x")
	log.Debug(permOKMarker)
	log.Warnf(permFailMarker + ": x")
	if fired.Load() != 0 {
		t.Fatalf("reset failed: fired=%d (fail/ok/fail не должно фаерить)", fired.Load())
	}
}

func TestPermWatchIgnoresOtherScopes(t *testing.T) {
	f, fired := newTestWatch(1)
	log := f.NewLogger("other")
	if _, ok := log.(*permWatchLogger); ok {
		t.Fatal("non-turnc scope must not be wrapped")
	}
	log.Warnf(permFailMarker)
	if fired.Load() != 0 {
		t.Fatalf("fired on non-turnc scope: %d", fired.Load())
	}
}

func TestPermWatchIgnoresUnrelatedMessages(t *testing.T) {
	f, fired := newTestWatch(1)
	log := f.NewLogger(turncScope)
	log.Debug("Started refresh permission timer")
	log.Debug("No permission to refresh")
	log.Warnf("Failed to refresh allocation: %s", "x")
	if fired.Load() != 0 {
		t.Fatalf("fired on unrelated message: %d", fired.Load())
	}
}

func TestRawRefreshFailureRecyclesImmediately(t *testing.T) {
	f, fired := newTestWatch(2)
	f.allocationFailure = true
	l := f.NewLogger(turncScope)
	l.Warnf("Failed to refresh allocation: %s", "transaction closed")
	if fired.Load() != 1 {
		t.Fatal("allocation failure did not recycle raw worker")
	}
	l.Warn("Failed to refresh allocation: again")
	if fired.Load() != 1 {
		t.Fatal("recycle fired twice")
	}
}
func TestMaintenanceDiagnosticsDoNotPromoteSecrets(t *testing.T) {
	for _, msg := range []string{"Username: secret", "Password: secret", "Nonce: secret", "STUN frame: secret"} {
		if maintenanceMessage(msg) {
			t.Fatalf("secret diagnostic promoted: %s", msg)
		}
	}
	if !maintenanceMessage("Updated lifetime: 600 seconds") {
		t.Fatal("maintenance status not promoted")
	}
}
