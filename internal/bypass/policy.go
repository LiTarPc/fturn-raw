package bypass

import (
	"encoding/binary"
	"fturnraw/bypassrules"

	"net/netip"
	"strings"
	"sync"
	"time"
)

type flow struct {
	Src, Dst netip.Addr
	SP, DP   uint16
	Proto    byte
}
type packet struct {
	flow
	header int
	syn    bool
	data   []byte
}

func parse(b []byte) (packet, bool) {
	if len(b) < 20 || b[0]>>4 != 4 {
		return packet{}, false
	}
	h := int(b[0]&15) * 4
	n := int(binary.BigEndian.Uint16(b[2:4]))
	// Fragmented packets remain in Raw as one path; never split fragment chains.
	if h < 20 || n < h || n > len(b) || binary.BigEndian.Uint16(b[6:8])&0x3fff != 0 {
		return packet{}, false
	}
	p := packet{flow: flow{Src: netip.AddrFrom4([4]byte(b[12:16])), Dst: netip.AddrFrom4([4]byte(b[16:20])), Proto: b[9]}, header: h, data: b[:n]}
	if p.Proto != 6 && p.Proto != 17 {
		return packet{}, false
	}
	need := 8
	if p.Proto == 6 {
		need = 20
	}
	if n < h+need {
		return packet{}, false
	}
	p.SP = binary.BigEndian.Uint16(b[h : h+2])
	p.DP = binary.BigEndian.Uint16(b[h+2 : h+4])
	if p.Proto == 6 {
		th := int(b[h+12]>>4) * 4
		if th < 20 || h+th > n {
			return packet{}, false
		}
		p.syn = b[h+13]&0x12 == 2
	} else if int(binary.BigEndian.Uint16(b[h+4:h+6])) != n-h {
		return packet{}, false
	}
	return p, true
}

type owner struct {
	PID  uint32
	Path string
}
type ownerLookup func(flow) (owner, bool)
type policy struct {
	nets       [33]map[uint32]bool
	domains    []string
	apps       []string
	mu         sync.Mutex
	learned    map[netip.Addr]time.Time
	dnsMu      sync.Mutex
	dnsStreams map[flow]*dnsStream
}

func newPolicy(c bypassrules.Config) *policy {
	p := &policy{apps: c.Apps, learned: map[netip.Addr]time.Time{}}
	for _, v := range append(append([]string{}, c.Sites...), c.CIDRs...) {
		if ip, e := netip.ParseAddr(v); e == nil {
			p.add(netip.PrefixFrom(ip, 32))
		} else if n, e := netip.ParsePrefix(v); e == nil {
			p.add(n)
		} else {
			p.domains = append(p.domains, v)
		}
	}
	return p
}
func ipnum(a netip.Addr) uint32 { v := a.As4(); return binary.BigEndian.Uint32(v[:]) }
func mask(n int) uint32         { return ^uint32(0) << (32 - n) }
func (p *policy) add(n netip.Prefix) {
	b := n.Bits()
	if p.nets[b] == nil {
		p.nets[b] = map[uint32]bool{}
	}
	p.nets[b][ipnum(n.Masked().Addr())] = true
}
func (p *policy) site(ip netip.Addr) bool {
	v := ipnum(ip)
	for n := 1; n <= 32; n++ {
		if p.nets[n][v&mask(n)] {
			return true
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	t, ok := p.learned[ip]
	return ok && time.Now().Before(t)
}
func (p *policy) domain(s string) bool {
	s = strings.ToLower(strings.TrimSuffix(s, "."))
	for _, d := range p.domains {
		if s == d || strings.HasSuffix(s, "."+d) {
			return true
		}
	}
	return false
}
func (p *policy) application(o owner) bool {
	path := strings.ToLower(strings.ReplaceAll(o.Path, "/", `\`))
	base := path
	if i := strings.LastIndexByte(path, '\\'); i >= 0 {
		base = path[i+1:]
	}
	for _, a := range p.apps {
		if a == path || (!strings.Contains(a, `\`) && a == base) {
			return true
		}
	}
	return false
}
func (p *policy) learn(ip netip.Addr, ttl uint32) {
	if !ip.Is4() || ttl == 0 {
		return
	}
	if ttl > 3600 {
		ttl = 3600
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	if len(p.learned) >= 8192 {
		for a, t := range p.learned {
			if !now.Before(t) {
				delete(p.learned, a)
			}
		}
		if len(p.learned) >= 8192 {
			return
		}
	}
	p.learned[ip] = now.Add(time.Duration(ttl) * time.Second)
}

// Standard DNS and DNS-over-TLS always use Raw, including bypassed applications.
func protected(f flow) bool { return f.DP == 53 || f.DP == 853 }
