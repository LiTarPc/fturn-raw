// Package rawsocks exposes a private SOCKS5 bridge into the RAW IP transport.
// All target TCP/UDP sockets belong to gVisor, never the host network.
package rawsocks

import (
	"context"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/miekg/dns"
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

const User = "fturn-raw"

type Device struct {
	ctx      context.Context
	cancel   context.CancelFunc
	once     sync.Once
	link     *channel.Endpoint
	stack    *stack.Stack
	listener net.Listener
	password string
	slots    chan struct{}
	mu       sync.Mutex
	sockets  map[net.Conn]struct{}
}

func Open(address netip.Addr, mtu, port int, password string) (*Device, error) {
	if !address.Is4() || mtu < 576 || mtu > 1500 || port < 1 || port > 65535 || len(password) < 16 || len(password) > 255 {
		return nil, errors.New("invalid RAW SOCKS bridge configuration")
	}
	l, e := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	d := &Device{ctx: ctx, cancel: cancel, listener: l, password: password, slots: make(chan struct{}, 256), sockets: map[net.Conn]struct{}{}}
	d.link = channel.New(1024, uint32(mtu), "")
	d.stack = stack.New(stack.Options{NetworkProtocols: []stack.NetworkProtocolFactory{ipv4.NewProtocol}, TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol}})
	if e := d.stack.CreateNIC(1, d.link); e != nil {
		d.Close()
		return nil, fmt.Errorf("RAW SOCKS NIC: %s", e)
	}
	if e := d.stack.AddProtocolAddress(1, tcpip.ProtocolAddress{Protocol: ipv4.ProtocolNumber, AddressWithPrefix: tcpip.AddrFrom4(address.As4()).WithPrefix()}, stack.AddressProperties{}); e != nil {
		d.Close()
		return nil, fmt.Errorf("RAW SOCKS address: %s", e)
	}
	d.stack.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}})
	sack := tcpip.TCPSACKEnabled(true)
	d.stack.SetTransportProtocolOption(tcp.ProtocolNumber, &sack)
	go d.accept()
	return d, nil
}
func (d *Device) Name() string { return "raw-socks-netstack" }
func (d *Device) Read(b []byte) (int, error) {
	p := d.link.ReadContext(d.ctx)
	if p == nil {
		return 0, net.ErrClosed
	}
	defer p.DecRef()
	if p.Size() > len(b) {
		return 0, io.ErrShortBuffer
	}
	n := 0
	for _, v := range p.AsSlices() {
		n += copy(b[n:], v)
	}
	return n, nil
}
func (d *Device) Write(b []byte) (int, error) {
	if d.ctx.Err() != nil {
		return 0, net.ErrClosed
	}
	if len(b) < 20 || b[0]>>4 != 4 {
		return 0, errors.New("RAW SOCKS requires IPv4 packets")
	}
	p := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(append([]byte(nil), b...))})
	defer p.DecRef()
	d.link.InjectInbound(ipv4.ProtocolNumber, p)
	return len(b), nil
}
func (d *Device) Close() error {
	d.once.Do(func() {
		d.cancel()
		d.listener.Close()
		d.mu.Lock()
		for c := range d.sockets {
			c.Close()
		}
		d.mu.Unlock()
		d.link.Close()
		d.stack.Close()
		d.stack.Wait()
	})
	return nil
}
func (d *Device) track(c net.Conn) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ctx.Err() != nil {
		c.Close()
		return false
	}
	d.sockets[c] = struct{}{}
	return true
}
func (d *Device) forget(c net.Conn) { c.Close(); d.mu.Lock(); delete(d.sockets, c); d.mu.Unlock() }
func (d *Device) accept() {
	for {
		c, e := d.listener.Accept()
		if e != nil {
			return
		}
		select {
		case d.slots <- struct{}{}:
			if !d.track(c) {
				<-d.slots
				return
			}
			go func() { defer func() { d.forget(c); <-d.slots }(); d.handle(c) }()
		default:
			c.Close()
		}
	}
}
func readN(r io.Reader, n int) ([]byte, error) {
	b := make([]byte, n)
	_, e := io.ReadFull(r, b)
	return b, e
}
func (d *Device) authenticate(c net.Conn) error {
	h, e := readN(c, 2)
	if e != nil {
		return e
	}
	if h[0] != 5 || h[1] == 0 {
		return errors.New("invalid SOCKS greeting")
	}
	methods, e := readN(c, int(h[1]))
	if e != nil {
		return e
	}
	found := false
	for _, m := range methods {
		if m == 2 {
			found = true
		}
	}
	if !found {
		c.Write([]byte{5, 255})
		return errors.New("authentication required")
	}
	if _, e = c.Write([]byte{5, 2}); e != nil {
		return e
	}
	h, e = readN(c, 2)
	if e != nil {
		return e
	}
	if h[0] != 1 || h[1] == 0 {
		return errors.New("invalid SOCKS authentication")
	}
	user, e := readN(c, int(h[1]))
	if e != nil {
		return e
	}
	h, e = readN(c, 1)
	if e != nil {
		return e
	}
	pass, e := readN(c, int(h[0]))
	if e != nil {
		return e
	}
	if subtle.ConstantTimeCompare(user, []byte(User)) != 1 || subtle.ConstantTimeCompare(pass, []byte(d.password)) != 1 {
		c.Write([]byte{1, 1})
		return errors.New("invalid credentials")
	}
	_, e = c.Write([]byte{1, 0})
	return e
}
func readTarget(r io.Reader, atyp byte) (string, uint16, error) {
	var host string
	switch atyp {
	case 1:
		b, e := readN(r, 4)
		if e != nil {
			return "", 0, e
		}
		host = net.IP(b).String()
	case 3:
		b, e := readN(r, 1)
		if e != nil {
			return "", 0, e
		}
		if b[0] == 0 {
			return "", 0, errors.New("empty SOCKS host")
		}
		b, e = readN(r, int(b[0]))
		if e != nil {
			return "", 0, e
		}
		host = string(b)
	default:
		return "", 0, errors.New("RAW SOCKS supports IPv4 only")
	}
	b, e := readN(r, 2)
	if e != nil {
		return "", 0, e
	}
	return host, binary.BigEndian.Uint16(b), nil
}
func reply(c net.Conn, code byte, port int) error {
	_, e := c.Write([]byte{5, code, 0, 1, 127, 0, 0, 1, byte(port >> 8), byte(port)})
	return e
}
func (d *Device) handle(c net.Conn) {
	c.SetDeadline(time.Now().Add(15 * time.Second))
	if d.authenticate(c) != nil {
		return
	}
	h, e := readN(c, 4)
	if e != nil {
		return
	}
	if h[0] != 5 || h[2] != 0 {
		reply(c, 1, 0)
		return
	}
	host, port, e := readTarget(c, h[3])
	if e != nil {
		reply(c, 8, 0)
		return
	}
	if h[1] == 3 {
		d.associate(c)
		return
	}
	if h[1] != 1 {
		reply(c, 7, 0)
		return
	}
	ctx, cancel := context.WithTimeout(d.ctx, 15*time.Second)
	defer cancel()
	ip, e := d.resolve(ctx, host)
	if e != nil || port == 0 {
		reply(c, 4, 0)
		return
	}
	target, e := gonet.DialContextTCP(ctx, d.stack, tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4(ip.As4()), Port: port}, ipv4.ProtocolNumber)
	if e != nil {
		reply(c, 5, 0)
		return
	}
	if !d.track(target) {
		return
	}
	defer d.forget(target)
	if reply(c, 0, 0) != nil {
		return
	}
	c.SetDeadline(time.Time{})
	done := make(chan struct{})
	go func() {
		_, err := io.Copy(target, c)
		target.CloseWrite()
		if err != nil {
			c.Close()
			target.Close()
		}
		close(done)
	}()
	_, err := io.Copy(c, target)
	c.(*net.TCPConn).CloseWrite()
	if err != nil {
		c.Close()
		target.Close()
	}
	// EOF closes only this direction. The peer may still be receiving upload.
	<-done
}
func (d *Device) resolve(ctx context.Context, host string) (netip.Addr, error) {
	if ip, e := netip.ParseAddr(host); e == nil {
		if ip.Is4() {
			return ip, nil
		}
		return netip.Addr{}, errors.New("IPv6 unsupported")
	}
	// Resolve only through the RAW tunnel; never use Android/host DNS for client destinations.
	q := new(dns.Msg)
	q.SetQuestion(dns.Fqdn(host), dns.TypeA)
	target := tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{1, 1, 1, 1}), Port: 53}
	conn, e := gonet.DialContextTCP(ctx, d.stack, target, ipv4.ProtocolNumber)
	if e != nil {
		return netip.Addr{}, e
	}
	defer conn.Close()
	if end, ok := ctx.Deadline(); ok {
		conn.SetDeadline(end)
	}
	response, _, e := (&dns.Client{}).ExchangeWithConn(q, &dns.Conn{Conn: conn})
	if e != nil {
		return netip.Addr{}, e
	}
	for _, rr := range response.Answer {
		if a, ok := rr.(*dns.A); ok {
			ip, ok := netip.AddrFromSlice(a.A)
			if ok {
				return ip.Unmap(), nil
			}
		}
	}
	return netip.Addr{}, errors.New("no IPv4 DNS answer")
}
func (d *Device) associate(c net.Conn) { d.associateWithTimeout(c, 2*time.Minute) }

func (d *Device) associateWithTimeout(c net.Conn, idle time.Duration) {
	relay, e := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		reply(c, 1, 0)
		return
	}
	defer relay.Close()
	if reply(c, 0, relay.LocalAddr().(*net.UDPAddr).Port) != nil {
		return
	}
	c.SetDeadline(time.Time{})
	ctx, cancel := context.WithCancel(d.ctx)
	defer cancel()
	go func() { io.Copy(io.Discard, c); cancel(); relay.Close() }()
	stop := context.AfterFunc(ctx, func() { relay.Close(); c.Close() })
	defer stop()
	type flow struct {
		conn   *gonet.UDPConn
		header []byte
	}
	flows := map[string]*flow{}
	var flowMu sync.Mutex
	var readers sync.WaitGroup
	defer func() {
		cancel()
		relay.Close()
		c.Close()
		flowMu.Lock()
		for _, f := range flows {
			f.conn.Close()
		}
		flowMu.Unlock()
		readers.Wait()
	}()
	var peer *net.UDPAddr
	b := make([]byte, 65535)
	for {
		n, from, e := relay.ReadFromUDP(b)
		if e != nil {
			return
		}
		if !from.IP.IsLoopback() || peer != nil && from.String() != peer.String() {
			continue
		}
		if n < 4 || b[0] != 0 || b[1] != 0 || b[2] != 0 {
			continue
		}
		reader := bytesReader(b[4:n])
		host, port, e := readTarget(&reader, b[3])
		if e != nil || port == 0 {
			continue
		}
		if peer == nil {
			peer = from
		}
		offset := n - len(reader)
		key := net.JoinHostPort(host, strconv.Itoa(int(port)))
		flowMu.Lock()
		f := flows[key]
		full := len(flows) >= 128
		flowMu.Unlock()
		if f == nil {
			if full {
				continue
			}
			lookupCtx, lookupCancel := context.WithTimeout(ctx, 10*time.Second)
			ip, e := d.resolve(lookupCtx, host)
			lookupCancel()
			if e != nil {
				continue
			}
			target := tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4(ip.As4()), Port: port}
			conn, e := gonet.DialUDP(d.stack, nil, &target, ipv4.ProtocolNumber)
			if e != nil {
				continue
			}
			addr := ip.As4()
			head := []byte{0, 0, 0, 1, addr[0], addr[1], addr[2], addr[3], byte(port >> 8), byte(port)}
			f = &flow{conn, head}
			flowMu.Lock()
			flows[key] = f
			flowMu.Unlock()
			readers.Add(1)
			go func(key string, f *flow, peer net.UDPAddr) {
				defer readers.Done()
				defer func() {
					flowMu.Lock()
					if flows[key] == f {
						delete(flows, key)
					}
					flowMu.Unlock()
					f.conn.Close()
				}()
				out := make([]byte, 65535)
				for {
					f.conn.SetReadDeadline(time.Now().Add(idle))
					n, e := f.conn.Read(out)
					if e != nil {
						return
					}
					if _, e = relay.WriteToUDP(append(append([]byte{}, f.header...), out[:n]...), &peer); e != nil {
						return
					}
				}
			}(key, f, *peer)
		}
		// Outbound traffic also keeps a one-way UDP flow alive.
		f.conn.SetReadDeadline(time.Now().Add(idle))
		f.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := f.conn.Write(b[offset:n]); err != nil {
			flowMu.Lock()
			if flows[key] == f {
				delete(flows, key)
			}
			flowMu.Unlock()
			f.conn.Close()
		}
	}
}

type bytesReader []byte

func (b *bytesReader) Read(p []byte) (int, error) {
	if len(*b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, *b)
	*b = (*b)[n:]
	return n, nil
}
