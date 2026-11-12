package bypass

import (
	"bytes"
	"fturnraw/bypassrules"
	"github.com/miekg/dns"
	"github.com/sagernet/sing-box/common/srs"
	"github.com/sagernet/sing-box/option"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeSRSKeepsDomainNetworkAndPortConditions(t *testing.T) {
	r := option.HeadlessRule{Type: "default", DefaultOptions: option.DefaultHeadlessRule{
		DomainSuffix: []string{"example.com"}, Network: []string{"tcp"}, Port: []uint16{443},
	}}
	var b bytes.Buffer
	if e := srs.Write(&b, option.PlainRuleSet{Rules: []option.HeadlessRule{r}}, 3); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "rules.srs")
	if e := os.WriteFile(path, b.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	p := newPolicy(bypassrules.Config{})
	if e := p.loadSRS([]string{path}); e != nil {
		t.Fatal(e)
	}
	ip := netip.MustParseAddr("203.0.113.9")
	f := flow{Src: netip.MustParseAddr("10.77.0.2"), Dst: ip, SP: 12345, DP: 443, Proto: 6}
	if p.matchRules(f) {
		t.Fatal("domain matched without a DNS association")
	}
	m := new(dns.Msg)
	m.SetQuestion("www.example.com.", dns.TypeA)
	m.Response = true
	m.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: "www.example.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP(ip.String())}}
	wire, e := m.Pack()
	if e != nil {
		t.Fatal(e)
	}
	p.observeMessage(wire)
	if !p.matchRules(f) {
		t.Fatal("valid DNS-associated TCP/443 rule did not match")
	}
	f.DP = 80
	if p.matchRules(f) {
		t.Fatal("SRS port condition was broadened")
	}
	f.DP = 443
	f.Proto = 17
	if p.matchRules(f) {
		t.Fatal("SRS TCP condition was broadened to UDP")
	}
	p.mu.Lock()
	p.names[ip]["www.example.com"] = time.Now().Add(-time.Second)
	p.mu.Unlock()
	f.Proto = 6
	if p.matchRules(f) {
		t.Fatal("expired DNS association still bypassed")
	}
}
func TestNativeBypassDefaultRouteCIDR(t *testing.T) {
	p := newPolicy(bypassrules.Config{CIDRs: []string{"0.0.0.0/0"}})
	if !p.site(netip.MustParseAddr("203.0.113.4")) {
		t.Fatal("default route bypass was lost")
	}
}
