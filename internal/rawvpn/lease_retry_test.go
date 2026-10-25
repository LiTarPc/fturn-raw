package rawvpn

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type limitLogger struct {
	nopTestLogger
	limits chan struct{}
}

func (l limitLogger) Warnf(format string, args ...any) {
	if strings.Contains(fmt.Sprintf(format, args...), "device limit") {
		select {
		case l.limits <- struct{}{}:
		default:
		}
	}
}
func TestClientRetriesDeviceLimitUntilSlotReleased(t *testing.T) {
	key := testKey(t)
	pc := udpSocket(t)
	_, _, _ = startRawTestServer(t, pc, key)
	cfg := ClientConfig{Address: netip.MustParseAddr("10.77.0.2"), MTU: 1280, Profile: "rtpopus2", Key: key}
	sockets := make([]net.PacketConn, 3)
	codecs := make([]*codec, 3)
	for i := range sockets {
		cfg.group[0] = byte(i + 1)
		sockets[i] = udpSocket(t)
		var err error
		codecs[i], _, err = clientHandshake(context.Background(), sockets[i], pc.LocalAddr(), cfg)
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	limits := make(chan struct{}, 1)
	var connected atomic.Int32
	cfg.Peer = pc.LocalAddr().(*net.UDPAddr)
	cfg.Streams = 1
	cfg.Provider = staticProvider{addr: startTURN(t, false)}
	cfg.Connected = &connected
	cfg.Log = limitLogger{limits: limits}
	done := make(chan error, 1)
	go func() { done <- RunClient(ctx, newTestDevice(), cfg) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("client did not cancel")
		}
	}()
	select {
	case <-limits:
	case err := <-done:
		t.Fatalf("client exited on limit: %v", err)
	case <-time.After(8 * time.Second):
		t.Fatal("limit not observed")
	}
	if err := writeFrame(sockets[1], pc.LocalAddr(), codecs[1], make([]byte, maxWire), []byte{frameClose}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for connected.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if connected.Load() != 1 {
		t.Fatal("client did not recover after slot release")
	}
}
func TestLostWorkersLeaseExpiresWithoutEvictingLiveDevice(t *testing.T) {
	ip := netip.MustParseAddr("10.77.0.2")
	now := time.Now()
	a := &serverSession{addr: &net.UDPAddr{Port: 1}, address: ip, active: true, seen: now}
	a.group[0] = 1
	b := &serverSession{addr: &net.UDPAddr{Port: 2}, address: ip.Next(), active: true, seen: now}
	b.group[0] = 2
	s := &rawServer{cfg: ServerConfig{ClientAddress: ip, MaxDevices: 2}, sessions: map[string]*serverSession{a.addr.String(): a, b.addr.String(): b}, order: []string{a.addr.String(), b.addr.String()}}
	s.remove(a.addr.String())
	newcomer := &serverSession{group: [16]byte{3}}
	if _, ok := s.assignAddress(newcomer); ok {
		t.Fatal("lost worker lease stolen before expiry")
	}
	b.seen = now.Add(deviceLeaseTTL)
	s.expire(now.Add(deviceLeaseTTL + time.Second))
	got, ok := s.assignAddress(newcomer)
	if !ok || got != ip {
		t.Fatalf("expired lease not released: %v %v", got, ok)
	}
	if s.sessions[b.addr.String()] != b {
		t.Fatal("live device evicted")
	}
}
