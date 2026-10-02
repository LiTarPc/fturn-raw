package session

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samosvalishe/free-turn-proxy/internal/config"
	"github.com/samosvalishe/free-turn-proxy/internal/logx"
)

// Opt-in live check: Windows VK/TURN -> remote Raw -> Linux TUN -> ICMP/NAT.
// The memory device does not need administrator rights or change Windows routes.
// Set FTURN_RAW_LIVE_PEER, FTURN_RAW_LIVE_LINK and FTURN_RAW_LIVE_KEY_FILE.
func TestLiveRawVK(t *testing.T) {
	peer, link, keyFile := os.Getenv("FTURN_RAW_LIVE_PEER"), os.Getenv("FTURN_RAW_LIVE_LINK"), os.Getenv("FTURN_RAW_LIVE_KEY_FILE")
	if peer == "" || link == "" || keyFile == "" {
		t.Skip("live VPS/VK check is opt-in")
	}
	key, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseClient([]string{"-peer", peer, "-links", link, "-n", "3", "-transport", "tcp", "-obf-profile", "rtpopus2", "-obf-key", strings.TrimSpace(string(key))}, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	mtu := 1280
	if value := os.Getenv("FTURN_RAW_LIVE_MTU"); value != "" {
		mtu, err = strconv.Atoi(value)
		if err != nil {
			t.Fatal(err)
		}
	}
	cfg.ClientID = "fturn-raw-live-probe-v1"
	dev := &liveProbeDevice{input: make(chan []byte, 8), output: make(chan []byte, 64), done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	done := make(chan error, 1)
	go func() { done <- RunRaw(ctx, cfg, dev, netip.MustParseAddr("10.77.0.2"), mtu, logx.New(false)) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("live client did not stop")
		}
	}()
	for _, target := range []struct {
		addr string
		size int
	}{{"10.77.0.1", 64}, {"10.77.0.1", mtu}, {"1.1.1.1", 64}, {"1.1.1.1", mtu}} {
		ticker := time.NewTicker(time.Second)
		success := false
		for !success {
			select {
			case <-ctx.Done():
				ticker.Stop()
				t.Fatalf("no ICMP reply from %s size=%d: %v", target.addr, target.size, ctx.Err())
			case err := <-done:
				done <- err
				ticker.Stop()
				t.Fatalf("live client stopped: %v", err)
			case <-ticker.C:
				select {
				case dev.input <- liveEcho(target.addr, target.size):
				default:
				}
			case p := <-dev.output:
				if len(p) < 28 || p[9] != 1 {
					continue
				}
				h := int(p[0]&15) * 4
				if h < 20 || len(p) < h+8 {
					continue
				}
				dst := netip.AddrFrom4([4]byte{p[16], p[17], p[18], p[19]})
				src := netip.AddrFrom4([4]byte{p[12], p[13], p[14], p[15]})
				if src.String() == target.addr && dst.String() == "10.77.0.2" && p[h] == 0 && binary.BigEndian.Uint16(p[h+4:h+6]) == 0x5257 && len(p) == target.size {
					success = true
					t.Logf("authenticated VK/TURN Raw ICMP reply: %s, IPv4 size=%d", target.addr, len(p))
				}
			}
		}
		ticker.Stop()
	}
}

type liveProbeDevice struct {
	input, output chan []byte
	done          chan struct{}
	once          sync.Once
}

func (d *liveProbeDevice) Read(b []byte) (int, error) {
	select {
	case <-d.done:
		return 0, net.ErrClosed
	case p := <-d.input:
		return copy(b, p), nil
	}
}
func (d *liveProbeDevice) Write(b []byte) (int, error) {
	select {
	case <-d.done:
		return 0, net.ErrClosed
	case d.output <- append([]byte(nil), b...):
		return len(b), nil
	}
}
func (d *liveProbeDevice) Close() error { d.once.Do(func() { close(d.done) }); return nil }
func (d *liveProbeDevice) Name() string { return "live-probe-memory" }
func liveEcho(dst string, size int) []byte {
	p := make([]byte, size)
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:4], uint16(size))
	p[8] = 64
	p[9] = 1
	src := netip.MustParseAddr("10.77.0.2").As4()
	target := netip.MustParseAddr(dst).As4()
	copy(p[12:16], src[:])
	copy(p[16:20], target[:])
	p[20] = 8
	binary.BigEndian.PutUint16(p[24:26], 0x5257)
	binary.BigEndian.PutUint16(p[26:28], uint16(size))
	for i := 28; i < len(p); i++ {
		p[i] = byte(i)
	}
	binary.BigEndian.PutUint16(p[22:24], liveChecksum(p[20:]))
	binary.BigEndian.PutUint16(p[10:12], liveChecksum(p[:20]))
	return p
}
func liveChecksum(b []byte) uint16 {
	var sum uint32
	for len(b) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(b[:2]))
		b = b[2:]
	}
	if len(b) > 0 {
		sum += uint32(b[0]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}
