package rawvpn

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"net"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"
)

// Opt-in authenticated deployment check. It uses no local TUN or route changes.
// Run against a newly provisioned personal Raw server, before using the client:
// FTURN_RAW_DEPLOY_PEER=IP:PORT FTURN_RAW_DEPLOY_KEY_FILE=/path/raw.key go test ...
func TestRawDeploymentLive(t *testing.T) {
	endpoint, path := os.Getenv("FTURN_RAW_DEPLOY_PEER"), os.Getenv("FTURN_RAW_DEPLOY_KEY_FILE")
	if endpoint == "" || path == "" {
		t.Skip("opt-in deployment check")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("cannot read deployment key file")
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(key) != 32 {
		t.Fatal("invalid deployment key file")
	}
	peer, err := net.ResolveUDPAddr("udp4", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	pc, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	cfg := ClientConfig{Address: netip.MustParseAddr("10.77.0.2"), MTU: 1420, Profile: "rtpopus2", Key: key}
	if _, err = rand.Read(cfg.group[:]); err != nil {
		t.Fatal(err)
	}
	if _, err = rand.Read(cfg.workerToken[:]); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	c, mtu, err := clientHandshake(ctx, pc, peer, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writeFrame(pc, peer, c, make([]byte, maxWire), []byte{frameClose}) }()
	if mtu != 1420 {
		t.Fatal("unexpected deployed MTU")
	}
	t.Log("Authenticated handshake succeeded")
	seq := uint16(0)
	for _, target := range []string{"10.77.0.1", "1.1.1.1"} {
		for _, size := range []int{64, 1420} {
			seq++
			packet := deploymentEcho(target, size, seq)
			if err := writeFrame(pc, peer, c, make([]byte, maxWire), append([]byte{frameData}, packet...)); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			_ = pc.SetReadDeadline(deadline)
			received := false
			for time.Now().Before(deadline) {
				wire := make([]byte, maxWire+1)
				n, from, err := pc.ReadFrom(wire)
				if err != nil {
					break
				}
				if from.String() != peer.String() {
					continue
				}
				plain, err := c.decode(make([]byte, maxFrame), wire[:n])
				if err != nil {
					continue
				}
				body, err := c.fragments.accept(plain, mtu, time.Now())
				if err != nil || len(body) < 28 {
					continue
				}
				h := int(body[0]&15) * 4
				if len(body) == size && len(body) >= h+8 && body[9] == 1 && body[h] == 0 && binary.BigEndian.Uint16(body[h+4:h+6]) == 0x4652 && binary.BigEndian.Uint16(body[h+6:h+8]) == seq && netip.AddrFrom4([4]byte(body[12:16])).String() == target {
					received = true
					break
				}
			}
			if !received {
				t.Fatalf("no authenticated IPv4 echo reply from %s, size=%d", target, size)
			}
			t.Logf("IPv4 echo reply from %s, size=%d", target, size)
		}
	}
}
func deploymentEcho(target string, size int, seq uint16) []byte {
	b := make([]byte, size)
	b[0] = 0x45
	b[8] = 64
	b[9] = 1
	binary.BigEndian.PutUint16(b[2:4], uint16(size))
	binary.BigEndian.PutUint16(b[6:8], 0x4000)
	src, dst := netip.MustParseAddr("10.77.0.2").As4(), netip.MustParseAddr(target).As4()
	copy(b[12:16], src[:])
	copy(b[16:20], dst[:])
	b[20] = 8
	binary.BigEndian.PutUint16(b[24:26], 0x4652)
	binary.BigEndian.PutUint16(b[26:28], seq)
	for i := 28; i < size; i++ {
		b[i] = byte(i)
	}
	binary.BigEndian.PutUint16(b[22:24], deploymentChecksum(b[20:]))
	binary.BigEndian.PutUint16(b[10:12], deploymentChecksum(b[:20]))
	return b
}
func deploymentChecksum(b []byte) uint16 {
	var sum uint32
	for len(b) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(b))
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
