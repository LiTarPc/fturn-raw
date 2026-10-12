//go:build windows

package rawvpn

import (
	"context"
	"encoding/binary"
	"fmt"
	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/sys/windows"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type dnsExchangeFunc func(context.Context, []byte) ([]byte, error)

func (f dnsExchangeFunc) exchange(c context.Context, q []byte) ([]byte, error) { return f(c, q) }
func operatorTestReply(query []byte) []byte {
	var q dnsmessage.Message
	_ = q.Unpack(query)
	m := dnsmessage.Message{Header: dnsmessage.Header{ID: q.ID, Response: true, RecursionAvailable: true}, Questions: q.Questions}
	if q.Questions[0].Type == dnsmessage.TypeA {
		m.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 30}, Body: &dnsmessage.AResource{A: [4]byte{192, 0, 2, 7}}}}
	}
	b, _ := m.Pack()
	return b
}
func operatorTestServer(t *testing.T, handler func(net.PacketConn, []byte, net.Addr)) net.PacketConn {
	t.Helper()
	c, e := net.ListenPacket("udp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = c.Close() })
	go func() {
		b := make([]byte, 65536)
		for {
			n, a, e := c.ReadFrom(b)
			if e != nil {
				return
			}
			handler(c, append([]byte(nil), b[:n]...), a)
		}
	}()
	return c
}
func operatorTestClient(t *testing.T, servers []net.Addr, f controlDNSExchanger) *operatorDNS {
	t.Helper()
	c, e := listenExclusiveDNS(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	d := newOperatorDNS(c, servers, f)
	d.timeout = 50 * time.Millisecond
	t.Cleanup(func() { _ = c.Close(); <-d.done })
	return d
}
func TestOperatorDNSWorksWhenDoHIsUnavailable(t *testing.T) {
	var fallback atomic.Int32
	server := operatorTestServer(t, func(c net.PacketConn, q []byte, a net.Addr) { _, _ = c.WriteTo(operatorTestReply(q), a) })
	d := operatorTestClient(t, []net.Addr{server.LocalAddr()}, dnsExchangeFunc(func(context.Context, []byte) ([]byte, error) {
		fallback.Add(1)
		return nil, fmt.Errorf("Cloudflare unreachable")
	}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ips, e := resolverForControlDNS(d).LookupIP(ctx, "ip4", "operator-only.invalid")
	if e != nil || len(ips) != 1 || !ips[0].Equal(net.IPv4(192, 0, 2, 7)) || fallback.Load() != 0 {
		t.Fatalf("operator DNS should not require DoH: %v %v", ips, e)
	}
}
func TestOperatorDNSConcurrentQueriesIgnoreInvalidReplies(t *testing.T) {
	server := operatorTestServer(t, func(c net.PacketConn, q []byte, a net.Addr) {
		reply := operatorTestReply(q)
		bad := append([]byte(nil), reply...)
		bad[0] ^= 0xff
		_, _ = c.WriteTo(bad, a)
		_, _ = c.WriteTo(reply, a)
	})
	d := operatorTestClient(t, []net.Addr{server.LocalAddr()}, nil)
	d.timeout = time.Second
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			q := dnsmessage.Message{Header: dnsmessage.Header{ID: 7}, Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName(fmt.Sprintf("query%d.invalid.", i)), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}}
			wire, _ := q.Pack()
			reply, e := d.exchange(context.Background(), wire)
			if e != nil || len(reply) < 12 || binary.BigEndian.Uint16(reply) != 7 {
				t.Errorf("parallel query %d: %v", i, e)
			}
		}(i)
	}
	wg.Wait()
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.pending) != 0 {
		t.Fatal("pending queries leaked")
	}
}
func TestOperatorDNSFallbackAndCancellation(t *testing.T) {
	silent := operatorTestServer(t, func(net.PacketConn, []byte, net.Addr) {})
	var fallback atomic.Int32
	d := operatorTestClient(t, []net.Addr{silent.LocalAddr()}, dnsExchangeFunc(func(c context.Context, q []byte) ([]byte, error) { fallback.Add(1); return operatorTestReply(q), nil }))
	q := dnsmessage.Message{Header: dnsmessage.Header{ID: 9}, Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("fallback.invalid."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}}
	wire, _ := q.Pack()
	if _, e := d.exchange(context.Background(), wire); e != nil || fallback.Load() != 1 {
		t.Fatalf("fallback not used: %v", e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if _, e := d.exchange(ctx, wire); e == nil || fallback.Load() != 1 {
		t.Fatal("cancelled lookup launched fallback")
	}
}
func TestOperatorDNSExclusivePortCannotBeRebound(t *testing.T) {
	d := operatorTestClient(t, nil, nil)
	port := d.conn.LocalAddr().(*net.UDPAddr).Port
	for _, ip := range []string{"0.0.0.0", "127.0.0.1"} {
		for _, reuse := range []bool{false, true} {
			lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
				if !reuse {
					return nil
				}
				var e error
				_ = c.Control(func(fd uintptr) {
					e = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_REUSEADDR, 1)
				})
				return e
			}}
			c, e := lc.ListenPacket(context.Background(), "udp4", fmt.Sprintf("%s:%d", ip, port))
			if e == nil {
				_ = c.Close()
				t.Fatalf("reserved port rebound: ip=%s reuse=%t", ip, reuse)
			}
		}
	}
}
