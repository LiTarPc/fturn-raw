package bypass

// Only the SRS file decoder is reused. No sing-box service or transport handles
// Android TUN packets. Matching happens before the existing native bypass path.
import (
	"bytes"
	"fmt"
	"io"
	"net/netip"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sagernet/sing-box/common/srs"
	"github.com/sagernet/sing-box/option"
)

type destinationRule func(flow, []string) bool

func (p *policy) loadSRS(files []string) error {
	if len(files) > 8 {
		return fmt.Errorf("too many native bypass rule sets")
	}
	for _, path := range files {
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		b, e := io.ReadAll(io.LimitReader(f, (2<<20)+1))
		f.Close()
		if e != nil {
			return e
		}
		if len(b) > 2<<20 {
			return fmt.Errorf("SRS exceeds 2 MiB")
		}
		data, e := srs.Read(bytes.NewReader(b), false)
		if e != nil {
			return fmt.Errorf("native bypass SRS: %w", e)
		}
		if len(data.Options.Rules) > 100000 {
			return fmt.Errorf("too many SRS rules")
		}
		for _, r := range data.Options.Rules {
			match, e := compileRule(r, 0)
			if e != nil {
				return e
			}
			p.rules = append(p.rules, match)
		}
	}
	return nil
}
func compileRule(r option.HeadlessRule, depth int) (destinationRule, error) {
	if depth > 32 {
		return nil, fmt.Errorf("SRS logical rule nesting exceeds 32")
	}
	if r.Type == "logical" {
		l := r.LogicalOptions
		if (l.Mode != "and" && l.Mode != "or") || len(l.Rules) == 0 {
			return nil, fmt.Errorf("invalid SRS logical rule")
		}
		var children []destinationRule
		for _, sub := range l.Rules {
			m, e := compileRule(sub, depth+1)
			if e != nil {
				return nil, e
			}
			children = append(children, m)
		}
		return func(f flow, n []string) bool {
			v := l.Mode == "and"
			for _, m := range children {
				if l.Mode == "and" {
					v = v && m(f, n)
				} else {
					v = v || m(f, n)
				}
			}
			return v != l.Invert
		}, nil
	}
	o := r.DefaultOptions
	// These fields need OS metadata unavailable to the native IPv4 packet path.
	// Refuse them explicitly rather than broaden an imported bypass rule.
	if len(o.QueryType)+len(o.ProcessName)+len(o.ProcessPath)+len(o.ProcessPathRegex)+len(o.PackageName)+len(o.WIFISSID)+len(o.WIFIBSSID)+len(o.NetworkType) > 0 || o.NetworkIsExpensive || o.NetworkIsConstrained {
		return nil, fmt.Errorf("RAW native SRS supports destination/domain, IP, port and network rules; this file requires OS/DNS metadata")
	}
	var regexes []*regexp.Regexp
	for _, s := range o.DomainRegex {
		v, e := regexp.Compile(s)
		if e != nil {
			return nil, e
		}
		regexes = append(regexes, v)
	}
	srcRanges, e := portRanges(o.SourcePortRange)
	if e != nil {
		return nil, e
	}
	dstRanges, e := portRanges(o.PortRange)
	if e != nil {
		return nil, e
	}
	hasDomains := o.DomainMatcher != nil || o.AdGuardDomainMatcher != nil || len(o.Domain)+len(o.DomainSuffix)+len(o.DomainKeyword)+len(regexes) > 0
	hasAddress := hasDomains || o.IPSet != nil
	return func(f flow, names []string) bool {
		matched := true
		if len(o.Network) > 0 {
			ok := false
			for _, n := range o.Network {
				ok = ok || (n == "tcp" && f.Proto == 6) || (n == "udp" && f.Proto == 17)
			}
			matched = matched && ok
		}
		if o.SourceIPSet != nil {
			matched = matched && o.SourceIPSet.Contains(f.Src)
		}
		matched = matched && portMatch(f.SP, o.SourcePort, srcRanges) && portMatch(f.DP, o.Port, dstRanges)
		if hasAddress {
			ok := o.IPSet != nil && o.IPSet.Contains(f.Dst)
			if hasDomains {
				for _, name := range names {
					if o.DomainMatcher != nil {
						ok = ok || o.DomainMatcher.Match(name)
					}
					if o.AdGuardDomainMatcher != nil {
						ok = ok || o.AdGuardDomainMatcher.Match(name)
					}
					for _, v := range o.Domain {
						ok = ok || name == v
					}
					for _, v := range o.DomainSuffix {
						ok = ok || name == v || strings.HasSuffix(name, "."+strings.TrimPrefix(v, "."))
					}
					for _, v := range o.DomainKeyword {
						ok = ok || strings.Contains(name, v)
					}
					for _, v := range regexes {
						ok = ok || v.MatchString(name)
					}
				}
			}
			matched = matched && ok
		}
		return matched != o.Invert
	}, nil
}
func portRanges(values []string) ([][2]uint16, error) {
	var out [][2]uint16
	for _, v := range values {
		a, b, ok := strings.Cut(v, ":")
		if !ok {
			return nil, fmt.Errorf("invalid SRS port range")
		}
		lo, hi := uint64(0), uint64(65535)
		var e error
		if a != "" {
			lo, e = strconv.ParseUint(a, 10, 16)
			if e != nil {
				return nil, e
			}
		}
		if b != "" {
			hi, e = strconv.ParseUint(b, 10, 16)
			if e != nil {
				return nil, e
			}
		}
		if lo > hi {
			return nil, fmt.Errorf("inverted SRS port range")
		}
		out = append(out, [2]uint16{uint16(lo), uint16(hi)})
	}
	return out, nil
}
func portMatch(port uint16, values []uint16, ranges [][2]uint16) bool {
	if len(values)+len(ranges) == 0 {
		return true
	}
	for _, v := range values {
		if port == v {
			return true
		}
	}
	for _, r := range ranges {
		if port >= r[0] && port <= r[1] {
			return true
		}
	}
	return false
}
func (p *policy) learnName(ip netip.Addr, name string, ttl uint32) {
	if len(p.rules) == 0 || ttl == 0 || !ip.Is4() {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.names == nil {
		p.names = map[netip.Addr]map[string]time.Time{}
	}
	now := time.Now()
	if len(p.names) >= 8192 {
		for a, n := range p.names {
			for s, t := range n {
				if !now.Before(t) {
					delete(n, s)
				}
			}
			if len(n) == 0 {
				delete(p.names, a)
			}
		}
		if len(p.names) >= 8192 {
			return
		}
	}
	if p.names[ip] == nil {
		p.names[ip] = map[string]time.Time{}
	}
	if len(p.names[ip]) < 32 {
		p.names[ip][strings.ToLower(strings.TrimSuffix(name, "."))] = now.Add(time.Duration(min(ttl, 3600)) * time.Second)
	}
}
func (p *policy) matchRules(f flow) bool {
	if len(p.rules) == 0 {
		return false
	}
	p.mu.Lock()
	var names []string
	now := time.Now()
	for n, t := range p.names[f.Dst] {
		if now.Before(t) {
			names = append(names, n)
		}
	}
	p.mu.Unlock()
	for _, r := range p.rules {
		if r(f, names) {
			return true
		}
	}
	return false
}
