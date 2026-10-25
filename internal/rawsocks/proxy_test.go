package rawsocks

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
)

const testPassword = "private-test-password"

func bridge(t *testing.T) (*Device, *stack.Stack) {
	t.Helper()
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	d, e := Open(netip.MustParseAddr("10.77.0.2"), 1280, port, testPassword)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	link := channel.New(1024, 1280, "")
	peer := stack.New(stack.Options{NetworkProtocols: []stack.NetworkProtocolFactory{ipv4.NewProtocol}, TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol}})
	if e := peer.CreateNIC(1, link); e != nil {
		t.Fatal(e)
	}
	if e := peer.AddProtocolAddress(1, tcpip.ProtocolAddress{Protocol: ipv4.ProtocolNumber, AddressWithPrefix: tcpip.AddrFrom4([4]byte{203, 0, 113, 9}).WithPrefix()}, stack.AddressProperties{}); e != nil {
		t.Fatal(e)
	}
	peer.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}})
	go func() {
		b := make([]byte, 1501)
		for {
			n, e := d.Read(b)
			if e != nil {
				return
			}
			p := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(append([]byte(nil), b[:n]...))})
			link.InjectInbound(ipv4.ProtocolNumber, p)
			p.DecRef()
		}
	}()
	go func() {
		for {
			p := link.ReadContext(ctx)
			if p == nil {
				return
			}
			var b []byte
			for _, v := range p.AsSlices() {
				b = append(b, v...)
			}
			p.DecRef()
			d.Write(b)
		}
	}()
	t.Cleanup(func() { cancel(); d.Close(); link.Close(); peer.Close(); peer.Wait() })
	return d, peer
}
func auth(t *testing.T, d *Device) net.Conn {
	t.Helper()
	c, e := net.DialTimeout("tcp4", d.listener.Addr().String(), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(5 * time.Second))
	c.Write([]byte{5, 1, 2})
	h, e := readN(c, 2)
	if e != nil || h[1] != 2 {
		t.Fatalf("greeting: %v %v", h, e)
	}
	request := append([]byte{1, byte(len(User))}, []byte(User)...)
	request = append(request, byte(len(testPassword)))
	request = append(request, []byte(testPassword)...)
	c.Write(request)
	h, e = readN(c, 2)
	if e != nil || h[1] != 0 {
		t.Fatalf("auth: %v %v", h, e)
	}
	return c
}
func TestTCPTraversesRawPacketsAndPreservesHalfClose(t *testing.T) {
	d, peer := bridge(t)
	l, e := gonet.ListenTCP(peer, tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 9}), Port: 443}, ipv4.ProtocolNumber)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	go func() {
		c, e := l.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		b, _ := io.ReadAll(c)
		c.Write(append([]byte("echo:"), b...))
	}()
	c := auth(t, d)
	c.Write([]byte{5, 1, 0, 1, 203, 0, 113, 9, 1, 187})
	h, e := readN(c, 10)
	if e != nil || h[1] != 0 {
		t.Fatalf("CONNECT %v %v", h, e)
	}
	c.Write([]byte("raw-only"))
	c.(*net.TCPConn).CloseWrite()
	b, e := io.ReadAll(c)
	if e != nil || string(b) != "echo:raw-only" {
		t.Fatalf("TCP echo %q %v", b, e)
	}
}
func TestUDPAssociateTraversesRawPackets(t *testing.T) {
	d, peer := bridge(t)
	addr := tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 9}), Port: 443}
	echo, e := gonet.DialUDP(peer, &addr, nil, ipv4.ProtocolNumber)
	if e != nil {
		t.Fatal(e)
	}
	defer echo.Close()
	go func() {
		b := make([]byte, 2048)
		n, from, e := echo.ReadFrom(b)
		if e == nil {
			echo.WriteTo(append([]byte("udp:"), b[:n]...), from)
		}
	}()
	c := auth(t, d)
	c.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0})
	h, e := readN(c, 10)
	if e != nil || h[1] != 0 {
		t.Fatalf("ASSOCIATE %v %v", h, e)
	}
	u, e := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(binary.BigEndian.Uint16(h[8:]))})
	if e != nil {
		t.Fatal(e)
	}
	defer u.Close()
	u.SetDeadline(time.Now().Add(5 * time.Second))
	u.Write(append([]byte{0, 0, 0, 1, 203, 0, 113, 9, 1, 187}, []byte("raw-only")...))
	b := make([]byte, 2048)
	n, e := u.Read(b)
	if e != nil || n < 10 || string(b[10:n]) != "udp:raw-only" {
		t.Fatalf("UDP echo %v %v", b[:n], e)
	}
}
func TestRejectsUnauthenticatedClients(t *testing.T) {
	d, _ := bridge(t)
	c, e := net.Dial("tcp4", d.listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(time.Second))
	c.Write([]byte{5, 1, 0})
	h, e := readN(c, 2)
	if e != nil || h[1] != 255 {
		t.Fatalf("unauthenticated greeting accepted %v %v", h, e)
	}
}
func TestCloseUnblocksPacketReadAndStopsListener(t *testing.T) {
	d, _ := bridge(t)
	done := make(chan error, 1)
	go func() { _, e := d.Read(make([]byte, 1501)); done <- e }()
	d.Close()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("read succeeded after close")
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not interrupt Read")
	}
	if c, e := net.DialTimeout("tcp4", d.listener.Addr().String(), time.Second); e == nil {
		c.Close()
		t.Fatal("listener survived Close")
	}
}
