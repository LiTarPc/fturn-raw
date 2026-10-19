package rawvpn

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pion/stun/v3"
	"github.com/samosvalishe/free-turn-proxy/internal/netctl"
)

// Opt-in reachability probe: no credentials, TUN, route changes or media traffic.
func TestTURNUDPReachabilityLive(t *testing.T) {
	servers := os.Getenv("FTURN_RAW_UDP_PROBE_SERVERS")
	if servers == "" {
		t.Skip("UDP reachability probe is opt-in")
	}
	index, err := strconv.Atoi(os.Getenv("FTURN_RAW_CONTROL_INTERFACE"))
	if err != nil || index < 1 {
		t.Fatal("select the existing underlying interface")
	}
	if err := BindControlInterface(index); err != nil {
		t.Fatal(err)
	}
	defer netctl.SetControl(nil)
	defer CloseControlDNS()
	for _, server := range strings.Split(servers, ",") {
		t.Run(server, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			conn, err := (&net.Dialer{Control: netctl.Apply}).DialContext(ctx, "udp4", server)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			deadline, _ := ctx.Deadline()
			conn.SetDeadline(deadline)
			msg := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
			started := time.Now()
			if _, err = conn.Write(msg.Raw); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 2048)
			n, err := conn.Read(buf)
			if err != nil {
				t.Fatal(err)
			}
			reply := &stun.Message{Raw: buf[:n]}
			if err = reply.Decode(); err != nil {
				t.Fatal(err)
			}
			if reply.TransactionID != msg.TransactionID || reply.Type != stun.BindingSuccess {
				t.Fatal("unexpected STUN reply")
			}
			var mapped stun.XORMappedAddress
			if err = mapped.GetFrom(reply); err != nil {
				t.Fatal("missing mapping", err)
			}
			// The public mapped address is intentionally not written to logs.
			t.Logf("UDP STUN binding succeeded via interface %d in %s", index, time.Since(started).Round(time.Millisecond))
		})
	}
}
