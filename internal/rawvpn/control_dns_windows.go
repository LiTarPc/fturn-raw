//go:build windows

package rawvpn

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/samosvalishe/free-turn-proxy/internal/logx"
	"github.com/samosvalishe/free-turn-proxy/internal/netctl"
)

type controlDNSLogger struct{ log logx.Logger }

var rawDNSLogger atomic.Pointer[controlDNSLogger]

func SetControlDNSLogger(logger logx.Logger) {
	rawDNSLogger.Store(&controlDNSLogger{logx.OrNop(logger)})
}
func serviceDNSLogger() logx.Logger {
	if value := rawDNSLogger.Load(); value != nil {
		return value.log
	}
	return logx.Nop()
}

// Keep the existing Cloudflare service and both existing bootstrap addresses.
// Retry the complete HTTPS exchange, not just TCP dialing. No other DNS service
// or system resolver is used. All sockets retain the selected underlay binding.
func controlDNSResolver() *net.Resolver {
	upstreams := []controlDNSUpstream{
		controlDoHUpstream("Cloudflare 1.1.1.1", "1.1.1.1:443"),
		controlDoHUpstream("Cloudflare 1.0.0.1", "1.0.0.1:443"),
	}
	return resolverForControlDNS(newControlDNSPool(upstreams))
}
func controlDoHUpstream(name, address string) controlDNSUpstream {
	dialer := &net.Dialer{Timeout: time.Second, Control: netctl.Apply}
	transport := &http.Transport{
		Proxy: nil, ForceAttemptHTTP2: true,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "cloudflare-dns.com"},
		TLSHandshakeTimeout: 4 * time.Second, ResponseHeaderTimeout: 4 * time.Second,
		IdleConnTimeout: 60 * time.Second, MaxIdleConns: 4,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp4", address)
		},
	}
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return controlDNSUpstream{name: name, client: client, endpoint: "https://cloudflare-dns.com/dns-query"}
}
func newControlDNSResolver(client *http.Client, endpoint string) *net.Resolver {
	return resolverForControlDNS(newControlDNSPool([]controlDNSUpstream{{name: "custom", client: client, endpoint: endpoint}}))
}

type controlDNSExchanger interface {
	exchange(context.Context, []byte) ([]byte, error)
}

func resolverForControlDNS(pool controlDNSExchanger) *net.Resolver {
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		queryCtx, cancel := context.WithCancel(ctx)
		caller, worker := net.Pipe()
		result := make(chan error, 1)
		go func() {
			stop := context.AfterFunc(queryCtx, func() { _ = worker.Close() })
			defer stop()
			err := serveControlDNSPool(queryCtx, worker, pool)
			// Publish the cause before EOF; otherwise net.Resolver loses HTTPS errors.
			result <- err
			_ = worker.Close()
		}()
		return &controlDNSConn{Conn: caller, cancel: cancel, result: result}, nil
	}}
}

type controlDNSConn struct {
	net.Conn
	cancel  context.CancelFunc
	result  <-chan error
	once    sync.Once
	failure error
}

func (c *controlDNSConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if err != nil {
		c.cancel() // Release a worker blocked writing after the caller read deadline.
		c.once.Do(func() { c.failure = <-c.result })
		if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
			return n, err
		}
		if c.failure != nil {
			return n, &controlDNSError{cause: c.failure}
		}
	}
	return n, err
}
func (c *controlDNSConn) Close() error { c.cancel(); return c.Conn.Close() }

type controlDNSError struct{ cause error }

func (e *controlDNSError) Error() string {
	return "raw service DNS: " + strings.ReplaceAll(e.cause.Error(), "\n", "; ")
}
func (e *controlDNSError) Unwrap() error   { return e.cause }
func (e *controlDNSError) Temporary() bool { return true }
func (e *controlDNSError) Timeout() bool {
	if errors.Is(e.cause, context.DeadlineExceeded) {
		return true
	}
	var networkError net.Error
	return errors.As(e.cause, &networkError) && networkError.Timeout()
}
func serveControlDNS(ctx context.Context, conn net.Conn, client *http.Client, endpoint string) error {
	return serveControlDNSPool(ctx, conn, newControlDNSPool([]controlDNSUpstream{{name: "custom", client: client, endpoint: endpoint}}))
}
func serveControlDNSPool(ctx context.Context, conn net.Conn, pool controlDNSExchanger) error {
	var prefix [2]byte
	if _, err := io.ReadFull(conn, prefix[:]); err != nil {
		return err
	}
	length := int(binary.BigEndian.Uint16(prefix[:]))
	if length < 12 {
		return fmt.Errorf("short DNS query")
	}
	query := make([]byte, length)
	if _, err := io.ReadFull(conn, query); err != nil {
		return err
	}
	body, err := pool.exchange(ctx, query)
	if err != nil {
		return err
	}
	binary.BigEndian.PutUint16(prefix[:], uint16(len(body)))
	_, err = conn.Write(append(prefix[:], body...))
	return err
}
