package rawsocks

import (
	"log"
	"net"
	"sync"
	"time"
)

type handlerState struct {
	started  time.Time
	kind     string
	activity *associationActivity
}
type bridgeCounters struct {
	mu             sync.Mutex
	handlers       map[net.Conn]*handlerState
	accepted       uint64
	rejected       uint64
	peak           int
	closed         map[string]uint64
	lastRejectLog  time.Time
	lastRequestLog time.Time
}

// Stats contains no destinations, credentials or packet contents.
type Stats struct {
	Limit, Slots, Active, Free, Peak, Handshake, TCP, UDP, IdleUDP int
	Accepted, Rejected                                             uint64
	Oldest, OldestUDPIdle                                          time.Duration
	Closed                                                         map[string]uint64
}

func (d *Device) Stats() Stats {
	d.counters.mu.Lock()
	defer d.counters.mu.Unlock()
	s := Stats{Limit: cap(d.slots), Active: len(d.counters.handlers), Peak: d.counters.peak,
		Accepted: d.counters.accepted, Rejected: d.counters.rejected, Closed: map[string]uint64{}}
	s.Slots = len(d.slots)
	s.Free = s.Limit - s.Slots
	for _, h := range d.counters.handlers {
		s.Oldest = max(s.Oldest, time.Since(h.started))
		switch h.kind {
		case "tcp":
			s.TCP++
		case "udp":
			s.UDP++
			if h.activity != nil {
				last, _ := h.activity.snapshot()
				idle := time.Since(last)
				s.OldestUDPIdle = max(s.OldestUDPIdle, idle)
				if idle >= 30*time.Second {
					s.IdleUDP++
				}
			}
		default:
			s.Handshake++
		}
	}
	for k, v := range d.counters.closed {
		s.Closed[k] = v
	}
	return s
}
func (d *Device) beginHandler(c net.Conn) {
	d.counters.mu.Lock()
	defer d.counters.mu.Unlock()
	if d.counters.handlers == nil {
		d.counters.handlers = map[net.Conn]*handlerState{}
	}
	d.counters.handlers[c] = &handlerState{started: time.Now(), kind: "handshake"}
	d.counters.accepted++
	d.counters.peak = max(d.counters.peak, len(d.counters.handlers))
}
func (d *Device) handlerKind(c net.Conn, kind string, a *associationActivity) {
	d.counters.mu.Lock()
	defer d.counters.mu.Unlock()
	if h := d.counters.handlers[c]; h != nil {
		h.kind = kind
		h.activity = a
	}
}
func (d *Device) endHandler(c net.Conn, reason string) {
	d.counters.mu.Lock()
	defer d.counters.mu.Unlock()
	delete(d.counters.handlers, c)
	if d.counters.closed == nil {
		d.counters.closed = map[string]uint64{}
	}
	d.counters.closed[reason]++
}
func (d *Device) rejectedHandler() {
	d.counters.mu.Lock()
	d.counters.rejected++
	emit := time.Since(d.counters.lastRejectLog) >= 5*time.Second
	if emit {
		d.counters.lastRejectLog = time.Now()
	}
	d.counters.mu.Unlock()
	if emit {
		d.logStats("[WARN]", "admission_limit")
	}
}
func (d *Device) logStats(level, event string) {
	s := d.Stats()
	log.Printf("%s [RAW SOCKS] event=%s slots=%d/%d free=%d handlers=%d peak=%d handshake=%d tcp=%d udp=%d udp_idle_30s=%d oldest_s=%.1f oldest_udp_idle_s=%.1f accepted=%d rejected=%d closed=%v",
		level, event, s.Slots, s.Limit, s.Free, s.Active, s.Peak, s.Handshake, s.TCP, s.UDP, s.IdleUDP, s.Oldest.Seconds(), s.OldestUDPIdle.Seconds(), s.Accepted, s.Rejected, s.Closed)
}
func (d *Device) reportStats() {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-d.ctx.Done():
			return
		case <-tick.C:
			d.logStats("[INFO]", "status")
		}
	}
}

// Request metadata is bounded and contains neither target address nor credentials.
func (d *Device) logRequestRejection(command, atyp byte, reason string) {
	d.counters.mu.Lock()
	emit := time.Since(d.counters.lastRequestLog) >= 5*time.Second
	if emit {
		d.counters.lastRequestLog = time.Now()
	}
	d.counters.mu.Unlock()
	if emit {
		log.Printf("[WARN] [RAW SOCKS] event=request_rejected phase=address command=%d atyp=%d reason=%s", command, atyp, reason)
	}
}
