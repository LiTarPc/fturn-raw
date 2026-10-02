//go:build windows

package rawvpn

import (
	"fmt"
	"math/bits"
	"net"
	"sync/atomic"
	"syscall"

	"github.com/samosvalishe/free-turn-proxy/internal/netctl"
	"golang.org/x/sys/windows"
)

var rawControlResolver atomic.Pointer[net.Resolver]

// ControlResolver is the dedicated service resolver selected by the Raw CLI.
// Keep it separately because shared session startup can replace DefaultResolver.
func ControlResolver() *net.Resolver { return rawControlResolver.Load() }

// BindControlInterface keeps VK/TURN sockets and encrypted service DNS on the underlay.
// Only this raw-client process is affected; application sockets are untouched.
func BindControlInterface(index int) error {
	if index == 0 {
		return nil
	}
	if index < 0 {
		return fmt.Errorf("raw: control interface index must be positive")
	}
	iface, err := net.InterfaceByIndex(index)
	if err != nil {
		return fmt.Errorf("raw: control interface: %w", err)
	}
	if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
		return fmt.Errorf("raw: control interface must be up and not loopback")
	}
	netctl.SetControl(func(network, address string, c syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip != nil && ip.To4() == nil {
			return fmt.Errorf("raw: control connections require IPv4")
		}
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
			return nil
		}
		var sockErr error
		if err := c.Control(func(fd uintptr) {
			// IP_UNICAST_IF requires the interface index in network byte order.
			sockErr = windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IP, 31 /* IP_UNICAST_IF, ws2ipdef.h */, int(bits.ReverseBytes32(uint32(index))))
		}); err != nil {
			return err
		}
		return sockErr
	})
	resolver := controlDNSResolver()
	rawControlResolver.Store(resolver)
	net.DefaultResolver = resolver
	return nil
}
