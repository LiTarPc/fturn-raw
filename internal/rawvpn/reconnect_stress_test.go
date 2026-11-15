package rawvpn

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samosvalishe/free-turn-proxy/internal/provider"
)

// A real local TURN TCP connection is reset, rather than replacing the provider
// with a mock. The same client process and logical workers must recover.
type resetTURNProxy struct {
	listener net.Listener
	mu       sync.Mutex
	active   map[net.Conn]struct{}
	accepted int
	times    []time.Time
	wg       sync.WaitGroup
}

func newResetTURNProxy(t *testing.T, target string) *resetTURNProxy {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &resetTURNProxy{listener: l, active: make(map[net.Conn]struct{})}
	p.wg.Go(func() {
		for {
			incoming, err := l.Accept()
			if err != nil {
				return
			}
			outgoing, err := net.DialTimeout("tcp4", target, time.Second)
			if err != nil {
				incoming.Close()
				continue
			}
			p.mu.Lock()
			p.active[incoming] = struct{}{}
			p.accepted++
			p.times = append(p.times, time.Now())
			p.mu.Unlock()
			p.wg.Go(func() {
				defer incoming.Close()
				defer outgoing.Close()
				defer func() { p.mu.Lock(); delete(p.active, incoming); p.mu.Unlock() }()
				var copies sync.WaitGroup
				copies.Go(func() { copySocket(outgoing, incoming); outgoing.Close() })
				copies.Go(func() { copySocket(incoming, outgoing); incoming.Close() })
				copies.Wait()
			})
		}
	})
	t.Cleanup(func() {
		l.Close()
		p.reset(0)
		done := make(chan struct{})
		go func() { p.wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("TURN fault proxy failed to stop")
		}
	})
	return p
}

func (p *resetTURNProxy) reset(count int) int {
	p.mu.Lock()
	var conns []net.Conn
	for c := range p.active {
		conns = append(conns, c)
		if count > 0 && len(conns) == count {
			break
		}
	}
	p.mu.Unlock()
	for _, c := range conns {
		if tcp, ok := c.(*net.TCPConn); ok {
			tcp.SetLinger(0)
		}
		c.Close()
	}
	return len(conns)
}

func (p *resetTURNProxy) total() int { p.mu.Lock(); defer p.mu.Unlock(); return p.accepted }

type recoveryLogger struct {
	nopTestLogger
	closed atomic.Int32
}

func (l *recoveryLogger) Infof(format string, args ...any) {
	var id int
	if _, err := fmt.Sscanf(fmt.Sprintf(format, args...), "RAW stream %d closed", &id); err == nil {
		l.closed.Add(1)
	}
}

func waitRecovery(t *testing.T, budget time.Duration, description string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func verifyRecoveryTraffic(t *testing.T, client, server *testDevice) {
	t.Helper()
	// Valid TCP headers exercise pinned TCP-flow dispatch as well as UDP/shared
	// dispatch. Unique sequence numbers make stale queued packets detectable.
	for i := range 24 {
		up := ipPacket("10.77.0.2", "203.0.113.9", 96)
		up[9] = 6
		binary.BigEndian.PutUint16(up[20:22], uint16(30000+i))
		binary.BigEndian.PutUint16(up[22:24], 443)
		up[32] = 5 << 4
		client.input <- up
		readPacket(t, server, up)
		down := ipPacket("203.0.113.9", "10.77.0.2", 96)
		down[95] = byte(i)
		server.input <- down
		readPacket(t, client, down)
	}
}

func TestReconnect21WorkersAfterRepeatedTCPResets(t *testing.T) {
	for _, mode := range []string{UplinkPacket, UplinkTCPFlow} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			proxy := newResetTURNProxy(t, startTURN(t, false))
			serverPC := udpSocket(t)
			key := testKey(t)
			server, _, _ := startRawTestServer(t, serverPC, key)
			client := newTestDevice()
			ctx, cancel := context.WithCancel(context.Background())
			var connected atomic.Int32
			log := &recoveryLogger{}
			done := make(chan error, 1)
			go func() {
				done <- RunClient(ctx, client, ClientConfig{Peer: serverPC.LocalAddr().(*net.UDPAddr), Address: netip.MustParseAddr("10.77.0.2"), MTU: 1280, Profile: "rtpopus2", Key: key, Streams: 21, Provider: staticProvider{addr: proxy.listener.Addr().String()}, Connected: &connected, UplinkMode: mode, Log: log})
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(4 * time.Second):
					t.Error("21-worker cancellation stalled")
				}
			})
			waitRecovery(t, 15*time.Second, "initial 21 workers", func() bool { return connected.Load() == 21 })
			verifyRecoveryTraffic(t, client, server)
			for _, count := range []int{1, 7, 21, 21} {
				before, closed := proxy.total(), log.closed.Load()
				if n := proxy.reset(count); n != count {
					t.Fatalf("reset %d/%d active connections", n, count)
				}
				started := time.Now()
				waitRecovery(t, 25*time.Second, fmt.Sprintf("%d reset workers", count), func() bool {
					return proxy.total() >= before+count && log.closed.Load() >= closed+int32(count) && connected.Load() == 21
				})
				verifyRecoveryTraffic(t, client, server)
				t.Logf("reset=%d recovered=21/21 elapsed=%s", count, time.Since(started).Round(time.Millisecond))
			}
		})
	}
}

type gatedRecoveryConn struct {
	net.PacketConn
	drop atomic.Bool
}

func (p *gatedRecoveryConn) ReadFrom(b []byte) (int, net.Addr, error) {
	for {
		n, a, err := p.PacketConn.ReadFrom(b)
		if err != nil || !p.drop.Load() {
			return n, a, err
		}
	}
}

func (p *gatedRecoveryConn) WriteTo(b []byte, a net.Addr) (int, error) {
	if p.drop.Load() {
		return len(b), nil
	}
	return p.PacketConn.WriteTo(b, a)
}

func TestReconnectAfterSilentUDPLoss(t *testing.T) {
	turnAddr := startTURN(t, true)
	pc := &gatedRecoveryConn{PacketConn: udpSocket(t)}
	key := testKey(t)
	server, _, _ := startRawTestServer(t, pc, key)
	client := newTestDevice()
	ctx, cancel := context.WithCancel(context.Background())
	var connected atomic.Int32
	log := &recoveryLogger{}
	done := make(chan error, 1)
	go func() {
		done <- RunClient(ctx, client, ClientConfig{Peer: pc.LocalAddr().(*net.UDPAddr), Address: netip.MustParseAddr("10.77.0.2"), MTU: 1280, Profile: "rtpopus3", Key: key, Streams: 7, TransportUDP: true, Provider: staticProvider{addr: turnAddr}, Connected: &connected, UplinkMode: UplinkTCPFlow, Log: log})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(4 * time.Second):
			t.Error("UDP recovery client failed to stop")
		}
	})
	waitRecovery(t, 10*time.Second, "initial UDP workers", func() bool { return connected.Load() == 7 })
	verifyRecoveryTraffic(t, client, server)
	pc.drop.Store(true)
	started := time.Now()
	waitRecovery(t, 40*time.Second, "silent-loss detection", func() bool { return connected.Load() == 0 })
	pc.drop.Store(false)
	waitRecovery(t, 15*time.Second, "all UDP workers reconnect", func() bool { return connected.Load() == 7 && log.closed.Load() >= 7 })
	verifyRecoveryTraffic(t, client, server)
	t.Logf("silent UDP loss detected and recovered=7/7 elapsed=%s", time.Since(started).Round(time.Millisecond))
}

type delayedRecoveryProvider struct {
	staticProvider
	arrived chan struct{}
	release chan struct{}
}

func (p *delayedRecoveryProvider) GetCredentials(ctx context.Context, id int) (provider.Credentials, error) {
	p.arrived <- struct{}{}
	select {
	case <-ctx.Done():
		return provider.Credentials{}, ctx.Err()
	case <-p.release:
		return p.staticProvider.GetCredentials(ctx, id)
	}
}

// Credential refresh or a browser check can hold a whole group. Pacing before
// GetCredentials must not turn into an Allocate burst when that group unblocks.
func TestReconnectAllocationPacingSurvivesCredentialWait(t *testing.T) {
	const workers = 7
	proxy := newResetTURNProxy(t, startTURN(t, false))
	pc := udpSocket(t)
	key := testKey(t)
	_, _, _ = startRawTestServer(t, pc, key)
	auth := &delayedRecoveryProvider{staticProvider: staticProvider{addr: proxy.listener.Addr().String()}, arrived: make(chan struct{}, workers), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	var connected atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- RunClient(ctx, newTestDevice(), ClientConfig{Peer: pc.LocalAddr().(*net.UDPAddr), Address: netip.MustParseAddr("10.77.0.2"), MTU: 1280, Profile: "rtpopus2", Key: key, Streams: workers, Provider: auth, Connected: &connected})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(4 * time.Second):
			t.Error("credential wait client failed to stop")
		}
	})
	for range workers {
		select {
		case <-auth.arrived:
		case <-time.After(5 * time.Second):
			t.Fatal("workers did not reach shared credential wait")
		}
	}
	close(auth.release)
	waitRecovery(t, 8*time.Second, "paced allocation after credentials", func() bool { return connected.Load() == workers })
	proxy.mu.Lock()
	times := append([]time.Time(nil), proxy.times...)
	proxy.mu.Unlock()
	if len(times) != workers {
		t.Fatalf("unexpected retries: accepted=%d", len(times))
	}
	span := times[len(times)-1].Sub(times[0])
	if span < time.Duration(workers-1)*150*time.Millisecond {
		t.Fatalf("credential wait bypassed allocation pacing: %d TURN connections opened in %s", workers, span)
	}
	t.Logf("%d connections after shared credential wait spread over %s", workers, span.Round(time.Millisecond))
}

type cachedRecoveryProvider struct {
	staticProvider
	until atomic.Int64
}

func (p *cachedRecoveryProvider) BackoffUntilUnix() int64 { return p.until.Load() }

func TestReconnectCachedWorkerDuringAuthorizationPause(t *testing.T) {
	proxy := newResetTURNProxy(t, startTURN(t, false))
	pc := udpSocket(t)
	key := testKey(t)
	server, _, _ := startRawTestServer(t, pc, key)
	client := newTestDevice()
	auth := &cachedRecoveryProvider{staticProvider: staticProvider{addr: proxy.listener.Addr().String()}}
	ctx, cancel := context.WithCancel(context.Background())
	var connected atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- RunClient(ctx, client, ClientConfig{Peer: pc.LocalAddr().(*net.UDPAddr), Address: netip.MustParseAddr("10.77.0.2"), MTU: 1280, Profile: "rtpopus2", Key: key, Streams: 1, Provider: auth, Connected: &connected})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(4 * time.Second):
			t.Error("cached worker failed to cancel")
		}
	})
	waitRecovery(t, 8*time.Second, "initial cached worker", func() bool { return connected.Load() == 1 })
	// Another credential group hits an authorization cooldown. This worker still
	// has valid cached TURN credentials and must be able to replace its socket.
	auth.until.Store(time.Now().Add(5 * time.Minute).Unix())
	before := proxy.total()
	proxy.reset(1)
	waitRecovery(t, 18*time.Second, "cached worker recovery during auth cooldown", func() bool { return proxy.total() > before && connected.Load() == 1 })
	verifyRecoveryTraffic(t, client, server)
}
