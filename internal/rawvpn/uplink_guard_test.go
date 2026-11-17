package rawvpn

import (
	"testing"
	"time"
)

func TestUplinkAckOnlyPacketKeepsFlowletOnCurrentPath(t *testing.T) {
	q := newUplinkQueue(UplinkTCPFlow)
	current := q.register(1)
	key, _ := tcpUplinkKey(tcpUplinkPacket(4300, 1, 1200))
	start := time.Unix(100, 0)
	for i := uint32(0); i < uplinkFlowletMinPackets; i++ {
		q.choosePacketLocked(key, 1+i*1160, 1160, true, start.Add(time.Duration(i)*time.Millisecond))
	}
	alternate := q.register(2)
	next := start.Add(250 * time.Millisecond)
	if got := q.choosePacketLocked(key, 20000, 0, true, next); got != current {
		t.Fatalf("ACK-only packet changed data path to %d", got.id)
	}
	if got := q.choosePacketLocked(key, 20000, 1160, true, next.Add(30*time.Millisecond)); got != alternate {
		t.Fatalf("idle data burst could not use the alternate path: path=%d", got.id)
	}
	if q.flowletMoves.Load() != 1 {
		t.Fatal("expected one proactive move after a data burst")
	}
}

func TestUplinkNewBurstDoesNotBypassQueuedTail(t *testing.T) {
	q := newUplinkQueue(UplinkTCPFlow)
	current := q.register(1)
	key, _ := tcpUplinkKey(tcpUplinkPacket(4301, 1, 1200))
	start := time.Unix(100, 0)
	q.choosePacketLocked(key, 1, 1160, true, start)
	for range 4 {
		current.packets <- uplinkPacket{}
	}
	q.register(2)
	if got := q.choosePacketLocked(key, 1161, 1160, true, start.Add(200*time.Millisecond)); got != current {
		t.Fatalf("new burst bypassed queued tail: path=%d", got.id)
	}
}
