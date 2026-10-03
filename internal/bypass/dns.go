package bypass

import (
	"encoding/binary"
	"github.com/miekg/dns"
	"net/netip"
	"strings"
	"time"
)

type dnsStream struct {
	next    uint32
	data    []byte
	pending map[uint32][]byte
	last    time.Time
	bytes   int
}

func (p *policy) observe(b []byte) {
	pkt, ok := parse(b)
	if !ok || pkt.SP != 53 {
		return
	}
	if pkt.Proto == 17 {
		p.observeMessage(pkt.data[pkt.header+8:])
		return
	}
	h := pkt.header
	seq := binary.BigEndian.Uint32(pkt.data[h+4:])
	flags := pkt.data[h+13]
	payload := pkt.data[h+int(pkt.data[h+12]>>4)*4:]
	p.dnsMu.Lock()
	defer p.dnsMu.Unlock()
	now := time.Now()
	if p.dnsStreams == nil {
		p.dnsStreams = map[flow]*dnsStream{}
	}
	for f, s := range p.dnsStreams {
		if now.Sub(s.last) > 30*time.Second {
			delete(p.dnsStreams, f)
		}
	}
	s := p.dnsStreams[pkt.flow]
	if flags&2 != 0 {
		seq++
		s = nil
		delete(p.dnsStreams, pkt.flow)
	}
	if s == nil {
		if len(p.dnsStreams) >= 128 {
			return
		}
		s = &dnsStream{next: seq, pending: map[uint32][]byte{}}
		p.dnsStreams[pkt.flow] = s
	}
	s.last = now
	if flags&4 != 0 {
		delete(p.dnsStreams, pkt.flow)
		return
	}
	if len(payload) == 0 {
		return
	}
	delta := int32(seq - s.next)
	if delta < 0 {
		skip := int(-delta)
		if skip >= len(payload) {
			return
		}
		payload = payload[skip:]
		seq = s.next
		delta = 0
	}
	if delta > 0 {
		if delta > 65536 || len(s.pending) >= 16 || s.bytes+len(payload) > 65536 {
			delete(p.dnsStreams, pkt.flow)
			return
		}
		if _, ok := s.pending[seq]; !ok {
			s.pending[seq] = append([]byte{}, payload...)
			s.bytes += len(payload)
		}
		return
	}
	s.data = append(s.data, payload...)
	s.next += uint32(len(payload))
	for {
		v, ok := s.pending[s.next]
		if !ok {
			break
		}
		delete(s.pending, s.next)
		s.bytes -= len(v)
		s.data = append(s.data, v...)
		s.next += uint32(len(v))
	}
	if len(s.data) > 65537 {
		delete(p.dnsStreams, pkt.flow)
		return
	}
	for len(s.data) >= 2 {
		n := int(binary.BigEndian.Uint16(s.data))
		if n == 0 {
			delete(p.dnsStreams, pkt.flow)
			return
		}
		if len(s.data) < n+2 {
			break
		}
		p.observeMessage(s.data[2 : n+2])
		s.data = s.data[n+2:]
	}
	if flags&1 != 0 {
		delete(p.dnsStreams, pkt.flow)
	}
}
func (p *policy) observeMessage(data []byte) {
	var m dns.Msg
	if m.Unpack(data) != nil || !m.Response || m.Rcode != dns.RcodeSuccess || len(m.Question) != 1 || m.Question[0].Qtype != dns.TypeA || !p.domain(m.Question[0].Name) {
		return
	}
	names := map[string]uint32{strings.ToLower(m.Question[0].Name): 3600}
	for i := 0; i < 16; i++ {
		changed := false
		for _, a := range m.Answer {
			if c, ok := a.(*dns.CNAME); ok {
				if ttl, yes := names[strings.ToLower(c.Hdr.Name)]; yes {
					target := strings.ToLower(c.Target)
					if _, yes = names[target]; !yes {
						names[target] = min(ttl, c.Hdr.Ttl)
						changed = true
					}
				}
			}
		}
		if !changed {
			break
		}
	}
	for _, a := range m.Answer {
		if a, ok := a.(*dns.A); ok {
			if ttl, yes := names[strings.ToLower(a.Hdr.Name)]; yes {
				ip, ok := netip.AddrFromSlice(a.A.To4())
				if ok {
					p.learn(ip, min(ttl, a.Hdr.Ttl))
				}
			}
		}
	}
}
