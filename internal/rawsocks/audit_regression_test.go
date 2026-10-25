package rawsocks

import (
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"io"
	"net"
	"testing"
	"time"
)

func TestAuditRemoteHalfCloseMustKeepUploadOpen(t *testing.T) {
	d, peer := bridge(t)
	l, e := gonet.ListenTCP(peer, tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 9}), Port: 443}, ipv4.ProtocolNumber)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	received := make(chan string, 1)
	go func() {
		c, e := l.Accept()
		if e != nil {
			received <- "accept failed"
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(3 * time.Second))
		c.Write([]byte("ready"))
		c.(*gonet.TCPConn).CloseWrite()
		b, _ := io.ReadAll(c)
		received <- string(b)
	}()
	c := auth(t, d)
	c.Write([]byte{5, 1, 0, 1, 203, 0, 113, 9, 1, 187})
	h, e := readN(c, 10)
	if e != nil || h[1] != 0 {
		t.Fatalf("CONNECT %v %v", h, e)
	}
	b, e := io.ReadAll(c)
	if e != nil || string(b) != "ready" {
		t.Fatalf("server greeting %q %v", b, e)
	}
	select {
	case got := <-received:
		t.Fatalf("remote half-close prematurely closed upload before client finished: received=%q", got)
	case <-time.After(100 * time.Millisecond):
	}
	_, writeErr := c.Write([]byte("upload-after-fin"))
	c.(*net.TCPConn).CloseWrite()
	select {
	case got := <-received:
		if got != "upload-after-fin" {
			t.Fatalf("server half-close cut off client upload: got=%q writeErr=%v", got, writeErr)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("upload did not terminate")
	}
}
