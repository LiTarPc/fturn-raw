package session

import (
	"context"
	"encoding/binary"
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

func TestRawServiceDNSUsedByProviderAndGlobalLookups(t *testing.T) {
	original := net.DefaultResolver
	defer func() { net.DefaultResolver = original }()
	var queries atomic.Int32
	control := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		caller, server := net.Pipe()
		go func() {
			defer server.Close()
			var prefix [2]byte
			if _, err := io.ReadFull(server, prefix[:]); err != nil {
				return
			}
			data := make([]byte, binary.BigEndian.Uint16(prefix[:]))
			if _, err := io.ReadFull(server, data); err != nil {
				return
			}
			var query dnsmessage.Message
			if query.Unpack(data) != nil || len(query.Questions) != 1 {
				return
			}
			queries.Add(1)
			q := query.Questions[0]
			reply := dnsmessage.Message{Header: dnsmessage.Header{ID: query.ID, Response: true, RecursionAvailable: true}, Questions: query.Questions}
			if q.Type == dnsmessage.TypeA {
				reply.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 1}, Body: &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}}}
			}
			wire, err := reply.Pack()
			if err != nil {
				return
			}
			binary.BigEndian.PutUint16(prefix[:], uint16(len(wire)))
			server.Write(append(prefix[:], wire...))
		}()
		return caller, nil
	}}
	for _, mode := range []string{"auto", "plain", "doh"} {
		t.Run(mode, func(t *testing.T) {
			dialer := rawServiceDialer(mode, control)
			if dialer.Resolver != control || net.DefaultResolver != control {
				t.Fatal("session replaced control resolver")
			}
			if dialer.Control == nil {
				t.Fatal("underlay socket binding lost")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", "turn-control.invalid")
			if err != nil || len(ips) != 1 || !ips[0].IsLoopback() {
				t.Fatal("global TURN lookup did not use service resolver", err)
			}
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
			defer httpServer.Close()
			transport := &http.Transport{DialContext: dialer.DialContext}
			defer transport.CloseIdleConnections()
			request, _ := http.NewRequestWithContext(ctx, "GET", strings.Replace(httpServer.URL, "127.0.0.1", "vk-control.invalid", 1), nil)
			response, err := (&http.Client{Transport: transport}).Do(request)
			if err != nil {
				t.Fatal("VK provider dialer did not use service resolver", err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				t.Fatal("unexpected provider response")
			}
		})
	}
	if queries.Load() < 6 {
		t.Fatal("expected both provider and global DNS lookups")
	}
}
