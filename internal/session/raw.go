package session

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"sync/atomic"

	"github.com/samosvalishe/free-turn-proxy/internal/captchaipc"
	"github.com/samosvalishe/free-turn-proxy/internal/client/dnsdial"
	"github.com/samosvalishe/free-turn-proxy/internal/config"
	"github.com/samosvalishe/free-turn-proxy/internal/logx"
	"github.com/samosvalishe/free-turn-proxy/internal/provider/vk"
	"github.com/samosvalishe/free-turn-proxy/internal/rawvpn"
	"github.com/samosvalishe/free-turn-proxy/internal/routemgr"
)

// RunRaw reuses the VK credential provider without creating a legacy DTLS session.
func RunRaw(ctx context.Context, cfg *config.Client, dev rawvpn.Device, address netip.Addr, mtu int, uplinkMode string, logger logx.Logger) error {
	if cfg.Proxy.Mode != config.ProxyModeUDP || cfg.Tunnel.Enabled() {
		return errors.New("raw-client requires -mode udp and no embedded WG/AWG tunnel")
	}
	if cfg.Obf.Timing != 0 {
		return errors.New("raw-client does not support -obf-timing")
	}
	logger = logx.OrNop(logger)
	dnsdial.SetLogger(logger)
	rawvpn.SetControlDNSLogger(logger)
	if cfg.DNS.Servers != nil {
		dnsdial.SetUDPDNSServers(cfg.DNS.Servers)
	}
	controlResolver := rawvpn.ControlResolver()
	dialer := rawServiceDialer(cfg.DNS.Mode, controlResolver)
	if controlResolver != nil {
		logger.Infof("[DNS] Raw service DNS: %s", rawvpn.ControlDNSDescription())
		if port := rawvpn.ControlDNSPort(); port > 0 {
			logger.Infof("[DNS] CONTROL_DNS_PORT=%d", port)
		}
	}
	var connected atomic.Int32
	total := cfg.TURN.N * max(len(cfg.VK.Links), 1)
	broker, err := captchaipc.FromEnvironment(ctx)
	if err != nil {
		return err
	}
	if os.Getenv("FREETURN_RAW_ADVANCED_CAPTCHA") == "1" {
		cfg.VK.AdvancedCaptcha = true
	}
	if broker != nil && cfg.VK.AdvancedCaptcha && !cfg.VK.ManualCaptcha && broker.Protocol() != 2 {
		defer broker.Close()
		return errors.New("advanced captcha requires captcha IPC protocol 2")
	}
	var hooks *vk.InteractiveHooks
	solver := vk.DefaultManualSolver
	if broker != nil {
		defer broker.Close()
		solver = vk.IPCSolver(broker, true)
		hooks = &vk.InteractiveHooks{BeforeFetch: broker.Wait, OnAuthWait: broker.Pause, OnAuthSuccess: broker.Authorized}
		if cfg.VK.AdvancedCaptcha && !cfg.VK.ManualCaptcha {
			hooks.PowCapture = vk.IPCPowCapture(broker)
		}
		if os.Getenv("FREETURN_RAW_BROWSER_CAPTCHA") == "1" && !cfg.VK.ManualCaptcha && !cfg.VK.AdvancedCaptcha {
			hooks.BrowserSolver = vk.IPCSolver(broker, false)
		}
	}
	prov, err := buildProvider(cfg, dialer, &connected, solver, logger, total, hooks)
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
		UplinkMode: uplinkMode, Streams: total, Provider: prov, Connected: &connected, OnTURN: onTURN, Log: logger,
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
