package turndial

import (
	"errors"
	"net"
	"os"
	"sync"
	"time"
)

// boundedTransport bounds every physical write, including TURN maintenance.
// An expired relay deadline is cleared by its caller after the application write.
type boundedTransport struct {
	net.PacketConn
	writeMu  sync.Mutex
	mu       sync.Mutex
	deadline time.Time
}

func (c *boundedTransport) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadline = t
	return c.PacketConn.SetWriteDeadline(c.limit())
}
func (c *boundedTransport) limit() time.Time {
	t := time.Now().Add(3 * time.Second)
	if !c.deadline.IsZero() && c.deadline.Before(t) {
		t = c.deadline
	}
	return t
}
func (c *boundedTransport) WriteTo(b []byte, addr net.Addr) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	err := c.PacketConn.SetWriteDeadline(c.limit())
	c.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return c.PacketConn.WriteTo(b, addr)
}

// A relay write can wait for CreatePermission as well as for socket I/O.
// On timeout discard this allocation; no goroutine is abandoned waiting for STUN.
type deadlineRelay struct {
	net.PacketConn
	abort    func()
	mu       sync.Mutex
	deadline time.Time
	timer    *time.Timer
	pending  int
	epoch    uint64
	expired  bool
}

func (c *deadlineRelay) arm() {
	c.epoch++
	epoch := c.epoch
	if c.timer != nil {
		c.timer.Stop()
	}
	if c.pending == 0 || c.deadline.IsZero() {
		return
	}
	c.timer = time.AfterFunc(time.Until(c.deadline), func() {
		c.mu.Lock()
		if c.epoch != epoch || c.pending == 0 {
			c.mu.Unlock()
			return
		}
		c.expired = true
		c.mu.Unlock()
		c.abort()
	})
}
func (c *deadlineRelay) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadline = t
	c.arm()
	return c.PacketConn.SetWriteDeadline(t)
}
func (c *deadlineRelay) SetDeadline(t time.Time) error {
	return errors.Join(c.SetReadDeadline(t), c.SetWriteDeadline(t))
}
func (c *deadlineRelay) WriteTo(b []byte, addr net.Addr) (int, error) {
	c.mu.Lock()
	if c.expired || (!c.deadline.IsZero() && !time.Now().Before(c.deadline)) {
		c.mu.Unlock()
		return 0, os.ErrDeadlineExceeded
	}
	c.pending++
	c.arm()
	c.mu.Unlock()
	n, err := c.PacketConn.WriteTo(b, addr)
	c.mu.Lock()
	c.pending--
	c.arm()
	expired := c.expired
	c.mu.Unlock()
	if expired {
		return n, os.ErrDeadlineExceeded
	}
	return n, err
}
