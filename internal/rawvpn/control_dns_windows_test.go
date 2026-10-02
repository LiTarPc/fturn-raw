//go:build windows

package rawvpn

import (
	"context"
	"golang.org/x/net/dns/dnsmessage"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestControlDNSWorksWithoutSystemOrTunnelDNS(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/dns-message" || r.Header.Get("Accept") != "application/dns-message" {
			t.Error("invalid DoH request")
		}
		data, _ := io.ReadAll(r.Body)
		var query dnsmessage.Message
		if err := query.Unpack(data); err != nil {
			t.Error(err)
			return
		}
		reply := dnsmessage.Message{Header: dnsmessage.Header{ID: query.ID, Response: true, RecursionAvailable: true}, Questions: query.Questions}
		q := query.Questions[0]
		if q.Type == dnsmessage.TypeA {
			reply.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60}, Body: &dnsmessage.AResource{A: [4]byte{192, 0, 2, 9}}}}
		}
		body, err := reply.Pack()
		if err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/dns-message")
		w.Write(body)
	}))
	defer server.Close()
	resolver := newControlDNSResolver(server.Client(), server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ips, err := resolver.LookupIP(ctx, "ip4", "only-doh.invalid")
	if err != nil || len(ips) != 1 || !ips[0].Equal(net.IPv4(192, 0, 2, 9)) {
		t.Fatalf("DoH lookup failed: %v", err)
	}
}
func TestControlDNSRejectsInvalidHTTPAndWireResponses(t *testing.T) {
	for _, kind := range []string{"status", "mime", "oversize", "id", "not-response"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/dns-message")
				if kind == "status" {
					w.WriteHeader(503)
					return
				}
				if kind == "mime" {
					w.Header().Set("Content-Type", "text/html")
				}
				if kind == "oversize" {
					w.Write(make([]byte, 65536))
					return
				}
				if kind == "id" {
					data[0] ^= 0xff
				}
				if kind != "not-response" {
					data[2] |= 0x80
				}
				w.Write(data)
			}))
			defer server.Close()
			resolver := newControlDNSResolver(server.Client(), server.URL)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := resolver.LookupIP(ctx, "ip4", "bad-doh.invalid"); err == nil {
				t.Fatal("invalid DoH answer accepted")
			}
		})
	}
}
func TestControlDNSCancellationDoesNotWaitForHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.ReadAll(r.Body); <-r.Context().Done() }))
	defer server.Close()
	resolver := newControlDNSResolver(server.Client(), server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := resolver.LookupIP(ctx, "ip4", "cancel-doh.invalid"); err == nil {
		t.Fatal("canceled query succeeded")
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation did not stop lookup")
	}
}

func TestControlDNSLiveUnderlay(t *testing.T) {
	value := os.Getenv("FTURN_RAW_TEST_CONTROL_INTERFACE")
	if value == "" {
		t.Skip("opt-in network test")
	}
	index, err := strconv.Atoi(value)
	if err != nil {
		t.Fatal("invalid interface index")
	}
	if err = BindControlInterface(index); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", "vk.ru")
	if err != nil || len(ips) == 0 {
		t.Fatalf("service DNS through selected underlay failed: %v", err)
	}
	t.Log("HTTPS service DNS resolved VK through the selected interface; no system routes changed")
}
