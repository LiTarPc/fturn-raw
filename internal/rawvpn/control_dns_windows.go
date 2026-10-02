//go:build windows

package rawvpn

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"time"

	"github.com/samosvalishe/free-turn-proxy/internal/netctl"
)

// Only this Raw process uses DoH through the selected underlay. Windows and
// browser DNS still follow the TUN's policy. This breaks the circular dependency
// between reconnecting VK/TURN and DNS carried by an unavailable Raw tunnel.
func controlDNSResolver() *net.Resolver {
	dialer := &net.Dialer{Timeout: 3 * time.Second, Control: netctl.Apply}
	transport := &http.Transport{
		Proxy: nil, ForceAttemptHTTP2: true,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "cloudflare-dns.com"},
		TLSHandshakeTimeout: 4 * time.Second, ResponseHeaderTimeout: 5 * time.Second,
		IdleConnTimeout: 60 * time.Second, MaxIdleConns: 4,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var last error
			for _, address := range []string{"1.1.1.1:443", "1.0.0.1:443"} {
				c, err := dialer.DialContext(ctx, "tcp4", address)
				if err == nil {
					return c, nil
				}
				last = err
			}
			return nil, last
		},
	}
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return newControlDNSResolver(client, "https://cloudflare-dns.com/dns-query")
}
func newControlDNSResolver(client *http.Client, endpoint string) *net.Resolver {
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		caller, worker := net.Pipe()
		go func() {
			defer worker.Close()
			cancelClose := context.AfterFunc(ctx, func() { _ = worker.Close() })
			defer cancelClose()
			_ = serveControlDNS(ctx, worker, client, endpoint)
		}()
		// net.Pipe is a stream Conn, so Go frames each query as DNS over TCP.
		return caller, nil
	}}
}
func serveControlDNS(ctx context.Context, conn net.Conn, client *http.Client, endpoint string) error {
	var prefix [2]byte
	if _, err := io.ReadFull(conn, prefix[:]); err != nil {
		return err
	}
	length := int(binary.BigEndian.Uint16(prefix[:]))
	if length < 12 {
		return fmt.Errorf("raw control DNS: short query")
	}
	query := make([]byte, length)
	if _, err := io.ReadFull(conn, query); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(query))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/dns-message")
	request.Header.Set("Accept", "application/dns-message")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || err != nil || media != "application/dns-message" {
		return fmt.Errorf("raw control DNS: invalid HTTPS response")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 65536))
	if err != nil {
		return err
	}
	if len(body) < 12 || len(body) > 65535 || !bytes.Equal(body[:2], query[:2]) || body[2]&0x80 == 0 {
		return fmt.Errorf("raw control DNS: invalid DNS response")
	}
	binary.BigEndian.PutUint16(prefix[:], uint16(len(body)))
	_, err = conn.Write(append(prefix[:], body...))
	return err
}
