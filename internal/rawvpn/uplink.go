package rawvpn

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const (
	UplinkPacket   = "packet"
	UplinkTCPFlow  = "tcp-flow"
	uplinkCapacity = 256
	// Every path can use the shared budget; packet payloads still share the
	// single atomic 256-packet reservation. A busy flow must not drop at 64.
	uplinkPathCapacity       = uplinkCapacity
	uplinkFlowLimit          = 8192
	uplinkFlowTTL            = 2 * time.Minute
	uplinkFlowletGap         = 20 * time.Millisecond
	uplinkSwitchCooldown     = 150 * time.Millisecond
	uplinkRetransmitCooldown = 250 * time.Millisecond
	uplinkPenaltyDuration    = 15 * time.Second
	uplinkLoadWindow         = 500 * time.Millisecond
	uplinkFlowletMinPackets  = 12
)

func validateUplink(mode string) error {
	if mode != "" && mode != UplinkPacket && mode != UplinkTCPFlow {
		return fmt.Errorf("raw: uplink must be packet or tcp-flow")
	}
	return nil
}

type uplinkPacket struct {
	plain    []byte
	queuedAt time.Time
}
type uplinkPath struct {
	id          int
	generation  uint64
	packets     chan uplinkPacket
	rttNs       atomic.Int64
	badUntil    time.Time
	windowStart time.Time
	windowBytes uint64
}

func (p *uplinkPath) recordRTT(d time.Duration) {
	if p == nil || d <= 0 {
		return
	}
	sample := d.Nanoseconds()
	for {
		old := p.rttNs.Load()
		next := sample
		if old > 0 {
			next = (old*3 + sample) / 4
		}
		if p.rttNs.CompareAndSwap(old, next) {
			return
		}
	}
}

func (p *uplinkPath) rtt() time.Duration {
	if p == nil {
		return 0
	}
	return time.Duration(p.rttNs.Load())
}

func (p *uplinkPath) recentBytesAt(now time.Time) uint64 {
	if p == nil || p.windowBytes == 0 || p.windowStart.IsZero() {
		return 0
	}
	elapsed := now.Sub(p.windowStart)
	if elapsed <= 0 {
		return p.windowBytes
	}
	shifts := int(elapsed / uplinkLoadWindow)
	if shifts >= 8 {
		p.windowBytes = 0
		p.windowStart = now
		return 0
	}
	if shifts > 0 {
		p.windowBytes >>= shifts
		p.windowStart = p.windowStart.Add(time.Duration(shifts) * uplinkLoadWindow)
	}
	return p.windowBytes
}

func (p *uplinkPath) recordBytes(now time.Time, n uint64) {
	if p == nil {
		return
	}
	_ = p.recentBytesAt(now)
	if p.windowStart.IsZero() {
		p.windowStart = now
	}
	if n == 0 {
		n = 64
	}
	p.windowBytes += n
}

type tcpFlowKey [12]byte
type uplinkBinding struct {
	id            int
	generation    uint64
	seen          time.Time
	switchedAt    time.Time
	lastSeq       uint32
	hasSeq        bool
	packetsOnPath uint32
}

// A single dispatcher preserves TUN order when assigning TCP packets. Only
// authenticated, ready paths register. A path joining never moves active flows.
// All queues share the original 256-packet budget; congestion drops instead of
// moving a busy flow to another path and introducing cross-path reordering.
type uplinkQueue struct {
	mode       string
	input      chan uplinkPacket
	shared     chan uplinkPacket
	changed    chan struct{}
	queued     atomic.Int32
	drops      atomic.Uint64
	pathDrops  atomic.Uint64
	failovers  atomic.Uint64
	waitMax    atomic.Int64
	mu         sync.Mutex
	paths      []*uplinkPath
	flows      map[tcpFlowKey]uplinkBinding
	next       uint64
	generation uint64
}

func newUplinkQueue(mode string) *uplinkQueue {
	if mode == "" {
		mode = UplinkPacket
	}
	return &uplinkQueue{mode: mode, input: make(chan uplinkPacket, uplinkCapacity), shared: make(chan uplinkPacket, uplinkCapacity), changed: make(chan struct{}, 1), flows: make(map[tcpFlowKey]uplinkBinding)}
}
func (q *uplinkQueue) enqueue(plain []byte) {
	for {
		n := q.queued.Load()
		if n >= uplinkCapacity {
			q.drops.Add(1)
			return
		}
		if q.queued.CompareAndSwap(n, n+1) {
			break
		}
	}
	packet := uplinkPacket{plain: plain, queuedAt: time.Now()}
	target := q.shared
	if q.mode == UplinkTCPFlow {
		target = q.input
	}
	select {
	case target <- packet:
	default:
		q.queued.Add(-1)
		q.drops.Add(1)
	}
}
func (q *uplinkQueue) take(packet uplinkPacket) []byte {
	q.queued.Add(-1)
	wait := time.Since(packet.queuedAt).Nanoseconds()
	for old := q.waitMax.Load(); wait > old; old = q.waitMax.Load() {
		if q.waitMax.CompareAndSwap(old, wait) {
			break
		}
	}
	return packet.plain
}
func (q *uplinkQueue) notify() {
	select {
	case q.changed <- struct{}{}:
	default:
	}
}
func (q *uplinkQueue) register(id int) *uplinkPath {
	if q.mode != UplinkTCPFlow {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.generation++
	p := &uplinkPath{id: id, generation: q.generation, packets: make(chan uplinkPacket, uplinkPathCapacity)}
	q.paths = append(q.paths, p)
	q.notify()
	return p
}
func (q *uplinkQueue) unregister(p *uplinkPath) {
	if p == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, path := range q.paths {
		if path == p {
			q.paths = append(q.paths[:i], q.paths[i+1:]...)
			break
		}
	}
	// Never close: a worker may still be selecting its queue during cancellation.
	for {
		select {
		case <-p.packets:
			q.queued.Add(-1)
			q.pathDrops.Add(1)
		default:
			q.notify()
			return
		}
	}
}
func (q *uplinkQueue) run(ctx context.Context) {
	if q.mode != UplinkTCPFlow {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case packet := <-q.input:
			key, seq, payloadLen, ok := tcpUplinkPacketInfo(packet.plain)
			if !ok {
				select {
				case q.shared <- packet:
				case <-ctx.Done():
					q.queued.Add(-1)
					return
				}
				continue
			}
			for {
				q.mu.Lock()
				if len(q.paths) > 0 {
					p := q.choosePacketLocked(key, seq, payloadLen, true, time.Now())
					select {
					case p.packets <- packet:
					default:
						q.queued.Add(-1)
						q.pathDrops.Add(1)
					}
					q.mu.Unlock()
					break
				}
				q.mu.Unlock()
				select {
				case <-ctx.Done():
					q.queued.Add(-1)
					return
				case <-q.changed:
				}
			}
		}
	}
}
func (q *uplinkQueue) chooseLocked(key tcpFlowKey, now time.Time) *uplinkPath {
	return q.choosePacketLocked(key, 0, 0, false, now)
}

func (q *uplinkQueue) choosePacketLocked(key tcpFlowKey, seq uint32, payloadLen int, hasTCP bool, now time.Time) *uplinkPath {
	binding, exists := q.flows[key]
	if exists && now.Sub(binding.seen) < uplinkFlowTTL {
		var current *uplinkPath
		for _, p := range q.paths {
			if p.id == binding.id && p.generation == binding.generation {
				current = p
				break
			}
		}
		if current != nil {
			isRetransmit := hasTCP && payloadLen >= 16 && binding.hasSeq && int32(seq-binding.lastSeq) <= 0
			if isRetransmit && len(q.paths) > 1 && now.Sub(binding.switchedAt) >= uplinkRetransmitCooldown {
				current.badUntil = now.Add(uplinkPenaltyDuration)
				if alt := q.pickBestPathLocked(now, current); alt != nil && alt != current {
					q.failovers.Add(1)
					q.next++
					q.flows[key] = uplinkBinding{
						id:            alt.id,
						generation:    alt.generation,
						seen:          now,
						switchedAt:    now,
						lastSeq:       seq,
						hasSeq:        true,
						packetsOnPath: 1,
					}
					alt.recordBytes(now, uint64(payloadLen))
					return alt
				}
			}
			if len(q.paths) > 1 && now.Sub(binding.seen) >= uplinkFlowletGap && now.Sub(binding.switchedAt) >= uplinkSwitchCooldown {
				if alt := q.pickBestPathLocked(now, current); alt != nil && alt != current {
					curLoad := current.recentBytesAt(now)
					altLoad := alt.recentBytesAt(now)
					curRTT := current.rtt()
					altRTT := alt.rtt()
					wantSwitch := (current.badUntil.After(now) && !alt.badUntil.After(now)) ||
						(len(current.packets) >= 4 && len(alt.packets) <= len(current.packets)/2) ||
						(binding.packetsOnPath >= uplinkFlowletMinPackets && !alt.badUntil.After(now) && len(alt.packets) == 0 &&
							(altLoad < curLoad/2 || (curRTT > 0 && altRTT > 0 && altRTT*3 < curRTT*2)))
					if wantSwitch {
						q.next++
						q.flows[key] = uplinkBinding{
							id:            alt.id,
							generation:    alt.generation,
							seen:          now,
							switchedAt:    now,
							lastSeq:       seq,
							hasSeq:        hasTCP && payloadLen > 0,
							packetsOnPath: 1,
						}
						if hasTCP && payloadLen > 0 && binding.hasSeq && int32(binding.lastSeq-seq) > 0 {
							updated := q.flows[key]
							updated.lastSeq = binding.lastSeq
							q.flows[key] = updated
						}
						alt.recordBytes(now, uint64(payloadLen))
						return alt
					}
				}
			}
			binding.seen = now
			binding.packetsOnPath++
			if hasTCP && payloadLen > 0 && (!binding.hasSeq || int32(seq-binding.lastSeq) > 0) {
				binding.lastSeq = seq
				binding.hasSeq = true
			}
			q.flows[key] = binding
			current.recordBytes(now, uint64(payloadLen))
			return current
		}
		q.failovers.Add(1)
	}
	if !exists && len(q.flows) >= uplinkFlowLimit {
		var oldest tcpFlowKey
		var oldestTime time.Time
		for k, b := range q.flows {
			if oldestTime.IsZero() || b.seen.Before(oldestTime) {
				oldest, oldestTime = k, b.seen
			}
		}
		delete(q.flows, oldest)
	}
	best := q.pickBestPathLocked(now, nil)
	q.next++
	q.flows[key] = uplinkBinding{
		id:            best.id,
		generation:    best.generation,
		seen:          now,
		switchedAt:    now,
		lastSeq:       seq,
		hasSeq:        hasTCP && payloadLen > 0,
		packetsOnPath: 1,
	}
	best.recordBytes(now, uint64(payloadLen))
	return best
}

func (q *uplinkQueue) pickBestPathLocked(now time.Time, exclude *uplinkPath) *uplinkPath {
	n := len(q.paths)
	if n == 0 {
		return nil
	}
	start := int(q.next % uint64(n))
	var best *uplinkPath
	for off := 0; off < n; off++ {
		cand := q.paths[(start+off)%n]
		if cand == exclude {
			continue
		}
		if best == nil || pathBetter(cand, best, now) {
			best = cand
		}
	}
	if best == nil {
		return exclude
	}
	return best
}

func pathBetter(cand, cur *uplinkPath, now time.Time) bool {
	candBad := cand.badUntil.After(now)
	curBad := cur.badUntil.After(now)
	if candBad != curBad {
		return !candBad
	}
	if candBad && !cand.badUntil.Equal(cur.badUntil) {
		return cand.badUntil.Before(cur.badUntil)
	}
	cl, bl := len(cand.packets), len(cur.packets)
	if cl != bl {
		return cl < bl
	}
	candLoad := cand.recentBytesAt(now)
	curLoad := cur.recentBytesAt(now)
	if (candLoad == 0) != (curLoad == 0) {
		return candLoad == 0
	}
	if candBucket, curBucket := candLoad/8192, curLoad/8192; candBucket != curBucket {
		return candBucket < curBucket
	}
	candRTT, curRTT := cand.rtt(), cur.rtt()
	if candRTT > 0 && curRTT > 0 && candRTT+10*time.Millisecond < curRTT {
		return true
	}
	return false
}

// IPv4 fragments retain packet scheduling: noninitial fragments have no ports.
// Raw's own encrypted fragmentation happens later, within the selected worker.
func tcpUplinkKey(frame []byte) (key tcpFlowKey, ok bool) {
	key, _, _, ok = tcpUplinkPacketInfo(frame)
	return key, ok
}

func tcpUplinkPacketInfo(frame []byte) (key tcpFlowKey, seq uint32, payloadLen int, ok bool) {
	if len(frame) < 41 || frame[0] != frameData {
		return key, 0, 0, false
	}
	p := frame[1:]
	ihl := int(p[0]&15) * 4
	if p[0]>>4 != 4 || ihl < 20 || len(p) < ihl+20 || int(binary.BigEndian.Uint16(p[2:4])) != len(p) || p[9] != 6 || binary.BigEndian.Uint16(p[6:8])&0x3fff != 0 {
		return key, 0, 0, false
	}
	tcpLen := int(p[ihl+12]>>4) * 4
	if tcpLen < 20 || len(p) < ihl+tcpLen {
		return key, 0, 0, false
	}
	copy(key[:8], p[12:20])
	copy(key[8:], p[ihl:ihl+4])
	seq = binary.BigEndian.Uint32(p[ihl+4 : ihl+8])
	payloadLen = len(p) - ihl - tcpLen
	return key, seq, payloadLen, true
}
