//go:build windows

package session

import (
	"context"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/samosvalishe/free-turn-proxy/internal/netctl"
	"github.com/samosvalishe/free-turn-proxy/internal/rawvpn"
)

// Run with only this test executable's UDP/TCP 53/853 blocked to prove that
// session setup and repeated service lookups never depend on ordinary DNS.
func TestRawSessionDNSLiveUnderlay(t *testing.T) {
	value := os.Getenv("FTURN_RAW_TEST_CONTROL_INTERFACE")
	if value == "" {
		t.Skip("opt-in underlay DNS test")
	}
	index, err := strconv.Atoi(value)
	if err != nil {
		t.Fatal(err)
	}
	original := net.DefaultResolver
	defer func() { net.DefaultResolver = original; netctl.SetControl(nil) }()
	if err = rawvpn.BindControlInterface(index); err != nil {
		t.Fatal(err)
	}
	resolver := rawvpn.ControlResolver()
	if resolver == nil {
		t.Fatal("bound Raw control resolver missing")
	}
	dialer := rawServiceDialer("auto", resolver)
	if dialer.Resolver != resolver || net.DefaultResolver != resolver {
		t.Fatal("session reset dedicated control DNS")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	for _, host := range []string{"vk.ru", "login.vk.ru", "vk.ru"} {
		ips, err := dialer.Resolver.LookupIP(ctx, "ip4", host)
		if err != nil || len(ips) == 0 {
			t.Fatalf("provider lookup %s failed: %v", host, err)
		}
		ips, err = net.DefaultResolver.LookupIP(ctx, "ip4", host)
		if err != nil || len(ips) == 0 {
			t.Fatalf("global lookup %s failed: %v", host, err)
		}
	}
	t.Log("Raw session initialization preserved HTTPS DNS; six live lookups succeeded on the bound underlay")
}
