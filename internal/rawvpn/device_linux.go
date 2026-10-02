//go:build linux && !android

package rawvpn

import (
	"fmt"
	"net/netip"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

type linuxDevice struct {
	file *os.File
	name string
}

func (d *linuxDevice) Read(b []byte) (int, error)  { return d.file.Read(b) }
func (d *linuxDevice) Write(b []byte) (int, error) { return d.file.Write(b) }
func (d *linuxDevice) Close() error                { return d.file.Close() }
func (d *linuxDevice) Name() string                { return d.name }

func openDevice(name string, prefix netip.Prefix, mtu int) (Device, error) {
	fd, err := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("raw: open TUN (requires root/CAP_NET_ADMIN): %w", err)
	}
	request, err := unix.NewIfreq(name)
	if err == nil {
		// No packet-information or virtio/GSO header: just one IPv4 packet per read.
		request.SetUint16(unix.IFF_TUN | unix.IFF_NO_PI)
		err = unix.IoctlIfreq(fd, unix.TUNSETIFF, request)
	}
	if err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("raw: create TUN: %w", err)
	}
	d := &linuxDevice{file: os.NewFile(uintptr(fd), "/dev/net/tun"), name: request.Name()}
	if err := runCommand("ip", "address", "add", prefix.String(), "dev", d.name); err != nil {
		d.Close()
		return nil, err
	}
	if err := runCommand("ip", "link", "set", "dev", d.name, "mtu", strconv.Itoa(mtu), "up"); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}
