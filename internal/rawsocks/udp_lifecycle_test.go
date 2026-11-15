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

func TestUDPAssociationRecreatesExpiredFlow(t *testing.T) {
	d, peer := bridge(t)
	target := tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 9}), Port: 443}
	echo, err := gonet.DialUDP(peer, &target, nil, ipv4.ProtocolNumber)
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		b := make([]byte, 2048)
		for {
			n, from, e := echo.ReadFrom(b)
			if e != nil {
				return
			}
			echo.WriteTo(b[:n], from)
		}
	}()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	control, err := net.Dial("tcp4", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	server, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	done := make(chan struct{})
	go func() { d.associateWithTimeout(server, 50*time.Millisecond); close(done) }()
	defer func() {
		control.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("association did not stop")
		}
	}()
	control.SetReadDeadline(time.Now().Add(time.Second))
	h, err := readN(control, 10)
	if err != nil || h[1] != 0 {
		t.Fatalf("reply %v %v", h, err)
	}
	u, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(binary.BigEndian.Uint16(h[8:]))})
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	for i := 0; i < 3; i++ {
		u.SetDeadline(time.Now().Add(time.Second))
		packet := append([]byte{0, 0, 0, 1, 203, 0, 113, 9, 1, 187}, byte(i))
		if _, err = u.Write(packet); err != nil {
			t.Fatal(err)
		}
		b := make([]byte, 2048)
		n, e := u.Read(b)
		if e != nil || n != 11 || b[10] != byte(i) {
			t.Fatalf("exchange %d after idle: n=%d err=%v", i, n, e)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestUDPAssociationReclaimsCapacity(t *testing.T) {
	d, peer := bridge(t)
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	control, err := net.Dial("tcp4", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	server, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	done := make(chan struct{})
	go func() { d.associateWithTimeout(server, 30*time.Millisecond); close(done) }()
	defer func() {
		control.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("association cleanup stalled")
		}
	}()
	control.SetReadDeadline(time.Now().Add(time.Second))
	h, err := readN(control, 10)
	if err != nil {
		t.Fatal(err)
	}
	u, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(binary.BigEndian.Uint16(h[8:]))})
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	for i := 0; i < 129; i++ {
		port := uint16(20000 + i)
		addr := tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 9}), Port: port}
		echo, e := gonet.DialUDP(peer, &addr, nil, ipv4.ProtocolNumber)
		if e != nil {
			t.Fatal(e)
		}
		echoed := make(chan struct{})
		go func() {
			defer close(echoed)
			b := make([]byte, 32)
			n, from, e := echo.ReadFrom(b)
			if e == nil {
				echo.WriteTo(b[:n], from)
			}
		}()
		packet := append([]byte{0, 0, 0, 1, 203, 0, 113, 9, byte(port >> 8), byte(port)}, byte(i))
		u.SetDeadline(time.Now().Add(time.Second))
		_, e = u.Write(packet)
		if e != nil {
			echo.Close()
			t.Fatal(e)
		}
		b := make([]byte, 32)
		n, e := u.Read(b)
		echo.Close()
		<-echoed
		if e != nil || n != 11 || b[10] != byte(i) {
			t.Fatalf("destination %d: n=%d err=%v", i, n, e)
		}
		if i%16 == 15 {
			time.Sleep(60 * time.Millisecond)
		}
	}
}
