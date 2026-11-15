package rawsocks

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func tcpPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	l, e := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	c, e := net.DialTCP("tcp4", nil, l.Addr().(*net.TCPAddr))
	if e != nil {
		t.Fatal(e)
	}
	p, e := l.AcceptTCP()
	if e != nil {
		c.Close()
		t.Fatal(e)
	}
	c.SetDeadline(time.Now().Add(3 * time.Second))
	p.SetDeadline(time.Now().Add(3 * time.Second))
	t.Cleanup(func() { c.Close(); p.Close() })
	return c, p
}
func waitRelay(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("relay retained a slot")
	}
}
func TestTCPRelayIdleAndAbandonedHalfClose(t *testing.T) {
	for _, half := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "half-close"}[half], func(t *testing.T) {
			app, c := tcpPair(t)
			u, remote := tcpPair(t)
			done := make(chan struct{})
			go func() { relayTCP(context.Background(), c, u, 80*time.Millisecond, 40*time.Millisecond); close(done) }()
			if half {
				app.CloseWrite()
				if n, e := remote.Read(make([]byte, 1)); n != 0 || e != io.EOF {
					t.Fatalf("upload EOF: %d %v", n, e)
				}
			}
			waitRelay(t, done)
		})
	}
}
func TestTCPRelayPreservesActiveResponseAfterHalfClose(t *testing.T) {
	app, c := tcpPair(t)
	u, remote := tcpPair(t)
	done := make(chan struct{})
	go func() { relayTCP(context.Background(), c, u, time.Second, 150*time.Millisecond); close(done) }()
	app.CloseWrite()
	if _, e := remote.Read(make([]byte, 1)); e != io.EOF {
		t.Fatal(e)
	}
	for i := range 10 {
		if _, e := remote.Write([]byte{byte(i)}); e != nil {
			t.Fatal(e)
		}
		b := make([]byte, 1)
		if _, e := io.ReadFull(app, b); e != nil || b[0] != byte(i) {
			t.Fatalf("response %d: %v", i, e)
		}
		time.Sleep(30 * time.Millisecond)
	}
	remote.CloseWrite()
	waitRelay(t, done)
}
func TestTCPRelayCancellationClosesBothDirections(t *testing.T) {
	_, c := tcpPair(t)
	u, _ := tcpPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { relayTCP(ctx, c, u, time.Minute, time.Minute); close(done) }()
	cancel()
	waitRelay(t, done)
}
func TestBridgeAdmits1024ClientsAndReleasesSlots(t *testing.T) {
	d, _ := bridge(t)
	clients := make([]net.Conn, 0, maxBridgeConnections)
	defer func() {
		for _, c := range clients {
			c.Close()
		}
	}()
	for range 1024 {
		c := auth(t, d)
		c.SetDeadline(time.Time{})
		clients = append(clients, c)
	}
	if len(d.slots) != 1024 {
		t.Fatalf("admitted %d", len(d.slots))
	}
	extra, e := net.DialTimeout("tcp4", d.listener.Addr().String(), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	extra.SetDeadline(time.Now().Add(time.Second))
	extra.Write([]byte{5, 1, 2})
	b := make([]byte, 2)
	n, e := extra.Read(b)
	extra.Close()
	if e == nil || n != 0 {
		t.Fatal("bridge exceeded its bounded admission budget")
	}
	for _, c := range clients {
		c.Close()
	}
	clients = nil
	deadline := time.Now().Add(time.Second)
	for len(d.slots) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(d.slots) != 0 {
		t.Fatalf("retained %d slots", len(d.slots))
	}
	auth(t, d)
}
