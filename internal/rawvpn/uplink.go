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
	UplinkPacket       = "packet"
	UplinkTCPFlow      = "tcp-flow"
	uplinkCapacity     = 256
	uplinkPathCapacity = 64
	uplinkFlowLimit    = 8192
	uplinkFlowTTL      = 2 * time.Minute
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
	id         int
	generation uint64
	packets    chan uplinkPacket
}
type tcpFlowKey [12]byte
type uplinkBinding struct {
	id         int
	generation uint64
	seen       time.Time
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
			key, ok := tcpUplinkKey(packet.plain)
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
					p := q.chooseLocked(key, time.Now())
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
	binding, exists := q.flows[key]
	if exists && now.Sub(binding.seen) < uplinkFlowTTL {
		for _, p := range q.paths {
			if p.id == binding.id && p.generation == binding.generation {
				binding.seen = now
				q.flows[key] = binding
				return p
			}
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
	p := q.paths[q.next%uint64(len(q.paths))]
	q.next++
	q.flows[key] = uplinkBinding{id: p.id, generation: p.generation, seen: now}
	return p
}

// IPv4 fragments retain packet scheduling: noninitial fragments have no ports.
// Raw's own encrypted fragmentation happens later, within the selected worker.
func tcpUplinkKey(frame []byte) (key tcpFlowKey, ok bool) {
	if len(frame) < 41 || frame[0] != frameData {
		return key, false
	}
	p := frame[1:]
	ihl := int(p[0]&15) * 4
	if p[0]>>4 != 4 || ihl < 20 || len(p) < ihl+20 || int(binary.BigEndian.Uint16(p[2:4])) != len(p) || p[9] != 6 || binary.BigEndian.Uint16(p[6:8])&0x3fff != 0 {
		return key, false
	}
	tcpLen := int(p[ihl+12]>>4) * 4
	if tcpLen < 20 || len(p) < ihl+tcpLen {
		return key, false
	}
	copy(key[:8], p[12:20])
	copy(key[8:], p[ihl:ihl+4])
	return key, true
}
