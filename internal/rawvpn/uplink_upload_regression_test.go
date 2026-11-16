package rawvpn

import (
	"testing"
	"time"
)

// A TCP sender pauses while waiting for ACKs, not just when a path is drained.
// A second, faster TURN path must not overtake the queued tail of that upload.
func TestUplinkUploadPauseDoesNotBypassQueuedPackets(t *testing.T) {
	q := newUplinkQueue(UplinkTCPFlow)
	slow := q.register(1)
	key, _ := tcpUplinkKey(tcpUplinkPacket(5000, 1, 1200))
	base := time.Unix(100, 0)
	for i := uint32(0); i < 16; i++ {
		path := q.choosePacketLocked(key, 1160, base.Add(time.Duration(i)*time.Millisecond))
		path.packets <- uplinkPacket{}
	}
	fast := q.register(2)
	next := q.choosePacketLocked(key, 1160, base.Add(200*time.Millisecond))
	if next != slow {
		t.Fatalf("upload reordered: next segment used path %d while %d older segments remain on path %d (new path %d)", next.id, len(slow.packets), slow.id, fast.id)
	}
}

// An empty local queue does not mean the last packet reached the remote server.
func TestUplinkUploadPauseDoesNotOvertakeInFlightPackets(t *testing.T) {
	q := newUplinkQueue(UplinkTCPFlow)
	slow := q.register(1)
	slow.recordRTT(800 * time.Millisecond)
	key, _ := tcpUplinkKey(tcpUplinkPacket(5001, 1, 1200))
	base := time.Unix(100, 0)
	for i := uint32(0); i < 16; i++ {
		q.choosePacketLocked(key, 1160, base.Add(time.Duration(i)*time.Millisecond))
	}
	fast := q.register(2)
	fast.recordRTT(40 * time.Millisecond)
	next := q.choosePacketLocked(key, 1160, base.Add(200*time.Millisecond))
	if next != slow {
		t.Fatalf("upload migrated to path %d before earlier packets on path %d could arrive (RTT %s)", next.id, slow.id, slow.rtt())
	}
}

// A retransmission is normal TCP loss recovery, not proof the TURN path failed.
func TestUplinkUploadRepeatedLossRecoveryDoesNotRotatePaths(t *testing.T) {
	q := newUplinkQueue(UplinkTCPFlow)
	original := q.register(1)
	q.register(2)
	q.register(3)
	key, _ := tcpUplinkKey(tcpUplinkPacket(5002, 100, 1200))
	base := time.Unix(100, 0)
	q.choosePacketLocked(key, 1160, base)
	for i := 1; i <= 8; i++ {
		next := q.choosePacketLocked(key, 1160, base.Add(time.Duration(i)*300*time.Millisecond))
		if next != original {
			t.Fatalf("retransmission %d moved live upload from path %d to %d", i, original.id, next.id)
		}
	}
}
