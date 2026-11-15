//go:build android && !fturntermux

package rawvpn

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sync"
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
	if os.Getenv("FREETURN_RAW_TUN_DEFERRED") == "1" {
		ctx, cancel := context.WithCancel(context.Background())
		d := &deferredAndroidDevice{ctx: ctx, cancel: cancel, ready: make(chan struct{})}
		go func() {
			dev, err := receiveAndroidDevice(ctx, prefix, mtu, true)
			d.mu.Lock()
			defer d.mu.Unlock()
			if ctx.Err() != nil && dev != nil {
				dev.Close()
				dev = nil
				err = net.ErrClosed
			}
			d.dev, d.err = dev, err
			close(d.ready)
		}()
		return d, nil
	}
	return receiveAndroidDevice(context.Background(), prefix, mtu, false)
}

type deferredAndroidDevice struct {
	ctx    context.Context
	cancel context.CancelFunc
	ready  chan struct{}
	mu     sync.Mutex
	dev    Device
	err    error
}

func (d *deferredAndroidDevice) Name() string { return "android-vpn-direct" }
func (d *deferredAndroidDevice) device() (Device, error) {
	select {
	case <-d.ctx.Done():
		return nil, net.ErrClosed
	case <-d.ready:
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dev, d.err
}
func (d *deferredAndroidDevice) Read(b []byte) (int, error) {
	v, e := d.device()
	if e != nil {
		return 0, e
	}
	return v.Read(b)
}
func (d *deferredAndroidDevice) Write(b []byte) (int, error) {
	v, e := d.device()
	if e != nil {
		return 0, e
	}
	return v.Write(b)
}
func (d *deferredAndroidDevice) Close() error {
	d.cancel()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dev != nil {
		return d.dev.Close()
	}
	return nil
}

func receiveAndroidDevice(ctx context.Context, prefix netip.Prefix, mtu int, deferred bool) (Device, error) {
	name := os.Getenv("FREETURN_RAW_TUN_SOCKET")
	if name == "" {
		return nil, fmt.Errorf("raw: Android requires a VpnService TUN socket")
	}
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: "@" + name, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("raw: connect Android TUN: %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if !deferred {
		if err = conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
			return nil, err
		}
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
	if deferred {
		if _, err = conn.Write([]byte{1}); err != nil {
			file.Close()
			return nil, err
		}
	}
	return &androidDevice{file: file}, nil
}
