package rawvpn

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSameWorkerReconnectRetiresOldRelay(t *testing.T) {
	key := testKey(t)
	serverPC := udpSocket(t)
	dev, _, _ := startRawTestServer(t, serverPC, key)
	cfg := ClientConfig{Address: netip.MustParseAddr("10.77.0.2"), MTU: 1280, Profile: "rtpopus2", Key: key}
	cfg.group[0] = 1
	cfg.workerToken[0] = 1
	old := udpSocket(t)
	oldCodec, _, err := clientHandshake(context.Background(), old, serverPC.LocalAddr(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	healthy := udpSocket(t)
	healthyCfg := cfg
	healthyCfg.workerToken[0] = 2
	healthyCodec, _, err := clientHandshake(context.Background(), healthy, serverPC.LocalAddr(), healthyCfg)
	if err != nil {
		t.Fatal(err)
	}
	replacement := udpSocket(t)
	newCodec, _, err := clientHandshake(context.Background(), replacement, serverPC.LocalAddr(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Old encrypted traffic is rejected, while the successor and unrelated worker still work.
	for _, v := range []struct {
		pc    net.PacketConn
		c     *codec
		value byte
	}{{old, oldCodec, 1}, {replacement, newCodec, 2}, {healthy, healthyCodec, 3}} {
		packet := ipPacket("10.77.0.2", "1.1.1.1", 64)
		packet[63] = v.value
		if err := writeFrame(v.pc, serverPC.LocalAddr(), v.c, make([]byte, maxWire), append([]byte{frameData}, packet...)); err != nil {
			t.Fatal(err)
		}
		if v.value != 1 {
			readPacket(t, dev, packet)
		}
	}
	// Every downlink packet reaches an active relay; none leaks to the old relay.
	down := ipPacket("1.1.1.1", "10.77.0.2", 64)
	for range 32 {
		dev.input <- down
	}
	total := 0
	for _, v := range []struct {
		pc net.PacketConn
		c  *codec
	}{{replacement, newCodec}, {healthy, healthyCodec}} {
		_ = v.pc.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
		for {
			b := make([]byte, maxWire)
			n, _, err := v.pc.ReadFrom(b)
			if err != nil {
				break
			}
			plain, err := v.c.decode(make([]byte, maxFrame), b[:n])
			if err == nil && len(plain) > 1 && plain[0] == frameData && bytes.Equal(plain[1:], down) {
				total++
			}
		}
	}
	if total != 32 {
		t.Fatalf("only %d/32 downlink packets reached current workers", total)
	}
	_ = old.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	b := make([]byte, maxWire)
	if n, _, err := old.ReadFrom(b); err == nil {
		plain, _ := oldCodec.decode(make([]byte, maxFrame), b[:n])
		if len(plain) > 1 && plain[0] == frameData {
			t.Fatal("downlink sent to retired relay")
		}
	}
}

func TestRetirementNoticeCannotRemoveSuccessorOrOtherGroup(t *testing.T) {
	key := testKey(t)
	pc := udpSocket(t)
	s := &rawServer{cfg: ServerConfig{ClientAddress: netip.MustParseAddr("10.77.0.2"), MTU: 1280}, pc: pc, dev: newTestDevice(), sessions: map[string]*serverSession{}}
	// Use a real logger fallback just as RunServer does.
	s.cfg.Log = nopTestLogger{}
	worker := func(id byte) *serverSession {
		c, _ := newCodec("rtpopus2", key, true, nil)
		w := &serverSession{addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 20000 + int(id)}, codec: c, active: true, seen: time.Now()}
		w.group[0] = 1
		w.clientToken[0] = id
		w.workerToken[0] = id
		w.serverToken[0] = id
		s.sessions[w.addr.String()] = w
		s.order = append(s.order, w.addr.String())
		return w
	}
	healthy := worker(1)
	old := worker(2)
	successor := worker(3)
	successor.workerToken = old.workerToken
	foreign := worker(4)
	foreign.group[0] = 2
	msg := make([]byte, 33)
	msg[0] = frameRetire
	copy(msg[1:17], old.workerToken[:])
	copy(msg[17:], old.serverToken[:])
	if err := s.sessionPacket(healthy, msg); err != nil {
		t.Fatal(err)
	}
	if s.sessions[old.addr.String()] != nil {
		t.Fatal("failed worker remained active")
	}
	for range 2 {
		if err := s.sessionPacket(healthy, msg); err != nil {
			t.Fatal(err)
		}
	}
	if s.sessions[successor.addr.String()] != successor {
		t.Fatal("late retirement removed successor")
	}
	copy(msg[1:17], foreign.workerToken[:])
	copy(msg[17:], foreign.serverToken[:])
	_ = s.sessionPacket(healthy, msg)
	if s.sessions[foreign.addr.String()] != foreign {
		t.Fatal("retirement crossed client process group")
	}
	if s.sessions[healthy.addr.String()] != healthy {
		t.Fatal("healthy sender was removed")
	}
}

type nopTestLogger struct{}

func (nopTestLogger) Debugf(string, ...any) {}
func (nopTestLogger) Infof(string, ...any)  {}
func (nopTestLogger) Warnf(string, ...any)  {}
func (nopTestLogger) Errorf(string, ...any) {}
func (nopTestLogger) DebugEnabled() bool    { return false }

// Real TURN over TCP: reset one connection during traffic, then verify recovery.
func TestTCPResetRecoveryThroughTURN(t *testing.T) {
	turnAddr := startTURN(t, false)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var mu sync.Mutex
	var sockets []net.Conn
	go func() {
		for {
			incoming, err := listener.Accept()
			if err != nil {
				return
			}
			outgoing, err := net.Dial("tcp", turnAddr)
			if err != nil {
				incoming.Close()
				continue
			}
			mu.Lock()
			sockets = append(sockets, incoming)
			mu.Unlock()
			go func() {
				defer incoming.Close()
				defer outgoing.Close()
				var wg sync.WaitGroup
				wg.Add(2)
				go func() { defer wg.Done(); copySocket(outgoing, incoming); outgoing.Close() }()
				go func() { defer wg.Done(); copySocket(incoming, outgoing); incoming.Close() }()
				wg.Wait()
			}()
		}
	}()
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range sockets {
			c.Close()
		}
	}()
	serverPC := udpSocket(t)
	key := testKey(t)
	dev, _, _ := startRawTestServer(t, serverPC, key)
	client := newTestDevice()
	peer := serverPC.LocalAddr().(*net.UDPAddr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var connected atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- RunClient(ctx, client, ClientConfig{Peer: peer, Address: netip.MustParseAddr("10.77.0.2"), MTU: 1280, Profile: "rtpopus2", Key: key, Streams: 3, Provider: staticProvider{addr: listener.Addr().String()}, Connected: &connected})
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("client failed to stop")
		}
	}()
	deadline := time.Now().Add(8 * time.Second)
	for connected.Load() != 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if connected.Load() != 3 {
		t.Fatalf("connected=%d", connected.Load())
	}
	mu.Lock()
	broken := sockets[0]
	mu.Unlock()
	if c, ok := broken.(*net.TCPConn); ok {
		_ = c.SetLinger(0)
	}
	broken.Close()
	// A ping or outgoing packet exposes the reset immediately.
	up := ipPacket("10.77.0.2", "1.1.1.1", 64)
	for range 20 {
		client.input <- up
	}
	deadline = time.Now().Add(18 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := len(sockets)
		mu.Unlock()
		if count > 3 && connected.Load() == 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	count := len(sockets)
	mu.Unlock()
	if count <= 3 || connected.Load() != 3 {
		t.Fatalf("reset was not recovered: sockets=%d connected=%d", count, connected.Load())
	}
	down := ipPacket("1.1.1.1", "10.77.0.2", 64)
	for i := range 48 {
		packet := append([]byte{}, down...)
		packet[63] = byte(i)
		dev.input <- packet
	}
	seen := map[byte]bool{}
	timeout := time.After(3 * time.Second)
	for len(seen) < 48 {
		select {
		case packet := <-client.output:
			seen[packet[63]] = true
		case <-timeout:
			t.Fatal(fmt.Sprintf("post-reset downlink lost %d/48 packets", 48-len(seen)))
		}
	}
}
func copySocket(dst, src net.Conn) {
	buf := make([]byte, 32768)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, e := dst.Write(buf[:n]); e != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func TestSameWorkerHandshakeKeepsFreshSessionKeys(t *testing.T) {
	key := testKey(t)
	pc := udpSocket(t)
	_, _, _ = startRawTestServer(t, pc, key)
	client := udpSocket(t)
	cfg := ClientConfig{Address: netip.MustParseAddr("10.77.0.2"), MTU: 1280, Profile: "rtpopus2", Key: key}
	cfg.group[0] = 1
	cfg.workerToken[0] = 1
	old, _, err := clientHandshake(context.Background(), client, pc.LocalAddr(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := clientHandshake(context.Background(), client, pc.LocalAddr(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if old.serverToken == current.serverToken {
		t.Fatal("session nonce reused on same relay address")
	}
	wire := make([]byte, maxWire)
	n, err := old.encode(wire, []byte{framePong})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := current.decode(make([]byte, maxFrame), wire[:n]); err == nil {
		t.Fatal("old session ciphertext accepted after reconnect")
	}
}

func TestReplacementAtWorkerLimit(t *testing.T) {
	key := testKey(t)
	s := &rawServer{cfg: ServerConfig{Log: nopTestLogger{}}, pc: udpSocket(t), sessions: map[string]*serverSession{}}
	var group [16]byte
	group[0] = 1
	for i := 1; i <= 64; i++ {
		c, _ := newCodec("rtpopus2", key, true, nil)
		w := &serverSession{addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 22000 + i}, codec: c, active: true, group: group, seen: time.Now()}
		w.workerToken[0] = byte(i)
		s.sessions[w.addr.String()] = w
		s.order = append(s.order, w.addr.String())
	}
	c, _ := newCodec("rtpopus2", key, true, nil)
	replacement := &serverSession{addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 23001}, codec: c, group: group}
	replacement.workerToken[0] = 1
	replacement.serverToken[0] = 9
	s.sessions[replacement.addr.String()] = replacement
	s.order = append(s.order, replacement.addr.String())
	confirm := append([]byte{frameConfirm}, replacement.serverToken[:]...)
	if err := s.sessionPacket(replacement, confirm); err != nil {
		t.Fatal(err)
	}
	if !replacement.active || len(s.sessions) != 64 {
		t.Fatalf("replacement at cap failed: active=%v workers=%d", replacement.active, len(s.sessions))
	}
}
