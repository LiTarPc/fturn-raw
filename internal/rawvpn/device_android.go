//go:build android && !fturntermux

package rawvpn

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// Android owns the interface and routes through VpnService. The child receives
// a duplicate descriptor through a private, same-UID local socket (SCM_RIGHTS).
type androidDevice struct{ file *os.File }

func (d *androidDevice) Read(b []byte) (int, error)  { return d.file.Read(b) }
func (d *androidDevice) Write(b []byte) (int, error) { return d.file.Write(b) }
func (d *androidDevice) Close() error                { return d.file.Close() }
func (d *androidDevice) Name() string                { return "android-vpn" }

func openDevice(_ string, prefix netip.Prefix, mtu int) (Device, error) {
	name := os.Getenv("FREETURN_RAW_TUN_SOCKET")
	if name == "" {
		return nil, fmt.Errorf("raw: Android requires a VpnService TUN socket")
	}
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: "@" + name, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("raw: connect Android TUN: %w", err)
	}
	defer conn.Close()
	if err = conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return nil, err
	}
	data, control := make([]byte, 128), make([]byte, unix.CmsgSpace(4)*8)
	n, oobn, flags, _, err := conn.ReadMsgUnix(data, control)
	if err != nil {
		return nil, fmt.Errorf("raw: receive Android TUN: %w", err)
	}
	messages, err := unix.ParseSocketControlMessage(control[:oobn])
	if err != nil {
		return nil, err
	}
	var fds []int
	for _, message := range messages {
		if message.Header.Level == unix.SOL_SOCKET && message.Header.Type == unix.SCM_RIGHTS {
			received, e := unix.ParseUnixRights(&message)
			if e != nil {
				for _, fd := range fds {
					unix.Close(fd)
				}
				return nil, e
			}
			fds = append(fds, received...)
		}
	}
	adopted := false
	defer func() {
		if !adopted {
			for _, fd := range fds {
				unix.Close(fd)
			}
		}
	}()
	if flags&(unix.MSG_CTRUNC|unix.MSG_TRUNC) != 0 || len(fds) != 1 {
		return nil, fmt.Errorf("raw: expected exactly one Android TUN descriptor")
	}
	if string(data[:n]) != fmt.Sprintf("%s|%d", prefix, mtu) {
		return nil, fmt.Errorf("raw: Android TUN address/MTU mismatch")
	}
	unix.CloseOnExec(fds[0])
	// Register with Go's poller so Close interrupts a pending TUN read.
	if err = unix.SetNonblock(fds[0], true); err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fds[0]), "android-vpn")
	if file == nil {
		return nil, fmt.Errorf("raw: invalid Android TUN descriptor")
	}
	adopted = true
	return &androidDevice{file: file}, nil
}
