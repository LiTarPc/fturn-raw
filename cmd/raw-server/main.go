package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/samosvalishe/free-turn-proxy/internal/logx"
	"github.com/samosvalishe/free-turn-proxy/internal/rawvpn"
	"github.com/samosvalishe/free-turn-proxy/internal/wire"
	"github.com/samosvalishe/free-turn-proxy/internal/wire/rtpopus"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	fs := flag.NewFlagSet("raw-server", flag.ContinueOnError)
	listen := fs.String("listen", "0.0.0.0:56010", "UDP endpoint reached by TURN relay")
	name := fs.String("tun", "ftraw0", "TUN interface name")
	address := fs.String("address", "10.77.0.1/24", "server IPv4 interface CIDR")
	clientIP := fs.String("client-ip", "10.77.0.2", "the only client IP authorized by this dedicated key")
	mtu := fs.Int("mtu", rawvpn.DefaultMTU, "IP MTU, must match client (576..1500)")
	profile := fs.String("obf-profile", wire.ProfileRTPOpus2, "encrypted RTP profile: rtpopus | rtpopus2")
	keyHex := fs.String("obf-key", "", "dedicated 64-character hex shared key")
	keyFile := fs.String("obf-key-file", "", "read shared key from file")
	gen := fs.Bool("gen-obf-key", false, "generate a random shared key and exit")
	debug := fs.Bool("debug", false, "enable debug logging")
	if err := fs.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("raw-server: unexpected positional arguments")
	}
	if *gen {
		key, err := rtpopus.GenKeyHex()
		if err == nil {
			fmt.Println(key)
		}
		return err
	}
	if *keyFile != "" {
		if *keyHex != "" {
			return errors.New("choose either -obf-key or -obf-key-file")
		}
		b, err := os.ReadFile(*keyFile)
		if err != nil {
			return err
		}
		*keyHex = strings.TrimSpace(string(b))
	}
	if *profile != wire.ProfileRTPOpus && *profile != wire.ProfileRTPOpus2 {
		return errors.New("raw-server requires rtpopus or rtpopus2")
	}
	key, err := rtpopus.DecodeKey(true, *keyHex)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(*clientIP)
	if err != nil || !ip.Is4() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLoopback() {
		return errors.New("-client-ip must be a unicast IPv4 address")
	}
	prefix, err := netip.ParsePrefix(*address)
	if err != nil || !prefix.Addr().Is4() || !prefix.Contains(ip) || prefix.Addr() == ip {
		return errors.New("client IP must be inside the server IPv4 subnet and differ from server address")
	}
	dev, err := rawvpn.OpenDevice(*name, *address, *mtu)
	if err != nil {
		return err
	}
	defer dev.Close()
	pc, err := net.ListenPacket("udp", *listen)
	if err != nil {
		return err
	}
	defer pc.Close()
	logger := logx.New(*debug)
	logger.Infof("Experimental RAW server: %s TUN=%s address=%s client=%s MTU=%d", pc.LocalAddr(), dev.Name(), *address, ip, *mtu)
	logger.Infof("Configure IPv4 forwarding and NAT for internet access; see docs/raw-experiment.md")
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return rawvpn.RunServer(ctx, pc, dev, rawvpn.ServerConfig{ClientAddress: ip, MTU: *mtu, Profile: *profile, Key: key, Log: logger})
}
