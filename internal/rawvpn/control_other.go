//go:build !windows

package rawvpn

import (
	"fmt"
	"github.com/samosvalishe/free-turn-proxy/internal/logx"
	"net"
)

func BindControlInterface(index int) error {
	if index != 0 {
		return fmt.Errorf("raw: -control-interface is supported only on Windows")
	}
	return nil
}

func ControlResolver() *net.Resolver { return nil }

func SetControlDNSLogger(logx.Logger) {}

func ControlDNSPort() int           { return 0 }
func CloseControlDNS()              {}
func ControlDNSDescription() string { return "system DNS" }
func BypassResolver() *net.Resolver { return nil }
