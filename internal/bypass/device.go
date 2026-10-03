// Package bypass routes selected IPv4 TCP/UDP flows through interface-bound sockets.
// It does not change global routes and never falls back from a failed underlay.
package bypass

import (
	"context"
	"errors"
	"fmt"
	"fturnraw/bypassrules"
	"github.com/samosvalishe/free-turn-proxy/internal/netctl"
	"github.com/samosvalishe/free-turn-proxy/internal/rawvpn"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"net"
	"net/netip"
	"sync"
	"time"
)

type Device struct {
	raw       rawvpn.Device
	ctx       context.Context
	cancel    context.CancelFunc
	once      sync.Once
	writeMu   sync.Mutex
	mu        sync.Mutex
	policy    *policy
	source    netip.Addr
	mtu       int
	owners    ownerLookup
	dial      func(context.Context, string, string) (net.Conn, error)
	link      *channel.Endpoint
	stack     *stack.Stack
	decisions map[flow]*tcpDecision
	udp       map[flow]*udpFlow
	slots     chan struct{}
	lastSweep time.Time
}

func Wrap(ctx context.Context, raw rawvpn.Device, c bypassrules.Config, source netip.Addr, mtu int, resolver *net.Resolver) (rawvpn.Device, error) {
	if !c.Enabled {
		return raw, nil
	}
	if !source.Is4() || mtu < 576 || mtu > 1500 {
		return nil, errors.New("bypass requires IPv4 and MTU 576..1500")
	}
	s, e := bypassrules.Normalize(c.Settings)
	if e != nil {
		return nil, e
	}
	c.Settings = s
	d := &Device{raw: raw, policy: newPolicy(c), source: source, mtu: mtu, owners: lookupOwner, decisions: map[flow]*tcpDecision{}, udp: map[flow]*udpFlow{}, slots: make(chan struct{}, 512)}
	d.ctx, d.cancel = context.WithCancel(ctx)
	dialer := &net.Dialer{Timeout: 12 * time.Second, KeepAlive: 30 * time.Second, Control: netctl.Apply}
	d.dial = dialer.DialContext
	d.link = channel.New(512, uint32(mtu), "")
	d.stack = stack.New(stack.Options{NetworkProtocols: []stack.NetworkProtocolFactory{ipv4.NewProtocol}, TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol}})
	if e := d.stack.CreateNIC(1, d.link); e != nil {
		d.stack.Close()
		d.cancel()
		return nil, fmt.Errorf("bypass NIC: %s", e)
	}
	if e := d.stack.SetPromiscuousMode(1, true); e != nil {
		d.Close()
		return nil, fmt.Errorf("bypass promiscuous: %s", e)
	}
	if e := d.stack.SetSpoofing(1, true); e != nil {
		d.Close()
		return nil, fmt.Errorf("bypass spoofing: %s", e)
	}
	d.stack.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}})
	f := tcp.NewForwarder(d.stack, 0, 512, d.forwardTCP)
	d.stack.SetTransportProtocolHandler(tcp.ProtocolNumber, f.HandlePacket)
	go d.output()
	go func() { <-d.ctx.Done(); d.Close() }()
	if resolver != nil && len(d.policy.domains) > 0 {
		go d.resolveDomains(resolver)
	}
	return d, nil
}
func (d *Device) Name() string { return d.raw.Name() }
func (d *Device) Close() error {
	var err error
	d.once.Do(func() {
		d.cancel()
		err = d.raw.Close()
		d.mu.Lock()
		for _, u := range d.udp {
			u.conn.Close()
		}
		d.udp = map[flow]*udpFlow{}
		d.mu.Unlock()
		d.link.Close()
		d.stack.Close()
		d.stack.Wait()
	})
	return err
}
func (d *Device) Write(b []byte) (int, error) { d.policy.observe(b); return d.write(b) }
func (d *Device) write(b []byte) (int, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	if d.ctx.Err() != nil {
		return 0, net.ErrClosed
	}
	return d.raw.Write(b)
}
func (d *Device) Read(b []byte) (int, error) {
	for {
		n, e := d.raw.Read(b)
		if e != nil {
			return n, e
		}
		p, ok := parse(b[:n])
		if !ok || p.Src != d.source || protected(p.flow) {
			return n, nil
		}
		if p.Proto == 17 {
			if d.directUDP(p) {
				continue
			}
			return n, nil
		}
		if !d.directTCP(p) {
			return n, nil
		}
		pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(append([]byte(nil), p.data...))})
		d.link.InjectInbound(ipv4.ProtocolNumber, pkt)
		pkt.DecRef()
	}
}
func (d *Device) selected(f flow) (bool, owner) {
	if d.policy.site(f.Dst) {
		return true, owner{}
	}
	if len(d.policy.apps) > 0 {
		if o, ok := d.owners(f); ok && d.policy.application(o) {
			return true, o
		}
	}
	return false, owner{}
}
func (d *Device) output() {
	for {
		p := d.link.ReadContext(d.ctx)
		if p == nil {
			return
		}
		b := make([]byte, 0, p.Size())
		for _, v := range p.AsSlices() {
			b = append(b, v...)
		}
		_, _ = d.write(b)
		p.DecRef()
	}
}
func (d *Device) resolveDomains(r *net.Resolver) {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for {
		for _, name := range d.policy.domains {
			ctx, cancel := context.WithTimeout(d.ctx, 8*time.Second)
			ips, e := r.LookupNetIP(ctx, "ip4", name)
			cancel()
			if e == nil {
				for _, ip := range ips {
					d.policy.learn(ip, 300)
				}
			}
			if d.ctx.Err() != nil {
				return
			}
		}
		select {
		case <-ticker.C:
		case <-d.ctx.Done():
			return
		}
	}
}
