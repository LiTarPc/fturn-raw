package session

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync/atomic"

	"github.com/samosvalishe/free-turn-proxy/internal/client/dnsdial"
	"github.com/samosvalishe/free-turn-proxy/internal/config"
	"github.com/samosvalishe/free-turn-proxy/internal/logx"
	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk"
	"github.com/samosvalishe/free-turn-proxy/internal/rawvpn"
	"github.com/samosvalishe/free-turn-proxy/internal/routemgr"
)

// RunRaw reuses the VK credential provider without creating a legacy DTLS session.
func RunRaw(ctx context.Context, cfg *config.Client, dev rawvpn.Device, address netip.Addr, mtu int, logger logx.Logger) error {
	if cfg.Proxy.Mode != config.ProxyModeUDP || cfg.Tunnel.Enabled() {
		return errors.New("raw-client requires -mode udp and no embedded WG/AWG tunnel")
	}
	if cfg.Obf.Timing != 0 {
		return errors.New("raw-client does not support -obf-timing")
	}
	logger = logx.OrNop(logger)
	dnsdial.SetLogger(logger)
	if cfg.DNS.Servers != nil {
		dnsdial.SetUDPDNSServers(cfg.DNS.Servers)
	}
	controlResolver := rawvpn.ControlResolver()
	dialer := rawServiceDialer(cfg.DNS.Mode, controlResolver)
	if controlResolver != nil {
		logger.Infof("[DNS] Raw service DNS: HTTPS via selected underlay; UDP/53 disabled for VK/TURN lookups")
	}
	var connected atomic.Int32
	total := cfg.TURN.N * max(len(cfg.VK.Links), 1)
	prov, err := buildProvider(cfg, dialer, &connected, vk.DefaultManualSolver, logger, total)
	if err != nil {
		return err
	}
	peer, err := net.ResolveUDPAddr("udp", cfg.Proxy.Peer)
	if err != nil {
		return err
	}
	var onTURN func(net.IP)
	if cfg.Routes {
		manager, err := routemgr.New(logger)
		if err != nil {
			return err
		}
		if manager != nil {
			defer manager.Close()
			onTURN = manager.Callback()
		}
	}
	return rawvpn.RunClient(ctx, dev, rawvpn.ClientConfig{
		Peer: peer, Address: address, MTU: mtu, Profile: string(cfg.Obf.Profile), Key: cfg.Obf.Key,
		Host: cfg.TURN.Host, Port: cfg.TURN.Port, TransportUDP: cfg.TURN.TransportUDP,
		Streams: total, Provider: prov, Connected: &connected, OnTURN: onTURN, Log: logger,
	})
}

// Both the VK provider's explicit dialer and standard-library TURN lookups must
// use the same underlay resolver. Never run auto UDP probing for a bound Raw CLI.
func rawServiceDialer(mode string, controlResolver *net.Resolver) net.Dialer {
	dialer := dnsdial.AppDialer(mode)
	if controlResolver != nil {
		dialer.Resolver = controlResolver
	}
	net.DefaultResolver = dialer.Resolver
	return dialer
}
