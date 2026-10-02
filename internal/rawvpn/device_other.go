//go:build (!linux && !windows) || android

package rawvpn

import (
	"errors"
	"net/netip"
)

func openDevice(string, netip.Prefix, int) (Device, error) {
	return nil, errors.New("raw: this experimental CLI supports Windows and Linux; Android TUN-fd integration is not implemented")
}
