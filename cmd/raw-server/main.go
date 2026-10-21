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

	"github.com/samosvalishe/free-turn-proxy/internal/buildinfo"
	"github.com/samosvalishe/free-turn-proxy/internal/logx"
	"github.com/samosvalishe/free-turn-proxy/internal/rawvpn"
	"github.com/samosvalishe/free-turn-proxy/internal/wire/rtpopus"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	for _, arg := range os.Args[1:] {
		if arg == "-version" || arg == "--version" {
			fmt.Println("FturnRaw server " + buildinfo.ServerVersion())
			return nil
		}
	}
	fs := flag.NewFlagSet("raw-server", flag.ContinueOnError)
	listen := fs.String("listen", "0.0.0.0:56010", "UDP endpoint reached by TURN relay")
	name := fs.String("tun", "ftraw0", "TUN interface name")
	address := fs.String("address", "10.77.0.1/24", "server IPv4 interface CIDR")
	clientIP := fs.String("client-ip", "10.77.0.2", "shared client IPv4 and first server-side address in the device pool")
	maxDevices := fs.Int("max-devices", rawvpn.DefaultMaxDevices, "simultaneous devices per shared key (1..64)")
	mtu := fs.Int("mtu", rawvpn.DefaultMTU, "IP MTU, must match client (576..1500)")
	profile := fs.String("obf-profile", "auto", "encrypted RTP profile: auto (rtpopus2 + rtpopus3) | rtpopus | rtpopus2 | rtpopus3")
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
	if *profile != "auto" && !rawvpn.SupportedProfile(*profile) {
		return errors.New("raw-server requires auto, rtpopus, rtpopus2 or rtpopus3")
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
	if err != nil {
		return errors.New("-address must be a server IPv4 CIDR")
	}
	if err := rawvpn.ValidateClientPool(prefix, ip, *maxDevices); err != nil {
		return err
	}
	if *maxDevices < 1 {
		return errors.New("-max-devices must be between 1 and 64")
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
	logger.Infof("[VERSION] FturnRaw server %s; profiles=%s", buildinfo.ServerVersion(), *profile)
	logger.Infof("Experimental RAW server: %s TUN=%s address=%s client=%s MTU=%d", pc.LocalAddr(), dev.Name(), *address, ip, *mtu)
	logger.Infof("RAW device limit=%d; configure IPv4 forwarding and NAT for the entire client address pool; see docs/raw-server.md", *maxDevices)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return rawvpn.RunServer(ctx, pc, dev, rawvpn.ServerConfig{ClientAddress: ip, MaxDevices: *maxDevices, MTU: *mtu, Profile: *profile, Key: key, Log: logger})
}
