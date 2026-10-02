package rawvpn

import (
	"fmt"
	"net/netip"
	"os/exec"
)

// Device preserves IP packet boundaries. Close must unblock Read.
type Device interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
	Name() string
}

func OpenDevice(name, address string, mtu int) (Device, error) {
	prefix, err := netip.ParsePrefix(address)
	if err != nil || !prefix.Addr().Is4() || prefix.Bits() < 1 || prefix.Bits() > 30 {
		return nil, fmt.Errorf("raw: address must be an IPv4 interface CIDR with prefix 1..30")
	}
	if err := validateMTU(mtu); err != nil {
		return nil, err
	}
	return openDevice(name, prefix, mtu)
}

func runCommand(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, out)
	}
	return nil
}
