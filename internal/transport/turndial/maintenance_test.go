package turndial

import (
	"github.com/pion/logging"
	"testing"
)

func TestMaintenanceTracksSuccessAndFailureWithoutPayload(t *testing.T) {
	state := &MaintenanceState{}
	f := &permWatchFactory{inner: logging.NewDefaultLoggerFactory(), threshold: 2, state: state}
	log := f.NewLogger(turncScope)
	log.Debugf("Send refresh request (dontWait=%v)", false)
	log.Debug("Refresh request sent")
	log.Debugf("Updated lifetime: %d seconds", 600)
	log.Debugf("ChannelBind request: peer=%s channel=%d", "PRIVATE_PEER", 16384)
	log.Debug("Channel binding successful: private")
	log.Warnf("Failed to bind channel: %s", "PRIVATE_SECRET")
	snapshot := state.Snapshot()
	if snapshot.RefreshAttempt != 1 || snapshot.RefreshOK != 1 || snapshot.BindAttempt != 1 || snapshot.BindOK != 1 || snapshot.BindFail != 1 || snapshot.RefreshAgeMS < 0 || snapshot.BindAgeMS < 0 {
		t.Fatalf("wrong maintenance counters: %+v", snapshot)
	}
}
