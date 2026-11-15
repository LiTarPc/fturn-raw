package turndial

import (
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

type waitingRelay struct {
	net.PacketConn
	done chan struct{}
	once sync.Once
}

func (p *waitingRelay) WriteTo([]byte, net.Addr) (int, error) { <-p.done; return 0, net.ErrClosed }
func (p *waitingRelay) SetWriteDeadline(time.Time) error      { return nil }
func (p *waitingRelay) close()                                { p.once.Do(func() { close(p.done) }) }
func TestWriteDeadlineInterruptsPermissionWait(t *testing.T) {
	raw := &waitingRelay{done: make(chan struct{})}
	defer raw.close()
	c := &deadlineRelay{PacketConn: raw, abort: raw.close}
	c.SetWriteDeadline(time.Now().Add(30 * time.Millisecond))
	done := make(chan error, 1)
	go func() { _, err := c.WriteTo([]byte("data"), &net.UDPAddr{}); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("timeout=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("permission wait survived deadline")
	}
}
func TestChangingPendingDeadlineAndClearingIdleDeadline(t *testing.T) {
	raw := &waitingRelay{done: make(chan struct{})}
	defer raw.close()
	c := &deadlineRelay{PacketConn: raw, abort: raw.close}
	c.SetWriteDeadline(time.Now().Add(-time.Second))
	c.SetWriteDeadline(time.Time{})
	done := make(chan error, 1)
	go func() { _, err := c.WriteTo(nil, &net.UDPAddr{}); done <- err }()
	select {
	case err := <-done:
		t.Fatalf("cleared deadline terminated write: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	c.SetWriteDeadline(time.Now())
	select {
	case err := <-done:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("changing deadline did not wake pending write")
	}
}
