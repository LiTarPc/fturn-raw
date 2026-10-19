package rawvpn

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

func tcpUplinkPacket(port uint16, sequence uint32, size int) []byte {
	p := ipPacket("10.77.0.2", "192.0.2.1", size)
	p[9] = 6
	binary.BigEndian.PutUint16(p[20:22], port)
	binary.BigEndian.PutUint16(p[22:24], 443)
	binary.BigEndian.PutUint32(p[24:28], sequence)
	p[32] = 5 << 4
	return append([]byte{frameData}, p...)
}
func runUplinkTest(t *testing.T, q *uplinkQueue) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); q.run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("dispatcher did not stop")
		}
	})
}
func receiveUplink(t *testing.T, q *uplinkQueue, ch <-chan uplinkPacket) []byte {
	t.Helper()
	select {
	case p := <-ch:
		return q.take(p)
	case <-time.After(time.Second):
		t.Fatal("uplink stalled")
		return nil
	}
}
func uplinkBarrier(t *testing.T, q *uplinkQueue) {
	t.Helper()
	q.enqueue(append([]byte{frameData}, ipPacket("10.77.0.2", "192.0.2.1", 64)...))
	receiveUplink(t, q, q.shared)
}
func TestUplinkStickyFlowJoinFailoverAndOrder(t *testing.T) {
	q := newUplinkQueue(UplinkTCPFlow)
	a := q.register(1)
	runUplinkTest(t, q)
	q.enqueue(tcpUplinkPacket(1000, 0, 64))
	receiveUplink(t, q, a.packets)
	b := q.register(2)
	for i := uint32(1); i <= 32; i++ {
		q.enqueue(tcpUplinkPacket(1000, i, 64))
	}
	uplinkBarrier(t, q)
	if len(b.packets) != 0 {
		t.Fatal("ready path moved existing TCP flow")
	}
	// A second connection can use the other path even while the first is stalled.
	q.enqueue(tcpUplinkPacket(1001, 99, 64))
	p := receiveUplink(t, q, b.packets)
	if binary.BigEndian.Uint32(p[25:29]) != 99 {
		t.Fatal("wrong second flow")
	}
	for i := uint32(1); i <= 32; i++ {
		p := receiveUplink(t, q, a.packets)
		if binary.BigEndian.Uint32(p[25:29]) != i {
			t.Fatal("same-flow FIFO broken")
		}
	}
	q.unregister(a)
	q.enqueue(tcpUplinkPacket(1000, 33, 64))
	receiveUplink(t, q, b.packets)
	if q.failovers.Load() != 1 {
		t.Fatal("missing failover metric")
	}
	// Reusing a logical worker ID must not revive the old queue or flow binding.
	q.unregister(b)
	replacement := q.register(2)
	q.enqueue(tcpUplinkPacket(1000, 34, 64))
	receiveUplink(t, q, replacement.packets)
	if q.queued.Load() != 0 || q.failovers.Load() != 2 {
		t.Fatal("reconnect leaked queue budget or binding")
	}
}
func TestUplinkBoundedQueuesDoNotSprayBusyFlows(t *testing.T) {
	q := newUplinkQueue(UplinkTCPFlow)
	for i := 0; i < uplinkCapacity+1; i++ {
		q.enqueue(tcpUplinkPacket(1000, uint32(i), 64))
	}
	if q.queued.Load() != uplinkCapacity || q.drops.Load() != 1 {
		t.Fatal("aggregate budget grew")
	}
	a := q.register(1)
	b := q.register(2)
	runUplinkTest(t, q)
	// Wait until all central entries were dispatched using a UDP barrier after
	// capacity becomes available from drops at the pinned queue.
	deadline := time.Now().Add(time.Second)
	for q.pathDrops.Load() != uplinkCapacity-uplinkPathCapacity && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if q.queued.Load() != uplinkPathCapacity || len(b.packets) != 0 {
		t.Fatal("busy flow migrated or queue budget incorrect")
	}
	q.unregister(a)
	q.unregister(a)
	if q.queued.Load() != 0 || q.pathDrops.Load() != uplinkCapacity {
		t.Fatal("failed path leaked packets")
	}
}
func TestUplinkParserAndLegacyScheduling(t *testing.T) {
	good := tcpUplinkPacket(1000, 1, 64)
	if _, ok := tcpUplinkKey(good); !ok {
		t.Fatal("TCP not classified")
	}
	for _, change := range []func([]byte) []byte{
		func(p []byte) []byte { return p[:30] },
		func(p []byte) []byte { p[10] = 17; return p },
		func(p []byte) []byte { p[7] = 0x20; return p },
		func(p []byte) []byte { p[8] = 1; return p },
		func(p []byte) []byte { p[33] = 0x10; return p },
		func(p []byte) []byte { p[1] = 0x4f; return p },
	} {
		bad := change(append([]byte(nil), good...))
		if _, ok := tcpUplinkKey(bad); ok {
			t.Fatal("non TCP/fragment/malformed packet classified")
		}
	}
	q := newUplinkQueue("")
	q.enqueue(good)
	receiveUplink(t, q, q.shared)
	if q.register(1) != nil || q.mode != UplinkPacket || q.queued.Load() != 0 {
		t.Fatal("baseline mode changed")
	}
	flow := newUplinkQueue(UplinkTCPFlow)
	runUplinkTest(t, flow)
	fragment := append([]byte(nil), good...)
	fragment[7] = 0x20
	flow.enqueue(fragment)
	receiveUplink(t, flow, flow.shared)
}
func TestUplinkFlowTableBoundAndIdleExpiry(t *testing.T) {
	q := newUplinkQueue(UplinkTCPFlow)
	a := q.register(1)
	now := time.Now()
	key, _ := tcpUplinkKey(tcpUplinkPacket(1000, 0, 64))
	q.chooseLocked(key, now)
	b := q.register(2)
	if q.chooseLocked(key, now.Add(time.Second)) != a {
		t.Fatal("flow lost affinity")
	}
	if q.chooseLocked(key, now.Add(uplinkFlowTTL+time.Second)) != b {
		t.Fatal("idle flow not expired")
	}
	for i := 0; i < uplinkFlowLimit+100; i++ {
		k, _ := tcpUplinkKey(tcpUplinkPacket(uint16(i), 0, 64))
		q.chooseLocked(k, now)
	}
	if len(q.flows) > uplinkFlowLimit {
		t.Fatal("unbounded flow table")
	}
}
func TestUplinkWaitForReadyAndCancellation(t *testing.T) {
	q := newUplinkQueue(UplinkTCPFlow)
	q.enqueue(tcpUplinkPacket(1000, 0, 64))
	runUplinkTest(t, q)
	p := q.register(1)
	receiveUplink(t, q, p.packets)
	q.unregister(p)
	q.enqueue(tcpUplinkPacket(1000, 1, 64))
	// Cleanup must interrupt a dispatcher waiting for a ready worker.
}
func TestUplinkOptionValidation(t *testing.T) {
	for _, mode := range []string{UplinkPacket, UplinkTCPFlow} {
		o, rest, err := ParseClientOptions([]string{"-raw-uplink", mode, "-n", "3"})
		if err != nil || o.UplinkMode != mode || len(rest) != 2 {
			t.Fatal("uplink option not consumed", err)
		}
	}
	for _, args := range [][]string{{"-raw-uplink", "round-robin"}, {"-raw-uplink"}} {
		if _, _, err := ParseClientOptions(args); err == nil {
			t.Fatal("bad mode accepted")
		}
	}
}
func TestTCPFlowUplinkThroughRealTURN(t *testing.T) {
	for _, profile := range []string{"rtpopus2", "rtpopus3"} {
		for _, udp := range []bool{false, true} {
			name := profile + "/tcp"
			if udp {
				name = profile + "/udp"
			}
			t.Run(name, func(t *testing.T) {
				turnAddr := startTURN(t, udp)
				key := testKey(t)
				pc := udpSocket(t)
				serverDev, _, _ := startRawTestServer(t, pc, key, 1500)
				clientDev := newTestDevice()
				peer, err := net.ResolveUDPAddr("udp", pc.LocalAddr().String())
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				var connected atomic.Int32
				done := make(chan error, 1)
				go func() {
					done <- RunClient(ctx, clientDev, ClientConfig{Peer: peer, Address: netip.MustParseAddr("10.77.0.2"), MTU: 1500, Profile: profile, Key: key, TransportUDP: udp, Streams: 3, Provider: staticProvider{addr: turnAddr}, Connected: &connected, UplinkMode: UplinkTCPFlow})
				}()
				t.Cleanup(func() {
					cancel()
					select {
					case err := <-done:
						if err != nil {
							t.Error(err)
						}
					case <-time.After(5 * time.Second):
						t.Error("client did not stop")
					}
				})
				deadline := time.Now().Add(8 * time.Second)
				for connected.Load() != 3 && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				if connected.Load() != 3 {
					t.Fatal("workers not ready")
				}
				// Burst of MTU-size packets exercises Raw fragmentation and same-flow FIFO.
				for i := uint32(0); i < 32; i++ {
					clientDev.input <- tcpUplinkPacket(1000, i, 1500)[1:]
				}
				for i := uint32(0); i < 32; i++ {
					readPacket(t, serverDev, tcpUplinkPacket(1000, i, 1500)[1:])
				}
				for port := uint16(1001); port < 1005; port++ {
					up := tcpUplinkPacket(port, 1, 64)[1:]
					clientDev.input <- up
					readPacket(t, serverDev, up)
				}
				up := ipPacket("10.77.0.2", "1.1.1.1", 1280)
				clientDev.input <- up
				readPacket(t, serverDev, up)
				down := ipPacket("1.1.1.1", "10.77.0.2", 1500)
				serverDev.input <- down
				readPacket(t, clientDev, down)
			})
		}
	}
}
