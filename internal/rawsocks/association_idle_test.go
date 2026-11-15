package rawsocks

import (
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
)

func shortAssociation(t *testing.T, d *Device, idle time.Duration) (net.Conn, *net.UDPConn, <-chan string) {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	control, err := net.Dial("tcp4", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server, err := l.Accept()
	if err != nil {
		control.Close()
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() { done <- d.associateWithTimeouts(server, idle/2, idle) }()
	control.SetReadDeadline(time.Now().Add(3 * time.Second))
	h, err := readN(control, 10)
	if err != nil || h[1] != 0 {
		t.Fatalf("reply %v %v", h, err)
	}
	u, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(binary.BigEndian.Uint16(h[8:]))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { control.Close(); server.Close(); u.Close() })
	return control, u, done
}
func expectAssociationEnd(t *testing.T, control net.Conn, done <-chan string, reason string) {
	t.Helper()
	select {
	case got := <-done:
		if got != reason {
			t.Fatalf("reason=%s want=%s", got, reason)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("association did not finish")
	}
	control.SetReadDeadline(time.Now().Add(time.Second))
	_, err := control.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("control remained open")
	}
	if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("control was not closed")
	}
}
func TestAssociationIdleClosesControlWithoutDatagrams(t *testing.T) {
	d, _ := bridge(t)
	c, _, done := shortAssociation(t, d, 80*time.Millisecond)
	expectAssociationEnd(t, c, done, "udp_idle")
}
func TestAssociationInvalidDatagramsDoNotExtendIdle(t *testing.T) {
	d, _ := bridge(t)
	c, u, done := shortAssociation(t, d, 100*time.Millisecond)
	for range 15 {
		u.Write([]byte{0, 0, 1, 1})
		time.Sleep(15 * time.Millisecond)
	}
	expectAssociationEnd(t, c, done, "udp_idle")
}
func TestAssociationOneWayTrafficSurvivesAndThenExpires(t *testing.T) {
	for _, direction := range []string{"upload", "download"} {
		t.Run(direction, func(t *testing.T) {
			d, peer := bridge(t)
			addr := tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 9}), Port: 443}
			remote, err := gonet.DialUDP(peer, &addr, nil, ipv4.ProtocolNumber)
			if err != nil {
				t.Fatal(err)
			}
			defer remote.Close()
			c, u, done := shortAssociation(t, d, 300*time.Millisecond)
			packet := []byte{0, 0, 0, 1, 203, 0, 113, 9, 1, 187, 42}
			u.Write(packet)
			remote.SetReadDeadline(time.Now().Add(time.Second))
			b := make([]byte, 64)
			_, from, err := remote.ReadFrom(b)
			if err != nil {
				t.Fatal(err)
			}
			for range 24 {
				time.Sleep(40 * time.Millisecond)
				if direction == "upload" {
					if _, err = u.Write(packet); err != nil {
						t.Fatal(err)
					}
					remote.SetReadDeadline(time.Now().Add(time.Second))
					if _, _, err = remote.ReadFrom(b); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err = remote.WriteTo([]byte{42}, from); err != nil {
						t.Fatal(err)
					}
					u.SetReadDeadline(time.Now().Add(time.Second))
					if _, err = u.Read(b); err != nil {
						t.Fatal(err)
					}
				}
				select {
				case reason := <-done:
					t.Fatalf("active %s closed: %s", direction, reason)
				default:
				}
			}
			expectAssociationEnd(t, c, done, "udp_idle")
		})
	}
}
func TestAssociationControlAndDeviceCloseInterruptIdle(t *testing.T) {
	for _, mode := range []string{"control", "device"} {
		t.Run(mode, func(t *testing.T) {
			d, _ := bridge(t)
			c, _, done := shortAssociation(t, d, time.Minute)
			if mode == "control" {
				c.Close()
			} else {
				d.Close()
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("close did not interrupt association")
			}
		})
	}
}
func waitBridgeStats(t *testing.T, d *Device, condition func(Stats) bool) Stats {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		s := d.Stats()
		if condition(s) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("stats condition failed: %+v", s)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func TestAssociationSaturatedBridgeRecoversWithoutRestart(t *testing.T) {
	// Exercise the actual accept/auth/dispatch/release path at the production
	// Android limit, including clients that leave TCP control open forever.
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	d, err := openWithAssociationIdle(netip.MustParseAddr("10.77.0.2"), 1280, port, testPassword, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	clients := make([]net.Conn, 0, cap(d.slots))
	defer func() {
		for _, c := range clients {
			c.Close()
		}
	}()
	started := time.Now()
	for range cap(d.slots) {
		c := auth(t, d)
		c.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0})
		h, err := readN(c, 10)
		if err != nil || h[1] != 0 {
			t.Fatalf("associate: %v %v", h, err)
		}
		clients = append(clients, c)
	}
	s := d.Stats()
	if s.Active != cap(d.slots) || s.UDP != cap(d.slots) {
		t.Fatalf("not saturated: %+v (setup %s)", s, time.Since(started))
	}
	extra, err := net.DialTimeout("tcp4", d.listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	extra.SetDeadline(time.Now().Add(time.Second))
	extra.Write([]byte{5, 1, 2})
	_, err = extra.Read(make([]byte, 2))
	extra.Close()
	if err == nil {
		t.Fatal("saturation accepted handshake")
	}
	s = waitBridgeStats(t, d, func(s Stats) bool { return s.Active == 0 && s.Closed["udp_idle"] == uint64(cap(d.slots)) })
	if s.Rejected != 1 || s.Peak != cap(d.slots) {
		t.Fatalf("counters: %+v", s)
	}
	for _, c := range clients {
		c.SetReadDeadline(time.Now().Add(time.Second))
		if _, err = c.Read(make([]byte, 1)); err != io.EOF {
			t.Fatalf("idle control not EOF: %v", err)
		}
	}
	// Reopen a real association using the same bridge, no VPN/process restart.
	c := auth(t, d)
	c.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0})
	h, err := readN(c, 10)
	if err != nil || h[1] != 0 {
		t.Fatalf("reopen: %v %v", h, err)
	}
	c.Close()
	waitBridgeStats(t, d, func(s Stats) bool { return s.Active == 0 && s.Closed["udp_control_closed"] == 1 })
	t.Logf("%d idle associations reclaimed; rejection counted; association reopened without restart", cap(d.slots))
}
