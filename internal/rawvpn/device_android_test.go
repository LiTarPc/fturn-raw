//go:build android && !fturntermux

package rawvpn

import (
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"
)

func TestAndroidDescriptorTransfer(t *testing.T) {
	for _, valid := range []bool{true, false} {
		t.Run(fmt.Sprint(valid), func(t *testing.T) {
			name := fmt.Sprintf("raw-test-%d-%d", os.Getpid(), time.Now().UnixNano())
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: "@" + name, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			defer writer.Close()
			t.Setenv("FREETURN_RAW_TUN_SOCKET", name)
			sent := make(chan error, 1)
			go func() {
				conn, err := listener.AcceptUnix()
				if err != nil {
					sent <- err
					return
				}
				defer conn.Close()
				meta := "10.77.0.2/24|1280"
				if !valid {
					meta = "10.77.0.2/24|1420"
				}
				_, _, err = conn.WriteMsgUnix([]byte(meta), unix.UnixRights(int(reader.Fd())), nil)
				sent <- err
			}()
			device, err := OpenDevice("", "10.77.0.2/24", 1280)
			if e := <-sent; e != nil {
				t.Fatal(e)
			}
			if !valid {
				if err == nil {
					device.Close()
					t.Fatal("accepted mismatched MTU")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer device.Close()
			reader.Close() // Only the received duplicate remains.
			if _, err = writer.Write([]byte("packet")); err != nil {
				t.Fatal(err)
			}
			b := make([]byte, 64)
			n, err := device.Read(b)
			if err != nil || string(b[:n]) != "packet" {
				t.Fatalf("read = %q, %v", b[:n], err)
			}
			done := make(chan error, 1)
			go func() { _, err := device.Read(b); done <- err }()
			device.Close()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("read after close succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("Close did not interrupt Read")
			}
		})
	}
}
func TestAndroidRequiresVpnService(t *testing.T) {
	t.Setenv("FREETURN_RAW_TUN_SOCKET", "")
	if _, err := openDevice("", netip.MustParsePrefix("10.77.0.2/24"), 1280); err == nil {
		t.Fatal("opened Android TUN without VpnService")
	}
}
