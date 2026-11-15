//go:build android

package rawvpn

import (
	"context"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/samosvalishe/free-turn-proxy/internal/logx"
	"github.com/samosvalishe/free-turn-proxy/internal/netctl"
	"golang.org/x/sys/unix"
)

// Android includes the UI/helper UID in the VPN. Every native underlay socket
// must be protected and bound to the physical network before connect or DNS.
func BindControlInterface(index int) error {
	if index != 0 {
		return fmt.Errorf("Android selects its physical network through VpnService")
	}
	endpoint := os.Getenv("FREETURN_RAW_PROTECT_SOCKET")
	if endpoint == "" {
		return nil
	} // Standalone/legacy callers exclude the core UID.
	netctl.SetControl(func(network, address string, raw syscall.RawConn) error {
		host, _, _ := net.SplitHostPort(address)
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
		var result error
		err := raw.Control(func(fd uintptr) {
			conn, e := net.DialUnix("unix", nil, &net.UnixAddr{Name: "@" + endpoint, Net: "unix"})
			if e != nil {
				result = e
				return
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			if _, _, e = conn.WriteMsgUnix([]byte{1}, unix.UnixRights(int(fd)), nil); e != nil {
				result = e
				return
			}
			var reply [1]byte
			if _, e = conn.Read(reply[:]); e != nil {
				result = e
			} else if reply[0] != 1 {
				result = fmt.Errorf("Android rejected underlay socket protection")
			}
		})
		if err != nil {
			return err
		}
		return result
	})
	// Standard TURN/DNS lookups also use protected sockets, including after TUN starts.
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{Control: netctl.Apply, Timeout: 5 * time.Second}).DialContext(ctx, network, address)
	}}
	return nil
}
func ControlResolver() *net.Resolver  { return nil }
func SetControlDNSLogger(logx.Logger) {}
func ControlDNSPort() int             { return 0 }
func CloseControlDNS()                {}
func ControlDNSDescription() string   { return "Android physical network" }
func BypassResolver() *net.Resolver   { return nil }
