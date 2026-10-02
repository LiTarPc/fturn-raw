//go:build windows

package rawvpn

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"

	"github.com/amnezia-vpn/amneziawg-go/tun"
)

type windowsDevice struct {
	dev  tun.Device
	name string
}

func (d *windowsDevice) Read(b []byte) (int, error) {
	sizes := []int{0}
	n, err := d.dev.Read([][]byte{b}, sizes, 0)
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, fmt.Errorf("raw: unexpected TUN batch size %d", n)
	}
	return sizes[0], nil
}
func (d *windowsDevice) Write(b []byte) (int, error) {
	n, err := d.dev.Write([][]byte{b}, 0)
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, fmt.Errorf("raw: TUN dropped packet")
	}
	return len(b), nil
}
func (d *windowsDevice) Close() error { return d.dev.Close() }
func (d *windowsDevice) Name() string { return d.name }

func openDevice(name string, prefix netip.Prefix, mtu int) (Device, error) {
	native, err := tun.CreateTUN(name, mtu)
	if err != nil {
		return nil, fmt.Errorf("raw: create Wintun (run elevated, provide matching wintun.dll): %w", err)
	}
	actual, err := native.Name()
	if err != nil {
		native.Close()
		return nil, err
	}
	d := &windowsDevice{dev: native, name: actual}
	go func() {
		for range native.Events() {
		}
	}()
	mask := net.IP(net.CIDRMask(prefix.Bits(), 32)).String()
	if err := runCommand("netsh", "interface", "ipv4", "set", "address", "name="+actual, "source=static", "address="+prefix.Addr().String(), "mask="+mask); err != nil {
		d.Close()
		return nil, err
	}
	if err := runCommand("netsh", "interface", "ipv4", "set", "subinterface", actual, "mtu="+strconv.Itoa(mtu), "store=active"); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}
