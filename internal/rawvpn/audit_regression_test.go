package rawvpn

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

type auditBlockedPC struct {
	net.PacketConn
	entered chan struct{}
	release chan struct{}
}

func (p *auditBlockedPC) WriteTo(b []byte, a net.Addr) (int, error) {
	close(p.entered)
	<-p.release
	return 0, errors.New("injected old-session send failure")
}
func TestAuditOldSendFailureMustNotDeleteReplacement(t *testing.T) {
	ip := netip.MustParseAddr("10.77.0.2")
	base := udpSocket(t)
	pc := &auditBlockedPC{PacketConn: base, entered: make(chan struct{}), release: make(chan struct{})}
	dev := newTestDevice()
	c, _ := newCodec("rtpopus2", testKey(t), true, nil)
	addr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 24001}
	old := &serverSession{addr: addr, address: ip, codec: c, active: true, seen: time.Now()}
	s := &rawServer{cfg: ServerConfig{ClientAddress: ip, MTU: 1280, Log: nopTestLogger{}}, pc: pc, dev: dev, sessions: map[string]*serverSession{addr.String(): old}, order: []string{addr.String()}}
	done := make(chan error, 1)
	go func() { done <- s.downlink(context.Background()) }()
	dev.input <- ipPacket("1.1.1.1", ip.String(), 64)
	<-pc.entered
	replacement := &serverSession{addr: addr, address: ip, codec: c, active: false, seen: time.Now()}
	s.mu.Lock()
	s.sessions[addr.String()] = replacement
	s.mu.Unlock()
	close(pc.release)
	deadline := time.Now().Add(time.Second)
	removed := false
	for time.Now().Before(deadline) {
		s.mu.Lock()
		removed = s.sessions[addr.String()] != replacement
		s.mu.Unlock()
		if removed {
			break
		}
		time.Sleep(time.Millisecond)
	}
	dev.Close()
	<-done
	if removed {
		t.Fatal("old downlink write failure deleted the new session at the same address")
	}
}
func TestAuditReconnectMustKeepDeviceAddress(t *testing.T) {
	ip := netip.MustParseAddr("10.77.0.2")
	a := &serverSession{addr: &net.UDPAddr{Port: 24011}, address: ip, active: true}
	b := &serverSession{addr: &net.UDPAddr{Port: 24012}, address: ip.Next(), active: true}
	a.group[0] = 1
	b.group[0] = 2
	s := &rawServer{cfg: ServerConfig{ClientAddress: ip}, sessions: map[string]*serverSession{a.addr.String(): a, b.addr.String(): b}, order: []string{a.addr.String(), b.addr.String()}}
	s.remove(a.addr.String())
	s.remove(b.addr.String())
	next := &serverSession{group: b.group}
	got, ok := s.assignAddress(next)
	if !ok || got != b.address {
		t.Fatalf("same device group moved after full worker loss: before=%v after=%v ok=%v", b.address, got, ok)
	}
}
