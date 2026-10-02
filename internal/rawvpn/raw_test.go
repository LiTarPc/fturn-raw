package rawvpn

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/turn/v5"
	"github.com/samosvalishe/free-turn-proxy/internal/provider"
)

type testDevice struct {
	input, output chan []byte
	done          chan struct{}
	once          sync.Once
}

func newTestDevice() *testDevice {
	return &testDevice{input: make(chan []byte, 256), output: make(chan []byte, 256), done: make(chan struct{})}
}
func (d *testDevice) Read(b []byte) (int, error) {
	select {
	case <-d.done:
		return 0, net.ErrClosed
	case p := <-d.input:
		return copy(b, p), nil
	}
}
func (d *testDevice) Write(b []byte) (int, error) {
	select {
	case <-d.done:
		return 0, net.ErrClosed
	case d.output <- append([]byte(nil), b...):
		return len(b), nil
	}
}
func (d *testDevice) Close() error { d.once.Do(func() { close(d.done) }); return nil }
func (*testDevice) Name() string   { return "test-tun" }

func ipPacket(src, dst string, size int) []byte {
	p := make([]byte, size)
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:4], uint16(size))
	p[8], p[9] = 64, 17
	a, b := netip.MustParseAddr(src).As4(), netip.MustParseAddr(dst).As4()
	copy(p[12:16], a[:])
	copy(p[16:20], b[:])
	for i := 20; i < len(p); i++ {
		p[i] = byte(i)
	}
	return p
}

func readPacket(t *testing.T, d *testDevice, want []byte) {
	t.Helper()
	select {
	case got := <-d.output:
		if !bytes.Equal(got, want) {
			t.Fatal("IP packet changed in transit")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("IP packet was not delivered")
	}
}

func TestIPv4LengthValidation(t *testing.T) {
	good := ipPacket("10.77.0.2", "1.1.1.1", 1280)
	if _, _, err := ipv4(good, 1280); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func([]byte) []byte{
		func(p []byte) []byte { return p[:19] },
		func(p []byte) []byte { p[0] = 0x65; return p },
		func(p []byte) []byte { p[0] = 0x44; return p },
		func(p []byte) []byte { binary.BigEndian.PutUint16(p[2:4], 1279); return p },
		func(p []byte) []byte { return append(p, 0) },
	} {
		if _, _, err := ipv4(mutate(append([]byte(nil), good...)), 1280); err == nil {
			t.Fatal("malformed IPv4 accepted")
		}
	}
}

func udpSocket(t *testing.T) net.PacketConn {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	return pc
}

func startRawTestServer(t *testing.T, pc net.PacketConn, key []byte, mtus ...int) (*testDevice, context.CancelFunc, <-chan error) {
	t.Helper()
	mtu := 1280
	if len(mtus) > 0 {
		mtu = mtus[0]
	}
	dev := newTestDevice()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunServer(ctx, pc, dev, ServerConfig{ClientAddress: netip.MustParseAddr("10.77.0.2"), MTU: mtu, Profile: "rtpopus2", Key: key})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("server shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
	})
	return dev, cancel, done
}

func TestRawServerRejectsSpoofingAndReplays(t *testing.T) {
	key := testKey(t)
	serverPC := udpSocket(t)
	serverDev, _, _ := startRawTestServer(t, serverPC, key)
	clientPC := udpSocket(t)
	peer := serverPC.LocalAddr()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	codec, _, err := clientHandshake(ctx, clientPC, peer, ClientConfig{Address: netip.MustParseAddr("10.77.0.2"), MTU: 1280, Profile: "rtpopus2", Key: key})
	if err != nil {
		t.Fatal(err)
	}
	wireBuf := make([]byte, maxWire)
	spoof := append([]byte{frameData}, ipPacket("10.77.0.99", "1.1.1.1", 64)...)
	if err := writeFrame(clientPC, peer, codec, wireBuf, spoof); err != nil {
		t.Fatal(err)
	}
	good := ipPacket("10.77.0.2", "1.1.1.1", 1280)
	n, err := codec.encode(wireBuf, append([]byte{frameData}, good...))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clientPC.WriteTo(wireBuf[:n], peer); err != nil {
		t.Fatal(err)
	}
	readPacket(t, serverDev, good)
	if _, err := clientPC.WriteTo(wireBuf[:n], peer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-serverDev.output:
		t.Fatal("spoofed or replayed packet reached TUN")
	case <-time.After(150 * time.Millisecond):
	}
}

type dropReplies struct {
	net.PacketConn
	count atomic.Int32
}

func (p *dropReplies) WriteTo(b []byte, addr net.Addr) (int, error) {
	n := p.count.Add(1)
	if n == 1 || n == 3 {
		return len(b), nil
	} // first challenge, then first ready
	return p.PacketConn.WriteTo(b, addr)
}
func TestEncryptedHandshakeSurvivesLostReplies(t *testing.T) {
	key := testKey(t)
	pc := &dropReplies{PacketConn: udpSocket(t)}
	_, _, _ = startRawTestServer(t, pc, key)
	cli := udpSocket(t)
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	_, mtu, err := clientHandshake(ctx, cli, pc.LocalAddr(), ClientConfig{Address: netip.MustParseAddr("10.77.0.2"), MTU: 1280, Profile: "rtpopus2", Key: key})
	if err != nil || mtu != 1280 {
		t.Fatalf("handshake after packet loss: mtu=%d err=%v", mtu, err)
	}
}

type staticProvider struct{ addr string }

func (p staticProvider) GetCredentials(context.Context, int) (provider.Credentials, error) {
	return provider.Credentials{User: "raw-user", Pass: "raw-pass", ServerAddrs: []string{p.addr}}, nil
}
func (staticProvider) IsAuthError(error) bool   { return false }
func (staticProvider) HandleAuthError(int) bool { return false }
func (staticProvider) ResetErrors(int)          {}
func (staticProvider) DropCredentials(int)      {}
func (staticProvider) BackoffUntilUnix() int64  { return 0 }
func (staticProvider) Name() string             { return "local-turn" }

func startTURN(t *testing.T, udp bool) string {
	t.Helper()
	const realm = "raw-test"
	key := turn.GenerateAuthKey("raw-user", realm, "raw-pass")
	cfg := turn.ServerConfig{Realm: realm, AuthHandler: func(r *turn.RequestAttributes) (string, []byte, bool) {
		return r.Username, key, r.Username == "raw-user"
	}}
	generator := &turn.RelayAddressGeneratorStatic{RelayAddress: net.ParseIP("127.0.0.1"), Address: "127.0.0.1"}
	var addr string
	if udp {
		pc := udpSocket(t)
		addr = pc.LocalAddr().String()
		cfg.PacketConnConfigs = []turn.PacketConnConfig{{PacketConn: pc, RelayAddressGenerator: generator}}
	} else {
		ln, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ln.Close() })
		addr = ln.Addr().String()
		cfg.ListenerConfigs = []turn.ListenerConfig{{Listener: ln, RelayAddressGenerator: generator}}
	}
	server, err := turn.NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	return addr
}

func TestRawIPThroughRealTURN(t *testing.T) {
	for _, udp := range []bool{true, false} {
		name := "tcp"
		if udp {
			name = "udp"
		}
		t.Run(name, func(t *testing.T) {
			turnAddr := startTURN(t, udp)
			key := testKey(t)
			serverPC := udpSocket(t)
			serverDev, _, _ := startRawTestServer(t, serverPC, key, 1500)
			clientDev := newTestDevice()
			peer, err := net.ResolveUDPAddr("udp", serverPC.LocalAddr().String())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			var connected atomic.Int32
			done := make(chan error, 1)
			go func() {
				done <- RunClient(ctx, clientDev, ClientConfig{Peer: peer, Address: netip.MustParseAddr("10.77.0.2"), MTU: 1500, Profile: "rtpopus2", Key: key, TransportUDP: udp, Streams: 3, Provider: staticProvider{addr: turnAddr}, Connected: &connected})
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Errorf("client shutdown: %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Error("client did not stop")
				}
			})
			deadline := time.Now().Add(8 * time.Second)
			for connected.Load() != 3 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if connected.Load() != 3 {
				t.Fatalf("only %d/3 raw workers connected", connected.Load())
			}
			// Both directions at MTU and small sizes: IP/TCP headers are payload,
			// not reconstructed by the relay and not passed through WireGuard/DTLS.
			for _, size := range []int{64, 1280, 1350, 1351, 1492, 1500, 512} {
				up := ipPacket("10.77.0.2", "1.1.1.1", size)
				clientDev.input <- up
				readPacket(t, serverDev, up)
				down := ipPacket("1.1.1.1", "10.77.0.2", size)
				serverDev.input <- down
				readPacket(t, clientDev, down)
			}
		})
	}
}

func TestClientOptionParsing(t *testing.T) {
	opts, rest, err := ParseClientOptions([]string{"-peer", "1.2.3.4:56010", "--tun=rawtest", "-raw-mtu", "1200", "-raw-address=10.77.0.2/24", "-obf-key-file", "raw.key", "-n", "3"})
	if err != nil || opts.TUN != "rawtest" || opts.MTU != 1200 || opts.KeyFile != "raw.key" || len(rest) != 4 {
		t.Fatalf("options=%+v rest=%v err=%v", opts, rest, err)
	}
	if _, _, err := ParseClientOptions([]string{"-raw-mtu"}); err == nil {
		t.Fatal("missing value accepted")
	}
	if _, _, err := ParseClientOptions([]string{"-raw-mtu", "bad"}); err == nil {
		t.Fatal("invalid MTU accepted")
	}
}

func TestCancellationUnblocksIdleTUN(t *testing.T) {
	dev := newTestDevice()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunClient(ctx, dev, ClientConfig{Peer: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}, Address: netip.MustParseAddr("10.77.0.2"), MTU: 1280, Profile: "rtpopus2", Key: testKey(t), Streams: 1, Provider: cancelProvider{}})
	}()
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("idle client did not stop")
	}
}

type cancelProvider struct{ staticProvider }

func (cancelProvider) GetCredentials(ctx context.Context, _ int) (provider.Credentials, error) {
	<-ctx.Done()
	return provider.Credentials{}, ctx.Err()
}

func TestNewClientProcessRetiresOldWorkers(t *testing.T) {
	key := testKey(t)
	serverPC := udpSocket(t)
	dev, _, _ := startRawTestServer(t, serverPC, key)
	cfg := ClientConfig{Address: netip.MustParseAddr("10.77.0.2"), MTU: 1280, Profile: "rtpopus2", Key: key}
	cfg.group[0] = 1
	oldPC := udpSocket(t)
	oldCodec, _, err := clientHandshake(context.Background(), oldPC, serverPC.LocalAddr(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.group[0] = 2
	newPC := udpSocket(t)
	newCodec, _, err := clientHandshake(context.Background(), newPC, serverPC.LocalAddr(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	oldPacket := ipPacket("10.77.0.2", "1.1.1.1", 64)
	oldPacket[63] = 1
	buf := make([]byte, maxWire)
	if err := writeFrame(oldPC, serverPC.LocalAddr(), oldCodec, buf, append([]byte{frameData}, oldPacket...)); err != nil {
		t.Fatal(err)
	}
	good := ipPacket("10.77.0.2", "1.1.1.1", 64)
	good[63] = 2
	if err := writeFrame(newPC, serverPC.LocalAddr(), newCodec, buf, append([]byte{frameData}, good...)); err != nil {
		t.Fatal(err)
	}
	readPacket(t, dev, good)
}
