//go:build windows

package rawvpn

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func testServiceDNSReply(t *testing.T, w http.ResponseWriter, r *http.Request, rcode dnsmessage.RCode) {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return
	}
	var query dnsmessage.Message
	if err = query.Unpack(data); err != nil {
		t.Error(err)
		return
	}
	answer := dnsmessage.Message{Header: dnsmessage.Header{ID: query.ID, Response: true, RecursionAvailable: true, RCode: rcode}, Questions: query.Questions}
	if rcode == dnsmessage.RCodeSuccess && query.Questions[0].Type == dnsmessage.TypeA {
		answer.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: query.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60}, Body: &dnsmessage.AResource{A: [4]byte{192, 0, 2, 9}}}}
	}
	body, err := answer.Pack()
	if err != nil {
		t.Error(err)
		return
	}
	w.Header().Set("Content-Type", "application/dns-message")
	_, _ = w.Write(body)
}
func testServiceDNSLookup(t *testing.T, pool *controlDNSPool, host string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ips, err := resolverForControlDNS(pool).LookupIP(ctx, "ip4", host)
	if err != nil || len(ips) != 1 || !ips[0].Equal(net.IPv4(192, 0, 2, 9)) {
		t.Fatalf("lookup: %v, %v", ips, err)
	}
}
func TestControlDNSHTTPSFailureSwitchesAndRecovers(t *testing.T) {
	var primaryCalls, fallbackCalls atomic.Int32
	var primaryHealthy, fallbackBroken atomic.Bool
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalls.Add(1)
		if !primaryHealthy.Load() {
			w.WriteHeader(503)
			return
		}
		testServiceDNSReply(t, w, r, dnsmessage.RCodeSuccess)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackCalls.Add(1)
		if fallbackBroken.Load() {
			w.WriteHeader(502)
			return
		}
		testServiceDNSReply(t, w, r, dnsmessage.RCodeSuccess)
	}))
	defer fallback.Close()
	pool := newControlDNSPool([]controlDNSUpstream{{"primary", primary.Client(), primary.URL}, {"fallback", fallback.Client(), fallback.URL}})
	testServiceDNSLookup(t, pool, "first.invalid")
	if primaryCalls.Load() != 1 || fallbackCalls.Load() != 1 || pool.preferred.Load() != 1 {
		t.Fatal("HTTPS failure did not select fallback")
	}
	testServiceDNSLookup(t, pool, "second.invalid")
	if primaryCalls.Load() != 1 || fallbackCalls.Load() != 2 {
		t.Fatal("healthy fallback was not reused")
	}
	primaryHealthy.Store(true)
	fallbackBroken.Store(true)
	testServiceDNSLookup(t, pool, "recovered.invalid")
	if pool.preferred.Load() != 0 || primaryCalls.Load() != 2 {
		t.Fatal("preferred endpoint recovery failed")
	}
}
func TestControlDNSSlowHTTPSFallsBackWithinLookupDeadline(t *testing.T) {
	cancelled := make(chan struct{}, 1)
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		<-r.Context().Done()
		cancelled <- struct{}{}
	}))
	defer primary.Close()
	client := primary.Client()
	client.Timeout = 60 * time.Millisecond
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { testServiceDNSReply(t, w, r, dnsmessage.RCodeSuccess) }))
	defer fallback.Close()
	pool := newControlDNSPool([]controlDNSUpstream{{"slow", client, primary.URL}, {"healthy", fallback.Client(), fallback.URL}})
	start := time.Now()
	testServiceDNSLookup(t, pool, "slow.invalid")
	if time.Since(start) > time.Second {
		t.Fatal("fallback exceeded lookup deadline")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("timed-out request was not cancelled")
	}
}
func TestControlDNSUnderlyingHTTPSCauseReachesResolver(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := newControlDNSResolver(server.Client(), server.URL).LookupIP(ctx, "ip4", "failed.invalid")
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) || !strings.Contains(err.Error(), "HTTP 503") || strings.Contains(err.Error(), ": EOF") {
		t.Fatalf("HTTPS cause lost: %v", err)
	}
}
func TestControlDNSNXDOMAINDoesNotRetryButTransientFailuresDo(t *testing.T) {
	for _, rcode := range []dnsmessage.RCode{dnsmessage.RCodeNameError, dnsmessage.RCodeServerFailure, dnsmessage.RCodeRefused} {
		t.Run(rcode.String(), func(t *testing.T) {
			var fallbackCalls atomic.Int32
			primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { testServiceDNSReply(t, w, r, rcode) }))
			defer primary.Close()
			fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fallbackCalls.Add(1)
				testServiceDNSReply(t, w, r, dnsmessage.RCodeSuccess)
			}))
			defer fallback.Close()
			pool := newControlDNSPool([]controlDNSUpstream{{"primary", primary.Client(), primary.URL}, {"fallback", fallback.Client(), fallback.URL}})
			if rcode == dnsmessage.RCodeNameError {
				_, err := resolverForControlDNS(pool).LookupIP(context.Background(), "ip4", "missing.invalid")
				var dnsErr *net.DNSError
				if !errors.As(err, &dnsErr) || !dnsErr.IsNotFound || fallbackCalls.Load() != 0 {
					t.Fatalf("NXDOMAIN retried: %v", err)
				}
			} else {
				testServiceDNSLookup(t, pool, "transient.invalid")
				if fallbackCalls.Load() != 1 {
					t.Fatal("transient DNS failure not retried")
				}
			}
		})
	}
}
func TestControlDNSCancellationDoesNotLaunchFallback(t *testing.T) {
	var fallbackCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.ReadAll(r.Body); <-r.Context().Done() }))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackCalls.Add(1)
		testServiceDNSReply(t, w, r, dnsmessage.RCodeSuccess)
	}))
	defer fallback.Close()
	pool := newControlDNSPool([]controlDNSUpstream{{"primary", primary.Client(), primary.URL}, {"fallback", fallback.Client(), fallback.URL}})
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := resolverForControlDNS(pool).LookupIP(ctx, "ip4", "cancel-pool.invalid")
	if err == nil || time.Since(start) > time.Second || fallbackCalls.Load() != 0 {
		t.Fatalf("cancellation: %v, fallback=%d", err, fallbackCalls.Load())
	}
}

func TestControlDNSReadDeadlineDoesNotWaitForBlockedReply(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { testServiceDNSReply(t, w, r, dnsmessage.RCodeSuccess) }))
	defer server.Close()
	resolver := newControlDNSResolver(server.Client(), server.URL)
	conn, err := resolver.Dial(context.Background(), "tcp", "unused:53")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	query := dnsmessage.Message{Header: dnsmessage.Header{ID: 9, RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("deadline.invalid."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}}
	body, err := query.Pack()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Write(append([]byte{byte(len(body) >> 8), byte(len(body))}, body...)); err != nil {
		t.Fatal(err)
	}
	// An already expired caller deadline must also interrupt a blocked pipe write.
	if err = conn.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { var buf [2]byte; _, err := conn.Read(buf[:]); done <- err }()
	select {
	case err = <-done:
		var networkError net.Error
		if !errors.As(err, &networkError) || !networkError.Timeout() {
			t.Fatalf("read deadline lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("read deadline hung waiting for worker reply")
	}
}
