package bypass

import (
	"encoding/binary"
	"net"
	"net/netip"
	"os"
	"strings"
	"testing"
)

func TestTableByteOrderAndAmbiguity(t *testing.T) {
	f := flow{Src: netip.MustParseAddr("10.77.0.2"), Dst: netip.MustParseAddr("203.0.113.9"), SP: 45000, DP: 443, Proto: 6}
	b := make([]byte, 28)
	binary.LittleEndian.PutUint32(b, 1)
	copy(b[8:], []byte{10, 77, 0, 2})
	binary.BigEndian.PutUint16(b[12:], 45000)
	copy(b[16:], []byte{203, 0, 113, 9})
	binary.BigEndian.PutUint16(b[20:], 443)
	binary.LittleEndian.PutUint32(b[24:], 123)
	if pid, ok := tableOwner(b, 24, f); !ok || pid != 123 {
		t.Fatal("TCP table byte order")
	}
	u := make([]byte, 28)
	binary.LittleEndian.PutUint32(u, 2)
	for i := 0; i < 2; i++ {
		row := u[4+i*12:]
		binary.BigEndian.PutUint16(row[4:], 45000)
		binary.LittleEndian.PutUint32(row[8:], uint32(i+1))
	}
	if _, ok := tableOwner(u, 12, f); ok {
		t.Fatal("ambiguous UDP table guessed")
	}
	binary.LittleEndian.PutUint32(u[24:], 1)
	if pid, ok := tableOwner(u, 12, f); !ok || pid != 1 {
		t.Fatal("wildcard UDP table")
	}
}
func TestLiveWindowsSocketOwner(t *testing.T) {
	c, e := net.ListenPacket("udp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	addr := c.LocalAddr().(*net.UDPAddr)
	ip, _ := netip.AddrFromSlice(addr.IP.To4())
	o, ok := lookupOwner(flow{Src: ip, SP: uint16(addr.Port), Proto: 17})
	path, _ := os.Executable()
	if !ok || o.PID != uint32(os.Getpid()) || !strings.EqualFold(o.Path, path) {
		t.Fatal("Windows UDP owner lookup failed")
	}
}

func TestLiveWindowsTCPPeerOwner(t *testing.T) {
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	c, e := net.Dial("tcp4", l.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	a := c.LocalAddr().(*net.TCPAddr)
	b := c.RemoteAddr().(*net.TCPAddr)
	src, _ := netip.AddrFromSlice(a.IP.To4())
	dst, _ := netip.AddrFromSlice(b.IP.To4())
	o, ok := lookupOwner(flow{Src: src, Dst: dst, SP: uint16(a.Port), DP: uint16(b.Port), Proto: 6})
	if !ok || o.PID != uint32(os.Getpid()) {
		t.Fatal("Windows TCP peer owner lookup failed")
	}
}
