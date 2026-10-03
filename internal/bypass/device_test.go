package bypass

import (
	"bytes"
	"context"
	"encoding/binary"
	"fturnraw/bypassrules"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"
)

type fakeDevice struct {
	in, out chan []byte
	done    chan struct{}
	once    sync.Once
}

func newFake() *fakeDevice {
	return &fakeDevice{in: make(chan []byte, 512), out: make(chan []byte, 512), done: make(chan struct{})}
}
func (f *fakeDevice) Read(b []byte) (int, error) {
	select {
	case p := <-f.in:
		return copy(b, p), nil
	case <-f.done:
		return 0, net.ErrClosed
	}
}
func (f *fakeDevice) Write(b []byte) (int, error) {
	select {
	case f.out <- append([]byte{}, b...):
		return len(b), nil
	case <-f.done:
		return 0, net.ErrClosed
	}
}
func (f *fakeDevice) Close() error { f.once.Do(func() { close(f.done) }); return nil }
func (f *fakeDevice) Name() string { return "fake" }
func testDevice(t *testing.T, c bypassrules.Config) (*Device, *fakeDevice) {
	t.Helper()
	f := newFake()
	w, e := Wrap(context.Background(), f, c, netip.MustParseAddr("10.77.0.2"), 1420, nil)
	if e != nil {
		t.Fatal(e)
	}
	d := w.(*Device)
	t.Cleanup(func() { d.Close() })
	return d, f
}
func selectedConfig() bypassrules.Config {
	return bypassrules.Config{Version: 1, Settings: bypassrules.Settings{Enabled: true, Sites: []string{"203.0.113.9"}}}
}
func TestTCPPacketForwardAndHalfClose(t *testing.T) {
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	go func() {
		c, e := l.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		b, _ := io.ReadAll(c)
		c.Write(append([]byte("echo:"), b...))
	}()
	d, f := testDevice(t, selectedConfig())
	d.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp4" || address != "203.0.113.9:443" {
			t.Errorf("wrong destination %s %s", network, address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, l.Addr().String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	link := channel.New(512, 1420, "")
	s := stack.New(stack.Options{NetworkProtocols: []stack.NetworkProtocolFactory{ipv4.NewProtocol}, TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol}})
	if err := s.CreateNIC(1, link); err != nil {
		t.Fatal(err)
	}
	if err := s.AddProtocolAddress(1, tcpip.ProtocolAddress{Protocol: ipv4.ProtocolNumber, AddressWithPrefix: tcpip.AddrFrom4([4]byte{10, 77, 0, 2}).WithPrefix()}, stack.AddressProperties{}); err != nil {
		t.Fatal(err)
	}
	s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}})
	defer func() { link.Close(); s.Close(); s.Wait() }()
	go func() {
		for {
			p := link.ReadContext(ctx)
			if p == nil {
				return
			}
			var b []byte
			for _, v := range p.AsSlices() {
				b = append(b, v...)
			}
			p.DecRef()
			select {
			case f.in <- b:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		for {
			select {
			case b := <-f.out:
				p := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(b)})
				link.InjectInbound(ipv4.ProtocolNumber, p)
				p.DecRef()
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		b := make([]byte, 1501)
		for {
			n, e := d.Read(b)
			if e != nil {
				return
			}
			t.Errorf("selected TCP packet escaped into Raw: %d", n)
		}
	}()
	c, e := gonet.DialContextTCP(ctx, s, tcpip.FullAddress{Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 9}), Port: 443}, ipv4.ProtocolNumber)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(6 * time.Second))
	payload := bytes.Repeat([]byte("abc"), 15000)
	if _, e = c.Write(payload); e != nil {
		t.Fatal(e)
	}
	c.CloseWrite()
	reply, e := io.ReadAll(c)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(reply, append([]byte("echo:"), payload...)) {
		t.Fatalf("TCP corruption: received %d", len(reply))
	}
}
func TestUDPForwardPreservesDatagrams(t *testing.T) {
	l, e := net.ListenPacket("udp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	go func() {
		b := make([]byte, 65507)
		n, a, e := l.ReadFrom(b)
		if e == nil {
			l.WriteTo(b[:n], a)
		}
	}()
	d, f := testDevice(t, selectedConfig())
	d.dial = func(ctx context.Context, n, a string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, n, l.LocalAddr().String())
	}
	u := &udpFlow{src: netip.MustParseAddr("203.0.113.9"), dst: netip.MustParseAddr("10.77.0.2"), sp: 443, dp: 45000}
	data := bytes.Repeat([]byte("udp"), 300)
	packet := udpPackets(u, data, 1420)[0]
	p, ok := parse(packet)
	if !ok || !d.directUDP(p) {
		t.Fatal("not handled")
	}
	select {
	case reply := <-f.out:
		q, ok := parse(reply)
		if !ok || q.Src != p.Dst || q.Dst != p.Src || !bytes.Equal(reply[28:], data) {
			t.Fatal("invalid UDP reply")
		}
		if checksum(reply[:20]) != 0 {
			t.Fatal("bad IP checksum")
		}
		pseudo := make([]byte, 12+len(reply)-20)
		copy(pseudo, reply[12:20])
		pseudo[9] = 17
		binary.BigEndian.PutUint16(pseudo[10:], uint16(len(reply)-20))
		copy(pseudo[12:], reply[20:])
		if checksum(pseudo) != 0 {
			t.Fatal("bad UDP checksum")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("UDP timeout")
	}
}
func TestApplicationsArePerFlowAndUnknownIsRaw(t *testing.T) {
	d, _ := testDevice(t, bypassrules.Config{Version: 1, Settings: bypassrules.Settings{Enabled: true, Apps: []string{"game.exe"}}})
	d.owners = func(f flow) (owner, bool) {
		if f.SP == 40000 {
			return owner{1, `C:\Games\game.exe`}, true
		}
		return owner{2, `C:\Apps\browser.exe`}, true
	}
	f := flow{Src: netip.MustParseAddr("10.77.0.2"), Dst: netip.MustParseAddr("203.0.113.9"), SP: 40000, DP: 443, Proto: 6}
	if yes, _ := d.selected(f); !yes {
		t.Fatal("game not selected")
	}
	f.SP++
	if yes, _ := d.selected(f); yes {
		t.Fatal("another process sharing destination was bypassed")
	}
	d.owners = func(flow) (owner, bool) { return owner{}, false }
	if yes, _ := d.selected(f); yes {
		t.Fatal("unknown process bypassed")
	}
}
func TestTCPPortReuseAndExistingConnections(t *testing.T) {
	d, _ := testDevice(t, selectedConfig())
	p := packet{flow: flow{Dst: netip.MustParseAddr("203.0.113.9"), SP: 40000, DP: 443, Proto: 6}, syn: false, header: 20, data: make([]byte, 40)}
	if d.directTCP(p) {
		t.Fatal("midstream must remain Raw")
	}
	p.syn = true
	binary.BigEndian.PutUint32(p.data[24:], 123)
	if !d.directTCP(p) {
		t.Fatal("new SYN not selected")
	}
	binary.BigEndian.PutUint32(p.data[24:], 124)
	if d.directTCP(p) {
		t.Fatal("reused active tuple must not inherit bypass")
	}
}
func TestUDPPortReuseClosesOldFlow(t *testing.T) {
	d, _ := testDevice(t, bypassrules.Config{Version: 1, Settings: bypassrules.Settings{Enabled: true, Apps: []string{"game.exe"}}})
	f := flow{Dst: netip.MustParseAddr("203.0.113.9"), SP: 40000, DP: 443, Proto: 17}
	a, b := net.Pipe()
	defer b.Close()
	d.udp[f] = &udpFlow{conn: a, identity: owner{1, `C:\game.exe`}}
	d.owners = func(flow) (owner, bool) { return owner{2, `C:\browser.exe`}, true }
	if d.directUDP(packet{flow: f}) {
		t.Fatal("port reused by browser inherited direct flow")
	}
	if len(d.udp) != 0 {
		t.Fatal("old flow retained")
	}
}
func TestUDPReplyFragmentation(t *testing.T) {
	u := &udpFlow{src: netip.MustParseAddr("10.77.0.2"), dst: netip.MustParseAddr("203.0.113.9"), sp: 40000, dp: 443}
	payload := bytes.Repeat([]byte("x"), 60000)
	packets := udpPackets(u, payload, 1420)
	var data []byte
	for _, p := range packets {
		if len(p) > 1420 || checksum(p[:20]) != 0 {
			t.Fatal("invalid fragment")
		}
		offset := int(binary.BigEndian.Uint16(p[6:8])&0x1fff) * 8
		if offset != len(data) {
			t.Fatal("bad fragment offset")
		}
		data = append(data, p[20:]...)
	}
	if !bytes.Equal(data[8:], payload) {
		t.Fatal("fragment corruption")
	}
}

func TestReadPrivacySourceAndFragmentFences(t *testing.T) {
	d, f := testDevice(t, selectedConfig())
	for _, tc := range []struct {
		src  string
		port uint16
		frag bool
	}{{"10.77.0.2", 53, false}, {"10.77.0.2", 853, false}, {"10.77.0.3", 443, false}, {"10.77.0.2", 443, true}} {
		u := &udpFlow{src: netip.MustParseAddr("203.0.113.9"), dst: netip.MustParseAddr(tc.src), sp: tc.port, dp: 45000}
		b := udpPackets(u, []byte("probe"), 1420)[0]
		if tc.frag {
			binary.BigEndian.PutUint16(b[6:], 0x2000)
		}
		f.in <- b
		buf := make([]byte, 1501)
		n, e := d.Read(buf)
		if e != nil || !bytes.Equal(b, buf[:n]) {
			t.Fatal("privacy/source/fragment fence failed")
		}
	}
}
func TestDisabledDoesNotWrapOrCloseDevice(t *testing.T) {
	f := newFake()
	defer f.Close()
	w, e := Wrap(context.Background(), f, bypassrules.Config{}, netip.Addr{}, 0, nil)
	if e != nil || w != f {
		t.Fatal("disabled bypass altered device")
	}
}
