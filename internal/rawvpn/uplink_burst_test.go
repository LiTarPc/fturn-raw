package rawvpn

import (
	"encoding/binary"
	"testing"
	"time"
)

func waitUplinkBuffers(t *testing.T, q *uplinkQueue, want int) {
	t.Helper()
	until := time.Now().Add(time.Second)
	for {
		q.mu.Lock()
		buffered := len(q.shared)
		for _, p := range q.paths {
			buffered += len(p.packets)
		}
		q.mu.Unlock()
		if buffered+int(q.pathDrops.Load()) >= want {
			return
		}
		if time.Now().After(until) {
			t.Fatalf("dispatcher stalled: buffered=%d drops=%d", buffered, q.pathDrops.Load())
		}
		time.Sleep(time.Millisecond)
	}
}
func TestTCPFlowBurstUsesAggregateBudget(t *testing.T) {
	q := newUplinkQueue(UplinkTCPFlow)
	p := q.register(1)
	runUplinkTest(t, q)
	for i := uint32(0); i < 192; i++ {
		q.enqueue(tcpUplinkPacket(1000, i, 1350))
	}
	waitUplinkBuffers(t, q, 192)
	if q.pathDrops.Load() != 0 || q.drops.Load() != 0 || len(p.packets) != 192 {
		t.Fatalf("premature burst loss: kept=%d path_drop=%d enqueue_drop=%d", len(p.packets), q.pathDrops.Load(), q.drops.Load())
	}
	for i := uint32(0); i < 192; i++ {
		packet := receiveUplink(t, q, p.packets)
		if binary.BigEndian.Uint32(packet[25:29]) != i {
			t.Fatal("FIFO changed")
		}
	}
	if q.queued.Load() != 0 {
		t.Fatal("budget not returned")
	}
}
func TestTCPFlowBusyPathLeavesBudgetForOtherFlow(t *testing.T) {
	q := newUplinkQueue(UplinkTCPFlow)
	a := q.register(1)
	b := q.register(2)
	runUplinkTest(t, q)
	for i := uint32(0); i < 192; i++ {
		q.enqueue(tcpUplinkPacket(1000, i, 64))
	}
	for i := uint32(0); i < 32; i++ {
		q.enqueue(tcpUplinkPacket(1001, i, 64))
	}
	waitUplinkBuffers(t, q, 224)
	if len(a.packets) != 192 || len(b.packets) != 32 || q.pathDrops.Load() != 0 {
		t.Fatalf("cross-flow budget: first=%d second=%d lost=%d", len(a.packets), len(b.packets), q.pathDrops.Load())
	}
	for range 32 {
		receiveUplink(t, q, b.packets)
	}
	q.unregister(a)
	q.unregister(a)
	if q.queued.Load() != 0 || q.pathDrops.Load() != 192 {
		t.Fatal("unregister leaked or double-freed aggregate budget")
	}
	replacement := q.register(1)
	q.unregister(b)
	q.enqueue(tcpUplinkPacket(1000, 193, 64))
	receiveUplink(t, q, replacement.packets)
	if q.queued.Load() != 0 || q.failovers.Load() != 1 {
		t.Fatal("reconnection revived old binding or lost capacity")
	}
}
