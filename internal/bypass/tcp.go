package bypass

import (
	"context"
	"encoding/binary"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/waiter"
	"io"
	"net"
	"net/netip"
	"strconv"
	"time"
)

type tcpDecision struct {
	direct bool
	last   time.Time
	seq    uint32
	active bool
}

func (d *Device) directTCP(p packet) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	if now.Sub(d.lastSweep) > time.Minute {
		for k, v := range d.decisions {
			if !v.active && now.Sub(v.last) > 15*time.Minute {
				delete(d.decisions, k)
			}
		}
		d.lastSweep = now
	}
	if v, ok := d.decisions[p.flow]; ok {
		if p.syn && binary.BigEndian.Uint32(p.data[p.header+4:]) != v.seq {
			yes, _ := d.selected(p.flow)
			yes = yes && !v.active
			d.decisions[p.flow] = &tcpDecision{direct: yes, last: now, seq: binary.BigEndian.Uint32(p.data[p.header+4:]), active: yes}
			return yes
		}
		v.last = now
		return v.direct
	}
	if !p.syn {
		return false
	}
	if len(d.decisions) >= 65536 {
		return false
	}
	yes, _ := d.selected(p.flow)
	d.decisions[p.flow] = &tcpDecision{direct: yes, last: now, seq: binary.BigEndian.Uint32(p.data[p.header+4:]), active: yes}
	return yes
}
func idFlow(id stack.TransportEndpointID) flow {
	return flow{Src: netip.AddrFrom4([4]byte(id.RemoteAddress.AsSlice())), Dst: netip.AddrFrom4([4]byte(id.LocalAddress.AsSlice())), SP: id.RemotePort, DP: id.LocalPort, Proto: 6}
}
func (d *Device) forwardTCP(r *tcp.ForwarderRequest) {
	f := idFlow(r.ID())
	d.mu.Lock()
	decision := d.decisions[f]
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		if decision != nil {
			decision.active = false
			decision.last = time.Now()
		}
		d.mu.Unlock()
	}()
	select {
	case d.slots <- struct{}{}:
		defer func() { <-d.slots }()
	default:
		r.Complete(true)
		return
	}
	upstream, e := d.dial(d.ctx, "tcp4", net.JoinHostPort(f.Dst.String(), strconv.Itoa(int(f.DP))))
	if e != nil {
		r.Complete(true)
		return
	}
	defer upstream.Close()
	var w waiter.Queue
	ep, err := r.CreateEndpoint(&w)
	if err != nil {
		r.Complete(true)
		return
	}
	r.Complete(false)
	local := gonet.NewTCPConn(&w, ep)
	defer local.Close()
	stop := context.AfterFunc(d.ctx, func() { local.Close(); upstream.Close() })
	defer stop()
	done := make(chan struct{}, 1)
	go func() {
		_, _ = io.Copy(upstream, local)
		if c, ok := upstream.(interface{ CloseWrite() error }); ok {
			c.CloseWrite()
		}
		done <- struct{}{}
	}()
	_, _ = io.Copy(local, upstream)
	local.CloseWrite()
	// Allow the peer's remaining half-close data, with a bounded teardown.
	select {
	case <-done:
	case <-d.ctx.Done():
	case <-time.After(30 * time.Second):
	}
	local.Close()
	upstream.Close()
}
