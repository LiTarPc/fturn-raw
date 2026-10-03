package bypass

import (
	"encoding/binary"
	"fturnraw/bypassrules"
	"github.com/miekg/dns"
	"net/netip"
	"testing"
)

func dnsPacket(t *testing.T, question string, answers []dns.RR) []byte {
	t.Helper()
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(question), dns.TypeA)
	m.Response = true
	m.Answer = answers
	data, e := m.Pack()
	if e != nil {
		t.Fatal(e)
	}
	u := &udpFlow{src: netip.MustParseAddr("10.77.0.2"), dst: netip.MustParseAddr("1.1.1.1"), sp: 40000, dp: 53}
	return udpPackets(u, data, 1420)[0]
}
func rr(t *testing.T, s string) dns.RR {
	t.Helper()
	v, e := dns.NewRR(s)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestDNSLearnsOnlySelectedChain(t *testing.T) {
	p := newPolicy(bypassrules.Config{Settings: bypassrules.Settings{Sites: []string{"example.org"}}})
	p.observe(dnsPacket(t, "sub.example.org", []dns.RR{rr(t, "sub.example.org. 60 IN CNAME edge.example.net."), rr(t, "edge.example.net. 60 IN A 203.0.113.9"), rr(t, "unrelated.example.net. 60 IN A 203.0.113.10")}))
	if !p.site(netip.MustParseAddr("203.0.113.9")) || p.site(netip.MustParseAddr("203.0.113.10")) {
		t.Fatal("CNAME chain or unrelated A validation failed")
	}
	p.observe(dnsPacket(t, "other.org", []dns.RR{rr(t, "other.org. 60 IN A 203.0.113.11")}))
	if p.site(netip.MustParseAddr("203.0.113.11")) {
		t.Fatal("unselected domain learned")
	}
	p.observe(dnsPacket(t, "example.org", []dns.RR{rr(t, "example.org. 0 IN A 203.0.113.12")}))
	if p.site(netip.MustParseAddr("203.0.113.12")) {
		t.Fatal("zero TTL retained")
	}
	if p.domain("badexample.org") || !p.domain("sub.EXAMPLE.org.") {
		t.Fatal("domain suffix matching")
	}
}
func TestPrivacyAndFragmentsStayRaw(t *testing.T) {
	for _, port := range []uint16{53, 853} {
		if !protected(flow{DP: port, Proto: 6}) || !protected(flow{DP: port, Proto: 17}) {
			t.Fatal("DNS bypass allowed")
		}
	}
	u := &udpFlow{src: netip.MustParseAddr("203.0.113.9"), dst: netip.MustParseAddr("10.77.0.2"), sp: 443, dp: 45000}
	b := udpPackets(u, []byte("udp"), 1420)[0]
	binary.BigEndian.PutUint16(b[6:], 0x2000)
	if _, ok := parse(b); ok {
		t.Fatal("fragment partially bypassed")
	}
	b[0] = 0x65
	if _, ok := parse(b); ok {
		t.Fatal("IPv6 accepted")
	}
}
func TestRUAndCIDRBoundary(t *testing.T) {
	p := newPolicy(bypassrules.Config{Settings: bypassrules.Settings{Sites: []string{"203.0.113.0/24"}}, CIDRs: []string{"198.51.100.0/24"}})
	for _, v := range []string{"203.0.113.0", "203.0.113.255", "198.51.100.3"} {
		if !p.site(netip.MustParseAddr(v)) {
			t.Fatal("CIDR missed", v)
		}
	}
	if p.site(netip.MustParseAddr("203.0.114.0")) {
		t.Fatal("CIDR boundary escaped")
	}
}

func TestTCPDNSReorderingAndRetransmit(t *testing.T) {
	p := newPolicy(bypassrules.Config{Settings: bypassrules.Settings{Sites: []string{"example.org"}}})
	udp := dnsPacket(t, "example.org", []dns.RR{rr(t, "example.org. 60 IN A 203.0.113.19")})
	msg := udp[28:]
	frame := make([]byte, 2+len(msg))
	binary.BigEndian.PutUint16(frame, uint16(len(msg)))
	copy(frame[2:], msg)
	send := func(seq uint32, flags byte, payload []byte) {
		b := make([]byte, 40+len(payload))
		b[0] = 0x45
		binary.BigEndian.PutUint16(b[2:], uint16(len(b)))
		b[9] = 6
		copy(b[12:], []byte{1, 1, 1, 1})
		copy(b[16:], []byte{10, 77, 0, 2})
		binary.BigEndian.PutUint16(b[20:], 53)
		binary.BigEndian.PutUint16(b[22:], 40000)
		binary.BigEndian.PutUint32(b[24:], seq)
		b[32] = 0x50
		b[33] = flags
		copy(b[40:], payload)
		p.observe(b)
	}
	send(100, 0x12, nil)
	send(111, 0x10, frame[10:])
	send(101, 0x10, frame[:10])
	send(101, 0x10, frame[:10])
	if !p.site(netip.MustParseAddr("203.0.113.19")) {
		t.Fatal("reordered TCP DNS not learned")
	}
}
