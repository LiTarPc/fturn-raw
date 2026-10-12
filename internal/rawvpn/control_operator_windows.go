//go:build windows

package rawvpn

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/samosvalishe/free-turn-proxy/internal/netctl"
	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/sys/windows"
)

// The one wildcard-bound exclusive socket stays alive for the whole core
// process. Privacy rules exempt only its occupied UDP source port, never a
// DNS destination or another application's socket.
type operatorDNS struct {
	conn      net.PacketConn
	servers   []net.Addr
	preferred atomic.Uint32
	timeout   time.Duration
	mu        sync.Mutex
	pending   map[uint16]*operatorQuery
	done      chan struct{}
	fallback  controlDNSExchanger
}
type operatorQuery struct {
	server   string
	question dnsmessage.Question
	answer   chan []byte
}

var rawOperatorDNS atomic.Pointer[operatorDNS]

func ControlDNSPort() int {
	if d := rawOperatorDNS.Load(); d != nil {
		return d.conn.LocalAddr().(*net.UDPAddr).Port
	}
	return 0
}
func CloseControlDNS() {
	if d := rawOperatorDNS.Swap(nil); d != nil {
		_ = d.conn.Close()
	}
}
func ControlDNSDescription() string {
	if d := rawOperatorDNS.Load(); d != nil {
		names := make([]string, len(d.servers))
		for i, s := range d.servers {
			names[i] = s.String()
		}
		return fmt.Sprintf("selected adapter DNS=%s; exclusive UDP source port=%d; HTTPS fallback", strings.Join(names, ","), ControlDNSPort())
	}
	return "HTTPS service DNS via selected underlay"
}

// Bypass site pre-resolution keeps the existing encrypted service resolver;
// ordinary application DNS packets continue through Raw.
func BypassResolver() *net.Resolver { return controlDNSResolver() }

func adapterDNSServers(index int) ([]net.Addr, error) {
	size := uint32(15000)
	for attempt := 0; attempt < 3; attempt++ {
		data := make([]byte, size)
		head := (*windows.IpAdapterAddresses)(unsafe.Pointer(&data[0]))
		err := windows.GetAdaptersAddresses(windows.AF_INET, windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST, 0, head, &size)
		if errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for a := head; a != nil; a = a.Next {
			if int(a.IfIndex) != index {
				continue
			}
			var servers []net.Addr
			seen := map[string]bool{}
			for s := a.FirstDnsServerAddress; s != nil; s = s.Next {
				if s.Address.Sockaddr == nil {
					continue
				}
				ip := s.Address.IP().To4()
				if ip == nil || ip.IsUnspecified() || ip.Equal(net.IPv4(10, 77, 0, 2)) {
					continue
				}
				text := ip.String()
				if !seen[text] {
					seen[text] = true
					servers = append(servers, &net.UDPAddr{IP: append(net.IP(nil), ip...), Port: 53})
				}
			}
			return servers, nil
		}
		return nil, fmt.Errorf("selected DNS adapter not found")
	}
	return nil, fmt.Errorf("adapter DNS enumeration changed repeatedly")
}
func listenExclusiveDNS(ctx context.Context, control func(string, string, syscall.RawConn) error) (net.PacketConn, error) {
	lc := net.ListenConfig{Control: func(network, address string, c syscall.RawConn) error {
		if control != nil {
			if err := control(network, "192.0.2.53:53", c); err != nil {
				return err
			}
		}
		var socketErr error
		if err := c.Control(func(fd uintptr) {
			socketErr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, ^windows.SO_REUSEADDR, 1)
		}); err != nil {
			return err
		}
		return socketErr
	}}
	return lc.ListenPacket(ctx, "udp4", "0.0.0.0:0")
}
func newOperatorDNS(conn net.PacketConn, servers []net.Addr, fallback controlDNSExchanger) *operatorDNS {
	d := &operatorDNS{conn: conn, servers: servers, timeout: 1500 * time.Millisecond, pending: map[uint16]*operatorQuery{}, done: make(chan struct{}), fallback: fallback}
	go d.readReplies()
	return d
}
func (d *operatorDNS) readReplies() {
	defer close(d.done)
	buf := make([]byte, 65536)
	for {
		n, from, err := d.conn.ReadFrom(buf)
		if err != nil {
			return
		}
		if n < 12 {
			continue
		}
		id := binary.BigEndian.Uint16(buf[:2])
		var reply dnsmessage.Message
		if reply.Unpack(buf[:n]) != nil || !reply.Response || len(reply.Questions) != 1 {
			continue
		}
		d.mu.Lock()
		q := d.pending[id]
		d.mu.Unlock()
		if q == nil || from.String() != q.server {
			continue
		}
		got := reply.Questions[0]
		want := q.question
		if !strings.EqualFold(got.Name.String(), want.Name.String()) || got.Type != want.Type || got.Class != want.Class {
			continue
		}
		select {
		case q.answer <- append([]byte(nil), buf[:n]...):
		default:
		}
	}
}
func (d *operatorDNS) query(ctx context.Context, server net.Addr, wire []byte, question dnsmessage.Question) ([]byte, error) {
	q := &operatorQuery{server: server.String(), question: question, answer: make(chan []byte, 1)}
	var id uint16
	d.mu.Lock()
	if len(d.pending) >= 1024 {
		d.mu.Unlock()
		return nil, fmt.Errorf("service DNS query limit reached")
	}
	for {
		var random [2]byte
		if _, err := rand.Read(random[:]); err != nil {
			d.mu.Unlock()
			return nil, err
		}
		id = binary.BigEndian.Uint16(random[:])
		if d.pending[id] == nil {
			break
		}
	}
	d.pending[id] = q
	d.mu.Unlock()
	defer func() { d.mu.Lock(); delete(d.pending, id); d.mu.Unlock() }()
	packet := append([]byte(nil), wire...)
	binary.BigEndian.PutUint16(packet, id)
	if _, err := d.conn.WriteTo(packet, server); err != nil {
		return nil, err
	}
	timer := time.NewTimer(d.timeout)
	defer timer.Stop()
	select {
	case reply := <-q.answer:
		binary.BigEndian.PutUint16(reply, binary.BigEndian.Uint16(wire[:2]))
		return reply, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-d.done:
		return nil, net.ErrClosed
	case <-timer.C:
		return nil, fmt.Errorf("UDP DNS timeout")
	}
}
func (d *operatorDNS) exchange(ctx context.Context, wire []byte) ([]byte, error) {
	var query dnsmessage.Message
	if query.Unpack(wire) != nil || query.Response || len(query.Questions) != 1 {
		return nil, fmt.Errorf("invalid DNS query")
	}
	failures := []error{}
	start := int(d.preferred.Load())
	for attempt := 0; attempt < len(d.servers); attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		i := (start + attempt) % len(d.servers)
		reply, err := d.query(ctx, d.servers[i], wire, query.Questions[0])
		if err == nil {
			var message dnsmessage.Message
			_ = message.Unpack(reply)
			if !message.Truncated && message.RCode != dnsmessage.RCodeServerFailure && message.RCode != dnsmessage.RCodeRefused {
				d.preferred.Store(uint32(i))
				return reply, nil
			}
			err = fmt.Errorf("UDP DNS temporary failure or truncated response")
		}
		failures = append(failures, fmt.Errorf("%s: %w", d.servers[i], err))
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if d.fallback != nil {
		reply, err := d.fallback.exchange(ctx, wire)
		if err == nil {
			return reply, nil
		}
		failures = append(failures, err)
	}
	return nil, fmt.Errorf("selected adapter service DNS unavailable: %w", errors.Join(failures...))
}
func selectedDNSResolver(index int) (*net.Resolver, error) {
	servers, err := adapterDNSServers(index)
	if err != nil {
		return nil, fmt.Errorf("raw: adapter DNS: %w", err)
	}
	fallback := newControlDNSPool([]controlDNSUpstream{controlDoHUpstream("Cloudflare 1.1.1.1", "1.1.1.1:443"), controlDoHUpstream("Cloudflare 1.0.0.1", "1.0.0.1:443")})
	if len(servers) == 0 {
		return resolverForControlDNS(fallback), nil
	}
	conn, err := listenExclusiveDNS(context.Background(), netctl.Apply)
	if err != nil {
		return nil, fmt.Errorf("raw: reserve service DNS socket: %w", err)
	}
	d := newOperatorDNS(conn, servers, fallback)
	rawOperatorDNS.Store(d)
	return resolverForControlDNS(d), nil
}
