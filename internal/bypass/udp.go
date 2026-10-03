package bypass

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"sync/atomic"
	"time"
)

type udpFlow struct {
	conn     net.Conn
	identity owner
	last     time.Time
	src, dst netip.Addr
	sp, dp   uint16
}

func (d *Device) directUDP(p packet) bool {
	// Recheck process ownership for every datagram: Windows can reuse UDP ports.
	yes, o := d.selected(p.flow)
	d.mu.Lock()
	u := d.udp[p.flow]
	if !yes || u != nil && u.identity != o {
		if u != nil {
			u.conn.Close()
			delete(d.udp, p.flow)
		}
		u = nil
	}
	if !yes {
		d.mu.Unlock()
		return false
	}
	if u == nil && len(d.udp) >= 1024 {
		d.mu.Unlock()
		return true
	}
	d.mu.Unlock()
	if u == nil {
		ctx, cancel := context.WithTimeout(d.ctx, 3*time.Second)
		c, e := d.dial(ctx, "udp4", net.JoinHostPort(p.Dst.String(), strconv.Itoa(int(p.DP))))
		cancel()
		if e != nil {
			return true
		}
		u = &udpFlow{conn: c, identity: o, last: time.Now(), src: p.Src, dst: p.Dst, sp: p.SP, dp: p.DP}
		d.mu.Lock()
		if d.ctx.Err() != nil {
			d.mu.Unlock()
			c.Close()
			return true
		}
		d.udp[p.flow] = u
		d.mu.Unlock()
		go d.readUDP(p.flow, u)
	}
	d.mu.Lock()
	u.last = time.Now()
	d.mu.Unlock()
	u.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	_, _ = u.conn.Write(p.data[p.header+8:])
	return true
}
func (d *Device) readUDP(f flow, u *udpFlow) {
	defer func() {
		u.conn.Close()
		d.mu.Lock()
		if d.udp[f] == u {
			delete(d.udp, f)
		}
		d.mu.Unlock()
	}()
	b := make([]byte, 65507)
	for {
		u.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, e := u.conn.Read(b)
		if e != nil {
			var ne net.Error
			if errors.As(e, &ne) && ne.Timeout() {
				d.mu.Lock()
				idle := time.Since(u.last) > 30*time.Second
				d.mu.Unlock()
				if !idle && d.ctx.Err() == nil {
					continue
				}
			}
			return
		}
		d.mu.Lock()
		valid := d.udp[f] == u
		u.last = time.Now()
		d.mu.Unlock()
		if !valid {
			return
		}
		if u.identity.PID != 0 {
			o, ok := d.owners(f)
			if !ok || o != u.identity {
				return
			}
		}
		for _, pkt := range udpPackets(u, b[:n], d.mtu) {
			if _, e = d.write(pkt); e != nil {
				return
			}
		}
	}
}
func checksum(b []byte) uint16 {
	var n uint32
	for len(b) >= 2 {
		n += uint32(binary.BigEndian.Uint16(b))
		b = b[2:]
	}
	if len(b) > 0 {
		n += uint32(b[0]) << 8
	}
	for n>>16 > 0 {
		n = (n & 65535) + (n >> 16)
	}
	return ^uint16(n)
}

// Preserve complete UDP datagrams; fragment large replies to the configured TUN MTU.
func udpPackets(u *udpFlow, payload []byte, mtu int) [][]byte {
	data := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint16(data, u.dp)
	binary.BigEndian.PutUint16(data[2:], u.sp)
	binary.BigEndian.PutUint16(data[4:], uint16(len(data)))
	copy(data[8:], payload)
	pseudo := make([]byte, 12+len(data))
	src := u.dst.As4()
	dst := u.src.As4()
	copy(pseudo, src[:])
	copy(pseudo[4:], dst[:])
	pseudo[9] = 17
	binary.BigEndian.PutUint16(pseudo[10:], uint16(len(data)))
	copy(pseudo[12:], data)
	c := checksum(pseudo)
	if c == 0 {
		c = 65535
	}
	binary.BigEndian.PutUint16(data[6:], c)
	step := (mtu - 20) &^ 7
	var out [][]byte
	id := uint16(fragmentID.Add(1))
	for off := 0; off < len(data); off += step {
		n := min(step, len(data)-off)
		b := make([]byte, 20+n)
		b[0] = 0x45
		binary.BigEndian.PutUint16(b[2:], uint16(len(b)))
		binary.BigEndian.PutUint16(b[4:], id)
		flags := uint16(off / 8)
		if off+n < len(data) {
			flags |= 0x2000
		}
		binary.BigEndian.PutUint16(b[6:], flags)
		b[8] = 64
		b[9] = 17
		copy(b[12:], src[:])
		copy(b[16:], dst[:])
		binary.BigEndian.PutUint16(b[10:], checksum(b[:20]))
		copy(b[20:], data[off:off+n])
		out = append(out, b)
	}
	return out
}

var fragmentID atomic.Uint32
