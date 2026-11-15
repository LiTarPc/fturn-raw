package rawvpn

import (
	"sync/atomic"
	"time"

	"github.com/samosvalishe/free-turn-proxy/internal/logx"
	"github.com/samosvalishe/free-turn-proxy/internal/transport/turndial"
)

type streamHealth struct {
	started                                                                             time.Time
	wireAt, decodedAt, validAt, pongAt                                                  atomic.Int64
	wirePackets, validPackets, decodeReject, foreign, invalidFrame, fragmentWait, pings atomic.Uint64
	deviceWriteFailed                                                                   atomic.Bool
}

func newStreamHealth() *streamHealth {
	h := &streamHealth{started: time.Now()}
	h.pongAt.Store(-1)
	h.wireAt.Store(-1)
	h.decodedAt.Store(-1)
	return h
}
func (h *streamHealth) mark(v *atomic.Int64) { v.Store(time.Since(h.started).Nanoseconds()) }
func (h *streamHealth) age(v *atomic.Int64) int64 {
	n := v.Load()
	if n < 0 {
		return -1
	}
	return max(int64(0), (time.Since(h.started).Nanoseconds()-n)/int64(time.Millisecond))
}
func (h *streamHealth) report(log logx.Logger, id int, transport, phase, cause string, state *turndial.MaintenanceState) {
	m := state.Snapshot()
	log.Infof("[RAW HEALTH] stream=%d transport=%s phase=%s cause=%s age_ms=%d wire_age_ms=%d decoded_age_ms=%d valid_age_ms=%d pong_age_ms=%d received=%d valid=%d decode_reject=%d foreign=%d frame_reject=%d fragment_wait=%d ping_sent=%d refresh_attempt=%d refresh_ok=%d refresh_fail=%d refresh_age_ms=%d bind_attempt=%d bind_ok=%d bind_fail=%d bind_age_ms=%d",
		id, transport, phase, cause, time.Since(h.started).Milliseconds(), h.age(&h.wireAt), h.age(&h.decodedAt), h.age(&h.validAt), h.age(&h.pongAt), h.wirePackets.Load(), h.validPackets.Load(), h.decodeReject.Load(), h.foreign.Load(), h.invalidFrame.Load(), h.fragmentWait.Load(), h.pings.Load(), m.RefreshAttempt, m.RefreshOK, m.RefreshFail, m.RefreshAgeMS, m.BindAttempt, m.BindOK, m.BindFail, m.BindAgeMS)
}
