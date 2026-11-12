package rawsocks

import (
	"encoding/binary"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"net"
	"testing"
	"time"
)

func TestUDPAssociateAcceptsUnspecifiedIPv6Hint(t *testing.T) {
	d, peer := bridge(t)
	addr := tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 9}), Port: 443}
	echo, e := gonet.DialUDP(peer, &addr, nil, ipv4.ProtocolNumber)
	if e != nil {
		t.Fatal(e)
	}
	defer echo.Close()
	go func() {
		b := make([]byte, 64)
		n, from, e := echo.ReadFrom(b)
		if e == nil {
			echo.WriteTo(b[:n], from)
		}
	}()
	c := auth(t, d)
	request := make([]byte, 22)
	copy(request, []byte{5, 3, 0, 4}) // :: port 0: unspecified client endpoint hint.
	c.Write(request)
	h, e := readN(c, 10)
	if e != nil || h[1] != 0 {
		t.Fatalf("UDP ASSOCIATE IPv6 hint: %v %v", h, e)
	}
	u, e := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(binary.BigEndian.Uint16(h[8:]))})
	if e != nil {
		t.Fatal(e)
	}
	defer u.Close()
	u.SetDeadline(time.Now().Add(time.Second))
	u.Write([]byte{0, 0, 0, 1, 203, 0, 113, 9, 1, 187, 42})
	b := make([]byte, 64)
	n, e := u.Read(b)
	if e != nil || n != 11 || b[10] != 42 {
		t.Fatalf("IPv4 data after hint: %d %v", n, e)
	}
	c.Close()
	waitBridgeStats(t, d, func(s Stats) bool { return s.Slots == 0 && s.Active == 0 })
}
func TestIPv6TargetsAndTruncatedHintsStayRejected(t *testing.T) {
	for _, mode := range []string{"connect", "specific_hint", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			d, _ := bridge(t)
			c := auth(t, d)
			request := make([]byte, 22)
			copy(request, []byte{5, 3, 0, 4})
			if mode == "connect" {
				request[1] = 1
				request[19] = 1
				request[21] = 80
			}
			if mode == "specific_hint" {
				request[19] = 1
			}
			if mode == "truncated" {
				request = request[:7]
			}
			c.Write(request)
			c.(*net.TCPConn).CloseWrite()
			h, e := readN(c, 10)
			if e != nil || h[1] != 8 {
				t.Fatalf("bad address accepted: %v %v", h, e)
			}
		})
	}
}
