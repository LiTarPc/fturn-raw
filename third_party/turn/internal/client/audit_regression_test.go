package client

import (
	"net"
	"testing"
	"time"
)

func TestAuditWriteDeadlineMustInterruptBlockedTransport(t *testing.T) {
	writer, reader := net.Pipe()
	defer writer.Close()
	defer reader.Close()
	peer := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 56010}
	bm := newBindingManager()
	bound := bm.create(peer)
	bound.setState(bindingStateReady)
	bound.setRefreshedAt(time.Now())
	pm := newPermissionMap()
	perm := &permission{}
	perm.setState(permStatePermitted)
	pm.insert(peer, perm)
	c := &UDPConn{
		bindingMgr: bm, closeCh: make(chan struct{}), bindingRefreshInterval: time.Hour,
		allocation: allocation{client: &mockClient{setWriteDeadline: writer.SetWriteDeadline, writeTo: func(b []byte, _ net.Addr) (int, error) { return writer.Write(b) }}, permMap: pm, serverAddr: peer},
	}
	c.SetWriteDeadline(time.Now().Add(30 * time.Millisecond))
	done := make(chan error, 1)
	go func() { _, e := c.WriteTo([]byte("test"), peer); done <- e }()
	select {
	case e := <-done:
		if e == nil {
			t.Error("write succeeded without a reader")
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("TURN relay ignored write deadline while underlying TCP-like stream was blocked")
		writer.Close()
		<-done
	}
}
