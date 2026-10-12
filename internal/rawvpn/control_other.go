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
