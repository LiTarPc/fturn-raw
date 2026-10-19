package session

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samosvalishe/free-turn-proxy/internal/logx"
	"github.com/samosvalishe/free-turn-proxy/internal/netctl"
	"github.com/samosvalishe/free-turn-proxy/internal/provider"
	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk"
	"github.com/samosvalishe/free-turn-proxy/internal/rawvpn"
)

// This opt-in test reads a selected inactive profile. It never creates a TUN,
// changes routes, opens a browser, logs credentials, or writes the user's state.
func TestRawUDPProfileLive(t *testing.T) {
	path, peerText := os.Getenv("FTURN_RAW_UDP_LIVE_PROFILES"), os.Getenv("FTURN_RAW_UDP_LIVE_PEER")
	if path == "" || peerText == "" {
		t.Skip("live UDP profile check is opt-in")
	}
	var book struct {
		ActiveID string
		Profiles []struct {
			ID      string
			Profile struct {
				Server, VkLink, Key string
				Mtu                 int
			}
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("cannot read profiles")
	}
	if json.Unmarshal(data, &book) != nil {
		t.Fatal("cannot decode profiles")
	}
	var link string
	var key []byte
	var mtu int
	for _, entry := range book.Profiles {
		if entry.Profile.Server == peerText {
			if entry.ID == book.ActiveID {
				t.Fatal("probe would replace the active server session")
			}
			link = entry.Profile.VkLink
			key, err = hex.DecodeString(entry.Profile.Key)
			mtu = entry.Profile.Mtu
		}
	}
	if err != nil || len(key) != 32 || link == "" {
		t.Fatal("inactive test profile missing")
	}
	index, err := strconv.Atoi(os.Getenv("FTURN_RAW_CONTROL_INTERFACE"))
	if err != nil || index < 1 {
		t.Fatal("underlying interface required")
	}
	if err = rawvpn.BindControlInterface(index); err != nil {
		t.Fatal("cannot select underlying interface")
	}
	defer netctl.SetControl(nil)
	defer rawvpn.CloseControlDNS()
	var connected atomic.Int32
	log := logx.Nop()
	prov, err := vk.New(vk.Config{Link: link, Dialer: rawServiceDialer("auto", rawvpn.ControlResolver()), Platform: "desktop", StreamsPerCache: 1, StreamsAlive: connected.Load, FingerprintSeed: "raw-udp-profile-probe-v1", StatePaths: []string{t.TempDir() + "/persona.json"}, Log: log}, nil)
	if err != nil {
		t.Fatal("cannot create isolated provider")
	}
	peer, err := net.ResolveUDPAddr("udp4", peerText)
	if err != nil {
		t.Fatal(err)
	}
	dev := &liveProbeDevice{input: make(chan []byte, 8), output: make(chan []byte, 64), done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- rawvpn.RunClient(ctx, dev, rawvpn.ClientConfig{Peer: peer, Address: netip.MustParseAddr("10.77.0.2"), MTU: mtu, Profile: "rtpopus3", Key: key, TransportUDP: true, Streams: 1, Provider: udpProbeProvider{prov}, Connected: &connected, Log: log})
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("UDP probe did not stop")
		}
	}()
	for _, target := range []struct {
		ip   string
		size int
	}{{"10.77.0.1", 64}, {"10.77.0.1", mtu}, {"1.1.1.1", 64}, {"1.1.1.1", mtu}} {
		ticker := time.NewTicker(time.Second)
		success := false
		for !success {
			select {
			case <-ctx.Done():
				ticker.Stop()
				t.Fatal("live UDP probe timed out")
			case err := <-done:
				done <- err
				ticker.Stop()
				t.Fatalf("isolated live UDP connection failed: %v", err)
			case <-ticker.C:
				select {
				case dev.input <- liveEcho(target.ip, target.size):
				default:
				}
			case packet := <-dev.output:
				if len(packet) != target.size || len(packet) < 28 || packet[9] != 1 {
					continue
				}
				ihl := int(packet[0]&15) * 4
				if ihl < 20 || len(packet) < ihl+8 || packet[ihl] != 0 {
					continue
				}
				src := netip.AddrFrom4([4]byte(packet[12:16]))
				if src.String() == target.ip {
					success = true
					t.Logf("Authenticated UDP + rtpopus3 ICMP reply: %s, IPv4 size=%d", target.ip, len(packet))
				}
			}
		}
		ticker.Stop()
	}
}

// A failed authorization ends this isolated probe rather than triggering a retry loop.
type udpProbeProvider struct{ provider.Provider }

func (p udpProbeProvider) GetCredentials(ctx context.Context, id int) (provider.Credentials, error) {
	credentials, err := p.Provider.GetCredentials(ctx, id)
	if err != nil {
		category := "authorization unavailable"
		text := strings.ToLower(err.Error())
		switch {
		case strings.Contains(text, "captcha"):
			category = "captcha"
		case strings.Contains(text, "timeout") || strings.Contains(text, "deadline"):
			category = "network timeout"
		case strings.Contains(text, "lookup") || strings.Contains(text, "dns"):
			category = "service DNS"
		}
		return provider.Credentials{}, fmt.Errorf("%w: %s", provider.ErrFatalNoStreams, category)
	}
	return credentials, nil
}
