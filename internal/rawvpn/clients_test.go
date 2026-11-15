package rawvpn

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

func TestValidateClientPool(t *testing.T) {
	for _, tc := range []struct {
		server, client string
		limit          int
		valid          bool
	}{
		{"10.77.0.1/24", "10.77.0.2", 0, true},
		{"10.77.0.1/24", "10.77.0.2", 3, true},
		{"10.77.0.1/30", "10.77.0.2", 1, true},
		{"10.77.0.1/30", "10.77.0.2", 3, false},
		{"10.77.0.1/24", "10.77.0.254", 3, false},
		{"10.77.0.1/24", "10.77.0.255", 1, false},
		{"10.77.0.1/24", "10.77.0.0", 1, false},
		{"10.77.0.3/24", "10.77.0.2", 3, false},
		{"10.77.0.1/24", "10.78.0.2", 3, false},
		{"10.77.0.1/24", "10.77.0.2", -1, false},
		{"10.77.0.1/24", "10.77.0.2", 65, false},
		{"10.77.0.1/24", "127.0.0.2", 3, false},
		{"::1/64", "10.77.0.2", 3, false},
	} {
		err := ValidateClientPool(netip.MustParsePrefix(tc.server), netip.MustParseAddr(tc.client), tc.limit)
		if (err == nil) != tc.valid {
			t.Errorf("%+v: %v", tc, err)
		}
	}
}

// Lose the first limit response to verify rejection is retryable, like Ready.
type dropLimitReply struct {
	net.PacketConn
	writes atomic.Int32
}

func (p *dropLimitReply) WriteTo(b []byte, addr net.Addr) (int, error) {
	if p.writes.Add(1) == 8 {
		return len(b), nil
	}
	return p.PacketConn.WriteTo(b, addr)
}

func TestDeviceLimitAndSlotReuse(t *testing.T) {
	key := testKey(t)
	server := &dropLimitReply{PacketConn: udpSocket(t)}
	dev, _, _ := startRawTestServer(t, server, key)
	cfg := ClientConfig{Address: netip.MustParseAddr("10.77.0.2"), MTU: 1280, Profile: "rtpopus2", Key: key}
	sockets := make([]net.PacketConn, 3)
	codecs := make([]*codec, 3)
	for i := range 3 {
		cfg.group[0] = byte(i + 1)
		sockets[i] = udpSocket(t)
		var err error
		codecs[i], _, err = clientHandshake(context.Background(), sockets[i], server.LocalAddr(), cfg)
		if err != nil {
			t.Fatal(err)
		}
	}
	cfg.group[0] = 4
	extra := udpSocket(t)
	if _, _, err := clientHandshake(context.Background(), extra, server.LocalAddr(), cfg); !errors.Is(err, ErrDeviceLimit) {
		t.Fatalf("fourth device: %v", err)
	}
	// Existing devices continue after rejection; verify independent reply routing.
	for i := range 3 {
		packet := checkedPacket(17, cfg.Address.String(), "1.1.1.1")
		packet[84] = byte(i)
		fillChecksums(packet)
		if err := writeFrame(sockets[i], server.LocalAddr(), codecs[i], make([]byte, maxWire), append([]byte{frameData}, packet...)); err != nil {
			t.Fatal(err)
		}
		assigned := cfg.Address
		for range i {
			assigned = assigned.Next()
		}
		want := append([]byte(nil), packet...)
		a := assigned.As4()
		copy(want[12:16], a[:])
		fillChecksums(want)
		readPacket(t, dev, want)
		reply := checkedPacket(17, "1.1.1.1", assigned.String())
		dev.input <- reply
		_ = sockets[i].SetReadDeadline(time.Now().Add(2 * time.Second))
		b := make([]byte, maxWire)
		n, _, err := sockets[i].ReadFrom(b)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := codecs[i].decode(make([]byte, maxFrame), b[:n])
		if err != nil {
			t.Fatal(err)
		}
		expected := checkedPacket(17, "1.1.1.1", cfg.Address.String())
		if len(plain) < 1 || plain[0] != frameData || !bytes.Equal(plain[1:], expected) {
			t.Fatal("reply routed/translated incorrectly")
		}
	}
	if err := writeFrame(sockets[1], server.LocalAddr(), codecs[1], make([]byte, maxWire), []byte{frameClose}); err != nil {
		t.Fatal(err)
	}
	// A ping/pong confirms the server is servicing the queued control frames.
	if err := writeFrame(sockets[0], server.LocalAddr(), codecs[0], make([]byte, maxWire), []byte{framePing}); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, maxWire)
	if _, _, err := sockets[0].ReadFrom(b); err != nil {
		t.Fatal(err)
	}
	replacement, _, err := clientHandshake(context.Background(), extra, server.LocalAddr(), cfg)
	if err != nil {
		t.Fatalf("slot not freed after close: %v", err)
	}
	p := checkedPacket(6, cfg.Address.String(), "1.1.1.1")
	if err := writeFrame(extra, server.LocalAddr(), replacement, make([]byte, maxWire), append([]byte{frameData}, p...)); err != nil {
		t.Fatal(err)
	}
	a := cfg.Address.Next().As4()
	copy(p[12:16], a[:])
	fillChecksums(p)
	readPacket(t, dev, p)
}

func TestExpiredDeviceSlotAndUnconfirmedHello(t *testing.T) {
	cfg := ServerConfig{ClientAddress: netip.MustParseAddr("10.77.0.2"), MaxDevices: 1}
	w := &serverSession{addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 20001}, active: true, seen: time.Now().Add(-91 * time.Second), address: cfg.ClientAddress}
	w.group[0] = 1
	pending := &serverSession{addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 20002}, seen: time.Now()}
	pending.group[0] = 2
	s := &rawServer{cfg: cfg, sessions: map[string]*serverSession{w.addr.String(): w, pending.addr.String(): pending}, order: []string{w.addr.String(), pending.addr.String()}}
	if _, ok := s.assignAddress(pending); ok {
		t.Fatal("occupied slot assigned twice")
	}
	s.expire(time.Now())
	if ip, ok := s.assignAddress(pending); !ok || ip != cfg.ClientAddress {
		t.Fatal("expired slot was not freed")
	}
	newcomer := &serverSession{}
	newcomer.group[0] = 3
	if _, ok := s.assignAddress(newcomer); !ok {
		t.Fatal("unconfirmed hello reserved a device slot")
	}
}

func TestThreeDevicesShareProfileThroughTURN(t *testing.T) {
	for _, udp := range []bool{false, true} {
		name := "tcp"
		if udp {
			name = "udp"
		}
		t.Run(name, func(t *testing.T) {
			turnAddr := startTURN(t, udp)
			key := testKey(t)
			server := udpSocket(t)
			dev, _, _ := startRawTestServer(t, server, key)
			clients := make([]*testDevice, 3)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			// Start each device in turn to give the address allocation a known order.
			for i := range clients {
				clients[i] = newTestDevice()
				var connected atomic.Int32
				done := make(chan error, 1)
				go func(i int) {
					done <- RunClient(ctx, clients[i], ClientConfig{Peer: server.LocalAddr().(*net.UDPAddr), Address: netip.MustParseAddr("10.77.0.2"), MTU: 1280, Profile: "rtpopus3", Key: key, TransportUDP: udp, Streams: 2, Provider: staticProvider{addr: turnAddr}, Connected: &connected})
				}(i)
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
				for connected.Load() != 2 && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				if connected.Load() != 2 {
					t.Fatalf("device %d workers=%d", i, connected.Load())
				}
			}
			for i, client := range clients {
				assigned := netip.MustParseAddr("10.77.0.2")
				for range i {
					assigned = assigned.Next()
				}
				// Same source/destination ports and ICMP IDs across devices.
				for _, proto := range []byte{6, 17, 1} {
					up := checkedPacket(proto, "10.77.0.2", "1.1.1.1")
					client.input <- append([]byte(nil), up...)
					a := assigned.As4()
					copy(up[12:16], a[:])
					fillChecksums(up)
					readPacket(t, dev, up)
					for range 32 {
						dev.input <- checkedPacket(proto, "1.1.1.1", assigned.String())
					}
					for range 32 {
						readPacket(t, client, checkedPacket(proto, "1.1.1.1", "10.77.0.2"))
					}
				}
			}
		})
	}
}

func TestInterleavedDevicesRotateTheirOwnWorkers(t *testing.T) {
	first := netip.MustParseAddr("10.77.0.2")
	s := &rawServer{cfg: ServerConfig{ClientAddress: first}, sessions: map[string]*serverSession{}}
	now := time.Now()
	for i := range 6 {
		address := first
		for range i / 2 {
			address = address.Next()
		}
		w := &serverSession{addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 21000 + i}, address: address, active: true, seen: now}
		s.sessions[w.addr.String()] = w
		s.order = append(s.order, w.addr.String())
	}
	counts := map[*serverSession]int{}
	for range 32 {
		ip := first
		for range 3 {
			w := s.pick(now, ip)
			if w == nil || w.address != ip {
				t.Fatal("picked another device's worker")
			}
			counts[w]++
			ip = ip.Next()
		}
	}
	if len(counts) != 6 {
		t.Fatalf("only %d/6 workers used", len(counts))
	}
	for _, count := range counts {
		if count != 16 {
			t.Fatalf("unbalanced worker rotation: %d", count)
		}
	}
	for i := range 200 {
		s.pick(now, netip.AddrFrom4([4]byte{192, 0, 2, byte(i)}))
	}
	if len(s.cursors) != 3 {
		t.Fatal("unknown destinations allocated scheduler state")
	}
}
