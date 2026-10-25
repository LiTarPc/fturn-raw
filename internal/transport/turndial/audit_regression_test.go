package turndial

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestAuditAllocateMustHonorContext(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, e := l.Accept()
		if e == nil {
			accepted <- c
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		s, e := Open(ctx, Config{}, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 56010}, "u", "p", l.Addr().String())
		if s != nil {
			s.Close()
		}
		done <- e
	}()
	c := <-accepted
	defer c.Close()
	// Consume the first request so cancellation is tested inside Allocate.
	c.SetReadDeadline(time.Now().Add(time.Second))
	b := make([]byte, 1024)
	if _, e := c.Read(b); e != nil {
		t.Fatal(e)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled Allocate succeeded")
		}
		return
	case <-time.After(time.Second):
		t.Error("Open is still blocked in Allocate one second after context cancellation")
	}
	c.Close()
	select {
	case <-done:
	case <-time.After(12 * time.Second):
	}
}

func TestAllocateUDPRespectsCancellation(t *testing.T) {
	socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		stream, e := Open(ctx, Config{TransportUDP: true}, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 56010}, "u", "p", socket.LocalAddr().String())
		if stream != nil {
			stream.Close()
		}
		done <- e
	}()
	socket.SetReadDeadline(time.Now().Add(time.Second))
	b := make([]byte, 2048)
	if _, _, err = socket.ReadFromUDP(b); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("UDP Allocate ignored cancellation")
	}
}
