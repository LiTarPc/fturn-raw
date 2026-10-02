package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/samosvalishe/free-turn-proxy/internal/clientid"
	"github.com/samosvalishe/free-turn-proxy/internal/config"
	"github.com/samosvalishe/free-turn-proxy/internal/logx"
	"github.com/samosvalishe/free-turn-proxy/internal/rawvpn"
	"github.com/samosvalishe/free-turn-proxy/internal/session"
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
		if arg == "-help" || arg == "--help" || arg == "-h" || arg == "--h" {
			printHelp()
			return nil
		}
	}
	opts, args, err := rawvpn.ParseClientOptions(os.Args[1:])
	if err != nil {
		return err
	}
	defaults := []string{"-obf-profile", "rtpopus2"}
	if opts.KeyFile != "" {
		key, err := os.ReadFile(opts.KeyFile)
		if err != nil {
			return err
		}
		defaults = append(defaults, "-obf-key", strings.TrimSpace(string(key)))
	}
	cfg, err := config.ParseClient(append(defaults, args...), os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "\nRaw-specific flags: -tun ftraw0 -raw-address 10.77.0.2/24 -raw-mtu 1280 -obf-key-file raw.key")
		fmt.Fprintln(os.Stderr, "Raw requires -mode udp, rtpopus/rtpopus2, a dedicated shared key, and the matching raw-server.")
		return nil
	}
	if err != nil {
		return err
	}
	if cfg.Obf.GenKey {
		key, err := rtpopus.GenKeyHex()
		if err == nil {
			fmt.Println(key)
		}
		return err
	}
	if cfg.SubURL != "" {
		return errors.New("raw-client does not import legacy subscriptions; use explicit -peer and -links")
	}
	if cfg.Proxy.Mode != config.ProxyModeUDP || cfg.Tunnel.Enabled() || cfg.Obf.Timing != 0 {
		return errors.New("raw-client needs -mode udp, no -tunnel and no -obf-timing")
	}
	if cfg.Obf.Profile != config.ObfProfileRTPOpus && cfg.Obf.Profile != config.ObfProfileRTPOpus2 {
		return errors.New("raw-client requires rtpopus or rtpopus2 encryption")
	}
	if cfg.TURN.N*max(len(cfg.VK.Links), 1) > 64 {
		return errors.New("raw-client supports at most 64 total TURN workers")
	}
	prefix, err := netip.ParsePrefix(opts.Address)
	if err != nil || !prefix.Addr().Is4() {
		return errors.New("-raw-address must be an IPv4 CIDR")
	}
	cfg.ClientID, _, err = clientid.Resolve(cfg.ClientID, clientid.DefaultPaths())
	if err != nil {
		return err
	}
	if err := rawvpn.BindControlInterface(opts.ControlInterface); err != nil {
		return err
	}
	dev, err := rawvpn.OpenDevice(opts.TUN, opts.Address, opts.MTU)
	if err != nil {
		return err
	}
	defer dev.Close()
	logger := logx.New(cfg.Log.Debug)
	logger.Infof("Experimental RAW client: TUN=%s address=%s MTU=%d; default route is not changed", dev.Name(), opts.Address, opts.MTU)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return session.RunRaw(ctx, cfg, dev, prefix.Addr(), opts.MTU, logger)
}

func printHelp() {
	fmt.Fprint(os.Stderr, `Experimental fturn Raw IP client (Windows / Linux)

Usage:
  raw-client -peer VPS_IP:56010 -links VK_CALL_URL -obf-key-file raw.key [options]

  -peer address          Raw server UDP endpoint on VPS (required)
  -links urls            Comma-separated VK call links (required)
  -obf-key-file path     Dedicated shared key file (or -obf-key HEX)
  -obf-profile name      rtpopus2 (default) or rtpopus; must match server
  -tun name              TUN interface name (default ftraw0)
  -raw-address cidr      Client IPv4 interface (default 10.77.0.2/24)
  -raw-mtu bytes         IP MTU 576..1500 (default 1280); must match server
  -control-interface n   Bind VK/TURN/DNS to a Windows adapter index
  -n count               Streams per VK link (default 10, max 64 total)
  -transport name        TCP to TURN (default tcp), or udp
  -streams-per-cred n    Streams per VK credential cache (default 5)
  -dns-mode mode         auto (default), doh, or plain
  -dns-servers list      Custom DNS resolver addresses
  -manual-captcha        Use manual CAPTCHA solver
  -platform name        VK device persona: desktop (default) or mobile
  -client-id id          VK fingerprint seed; generated/persisted if omitted
  -turn host             Override TURN host
  -port port             Override TURN port
  -routes                Preserve host routes to TURN (requires admin)
  -debug                 Debug logs
  -gen-obf-key           Print a new key and exit

Run with administrator/root privileges. This client creates/configures its
TUN interface and preserves the existing default route. Start with ping
10.77.0.1. See docs/raw-experiment.md for NAT and speed measurements.
Legacy DTLS servers, WG/AWG configuration and subscriptions are not supported.
`)
}
