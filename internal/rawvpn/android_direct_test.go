//go:build android && !fturntermux

package rawvpn

import (
	"context"
	"fmt"
	"github.com/samosvalishe/free-turn-proxy/internal/netctl"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"testing"
	"time"
)

func TestAndroidDeferredTUNClosesBeforeAttachment(t *testing.T) {
	name := fmt.Sprintf("deferred-close-%d", time.Now().UnixNano())
	listener, e := net.ListenUnix("unix", &net.UnixAddr{Name: "@" + name, Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	t.Setenv("FREETURN_RAW_TUN_SOCKET", name)
	t.Setenv("FREETURN_RAW_TUN_DEFERRED", "1")
	device, e := OpenDevice("", "10.77.0.2/24", 1280)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := device.Read(make([]byte, 1500)); done <- e }()
	device.Close()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("read unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not wake deferred TUN reader")
	}
}
func TestAndroidDeferredDescriptorAcknowledgement(t *testing.T) {
	name := fmt.Sprintf("deferred-transfer-%d", time.Now().UnixNano())
	listener, e := net.ListenUnix("unix", &net.UnixAddr{Name: "@" + name, Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	reader, writer, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Close()
	defer writer.Close()
	t.Setenv("FREETURN_RAW_TUN_SOCKET", name)
	t.Setenv("FREETURN_RAW_TUN_DEFERRED", "1")
	device, e := OpenDevice("", "10.77.0.2/24", 1280)
	if e != nil {
		t.Fatal(e)
	}
	defer device.Close()
	conn, e := listener.AcceptUnix()
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	if _, _, e = conn.WriteMsgUnix([]byte("10.77.0.2/24|1280"), unix.UnixRights(int(reader.Fd())), nil); e != nil {
		t.Fatal(e)
	}
	b := make([]byte, 1)
	if _, e = conn.Read(b); e != nil || b[0] != 1 {
		t.Fatalf("descriptor ack: %v %v", b, e)
	}
	if _, e = writer.Write([]byte("packet")); e != nil {
		t.Fatal(e)
	}
	b = make([]byte, 20)
	n, e := device.Read(b)
	if e != nil || string(b[:n]) != "packet" {
		t.Fatalf("deferred read: %q %v", b[:n], e)
	}
}
func TestAndroidSocketProtectionBeforeDial(t *testing.T) {
	name := fmt.Sprintf("protect-test-%d", time.Now().UnixNano())
	listener, e := net.ListenUnix("unix", &net.UnixAddr{Name: "@" + name, Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	t.Setenv("FREETURN_RAW_PROTECT_SOCKET", name)
	original := net.DefaultResolver
	defer func() { net.DefaultResolver = original; netctl.SetControl(nil) }()
	if e = BindControlInterface(0); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() {
		conn, e := listener.AcceptUnix()
		if e != nil {
			done <- e
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(time.Second))
		data, oob := make([]byte, 1), make([]byte, unix.CmsgSpace(4))
		_, n, _, _, e := conn.ReadMsgUnix(data, oob)
		if e != nil {
			done <- e
			return
		}
		messages, e := unix.ParseSocketControlMessage(oob[:n])
		if e != nil {
			done <- e
			return
		}
		var fds []int
		for _, m := range messages {
			f, e := unix.ParseUnixRights(&m)
			if e != nil {
				done <- e
				return
			}
			fds = append(fds, f...)
		}
		defer func() {
			for _, fd := range fds {
				unix.Close(fd)
			}
		}()
		if len(fds) != 1 || data[0] != 1 {
			done <- fmt.Errorf("bad protection request")
			return
		}
		_, e = unix.GetsockoptInt(fds[0], unix.SOL_SOCKET, unix.SO_TYPE)
		if e == nil {
			_, e = conn.Write([]byte{1})
		}
		done <- e
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, e := (&net.Dialer{Control: netctl.Apply}).DialContext(ctx, "udp4", "203.0.113.1:9")
	if e != nil {
		t.Fatal(e)
	}
	c.Close()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}
