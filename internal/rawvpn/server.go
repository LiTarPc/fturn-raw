package rawvpn

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/samosvalishe/free-turn-proxy/internal/logx"
)

type ServerConfig struct {
	ClientAddress netip.Addr
	MTU           int
	Profile       string
	Key           []byte
	Log           logx.Logger
}

type serverSession struct {
	addr                     net.Addr
	clientToken, serverToken [16]byte
	workerToken              [16]byte
	group                    [16]byte
	codec                    *codec
	active                   bool
	seen                     time.Time
}

type rawServer struct {
	cfg          ServerConfig
	pc           net.PacketConn
	dev          Device
	base         *codec
	mu           sync.Mutex
	sessions     map[string]*serverSession
	order        []string
	index, chunk int
	writeMu      sync.Mutex
}

// RunServer serves one provisioned IPv4 client with up to 64 parallel workers.
// Both pc and dev are closed when ctx is cancelled or either packet loop fails.
func RunServer(ctx context.Context, pc net.PacketConn, dev Device, cfg ServerConfig) error {
	if !cfg.ClientAddress.Is4() {
		return errors.New("raw: server requires a provisioned IPv4 client address")
	}
	if err := validateMTU(cfg.MTU); err != nil {
		return err
	}
	base, err := newCodec(cfg.Profile, cfg.Key, true, nil)
	if err != nil {
		return err
	}
	cfg.Log = logx.OrNop(cfg.Log)
	s := &rawServer{cfg: cfg, pc: pc, dev: dev, base: base, sessions: make(map[string]*serverSession)}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(runCtx, func() { _ = pc.Close(); _ = dev.Close() })
	defer stop()
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Go(func() { errCh <- s.uplink(runCtx) })
	wg.Go(func() { errCh <- s.downlink(runCtx) })
	wg.Go(func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case now := <-ticker.C:
				s.expire(now)
			}
		}
	})
	select {
	case <-runCtx.Done():
	case err = <-errCh:
	}
	wasCancelled := ctx.Err() != nil
	cancel()
	wg.Wait()
	if wasCancelled {
		return nil
	}
	return err
}

func (s *rawServer) send(addr net.Addr, c *codec, plain []byte) error {
	// Serialize writes and socket deadlines. RTP sender counters are per codec.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var wireBuf [maxWire]byte
	return writeFrame(s.pc, addr, c, wireBuf[:], plain)
}

func (s *rawServer) uplink(ctx context.Context) error {
	wireBuf, plainBuf := make([]byte, maxWire+1), make([]byte, maxFrame)
	for {
		n, addr, err := s.pc.ReadFrom(wireBuf)
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.mu.Lock()
		worker := s.sessions[addr.String()]
		s.mu.Unlock()
		if worker != nil {
			plain, err := worker.codec.decode(plainBuf, wireBuf[:n])
			if err == nil && len(plain) != 0 {
				if err := s.sessionPacket(worker, plain); err != nil {
					return err
				}
				continue
			}
		}
		plain, err := s.base.decode(plainBuf, wireBuf[:n])
		if err != nil || (len(plain) != 39 && len(plain) != 55) || plain[0] != frameHello {
			continue
		}
		if err := s.hello(addr, plain); err != nil {
			s.cfg.Log.Debugf("RAW hello rejected: %v", err)
		}
	}
}

func (s *rawServer) hello(addr net.Addr, plain []byte) error {
	ip := netip.AddrFrom4([4]byte(plain[33:37]))
	mtu := int(binary.BigEndian.Uint16(plain[37:39]))
	if ip != s.cfg.ClientAddress || mtu != s.cfg.MTU {
		return errors.New("raw: client IP or MTU does not match server provisioning")
	}
	var token [16]byte
	copy(token[:], plain[1:17])
	var group [16]byte
	copy(group[:], plain[17:33])
	var logical [16]byte
	if len(plain) == 55 {
		copy(logical[:], plain[39:55])
		if logical == ([16]byte{}) {
			return errors.New("raw: empty worker identity")
		}
	}
	s.mu.Lock()
	worker := s.sessions[addr.String()]
	if worker == nil || worker.clientToken != token || worker.group != group {
		if worker == nil && len(s.sessions) >= 128 {
			s.mu.Unlock()
			return errors.New("raw: worker limit reached")
		}
		worker = &serverSession{addr: addr, clientToken: token, workerToken: logical, group: group, seen: time.Now()}
		if _, err := rand.Read(worker.serverToken[:]); err != nil {
			s.mu.Unlock()
			return err
		}
		tokens := append(append([]byte(nil), token[:]...), worker.serverToken[:]...)
		tokens = append(tokens, group[:]...)
		if logical != ([16]byte{}) {
			tokens = append(tokens, logical[:]...)
		}
		var err error
		worker.codec, err = newCodec(s.cfg.Profile, s.cfg.Key, true, tokens)
		if err != nil {
			s.mu.Unlock()
			return err
		}
		if s.sessions[addr.String()] == nil {
			s.order = append(s.order, addr.String())
		}
		s.sessions[addr.String()] = worker
	}
	s.mu.Unlock()
	challenge := make([]byte, 35)
	challenge[0] = frameChallenge
	copy(challenge[1:17], worker.clientToken[:])
	copy(challenge[17:33], worker.serverToken[:])
	binary.BigEndian.PutUint16(challenge[33:], uint16(s.cfg.MTU))
	return s.send(addr, s.base, challenge)
}

func (s *rawServer) sessionPacket(worker *serverSession, plain []byte) error {
	s.mu.Lock()
	active := worker.active
	s.mu.Unlock()
	if plain[0] == frameConfirm {
		if len(plain) != 17 || !bytes.Equal(plain[1:], worker.serverToken[:]) {
			return nil
		}
		s.mu.Lock()
		if !worker.active {
			activeCount := 0
			for _, existing := range s.sessions {
				if existing.active && existing.group == worker.group && (worker.workerToken == ([16]byte{}) || existing.workerToken != worker.workerToken) {
					activeCount++
				}
			}
			if activeCount >= 64 {
				s.mu.Unlock()
				return nil
			}
		}
		// One key provisions one client. A new client process retires all old
		// workers after proving possession of a fresh, challenged session key.
		for address, old := range s.sessions {
			if old.group != worker.group || (old != worker && worker.workerToken != ([16]byte{}) && old.workerToken == worker.workerToken) {
				s.cfg.Log.Infof("RAW worker retired on authenticated replacement: %s -> %s", old.addr, worker.addr)
				s.remove(address)
			}
		}
		worker.active, worker.seen = true, time.Now()
		s.mu.Unlock()
		if !active {
			s.cfg.Log.Infof("RAW authenticated worker %s for %s", worker.addr, s.cfg.ClientAddress)
		}
		s.sendSessionControl(worker, frameReady)
		return nil
	}
	if !active {
		return nil
	}
	switch plain[0] {
	case frameData, frameFragment:
		packet, err := worker.codec.fragments.accept(plain, s.cfg.MTU, time.Now())
		if err != nil || packet == nil {
			return nil
		}
		src, _, err := ipv4(packet, s.cfg.MTU)
		if err != nil || src != s.cfg.ClientAddress {
			return nil
		}
		s.mu.Lock()
		worker.seen = time.Now()
		s.mu.Unlock()
		if _, err := s.dev.Write(packet); err != nil {
			return fmt.Errorf("raw: write TUN: %w", err)
		}
	case framePing:
		if len(plain) != 1 {
			return nil
		}
		s.mu.Lock()
		worker.seen = time.Now()
		s.mu.Unlock()
		s.sendSessionControl(worker, framePong)
		return nil
	case frameRetire:
		if len(plain) != 33 {
			return nil
		}
		s.mu.Lock()
		for address, old := range s.sessions {
			if old.group == worker.group && bytes.Equal(old.workerToken[:], plain[1:17]) && bytes.Equal(old.serverToken[:], plain[17:33]) {
				s.cfg.Log.Infof("RAW worker retired via healthy path: %s", old.addr)
				s.remove(address)
			}
		}
		s.mu.Unlock()
		return nil
	case frameClose:
		if len(plain) == 1 {
			s.mu.Lock()
			s.remove(worker.addr.String())
			s.mu.Unlock()
		}
	}
	return nil
}

func (s *rawServer) downlink(ctx context.Context) error {
	packet := make([]byte, MaxMTU+1)
	for {
		n, err := s.dev.Read(packet[1:])
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, dst, err := ipv4(packet[1:n+1], s.cfg.MTU)
		if err != nil || dst != s.cfg.ClientAddress {
			continue
		}
		worker := s.pick(time.Now())
		if worker == nil {
			continue
		}
		packet[0] = frameData
		if err := s.send(worker.addr, worker.codec, packet[:n+1]); err != nil {
			s.cfg.Log.Debugf("RAW downlink send: %v", err)
			s.mu.Lock()
			s.remove(worker.addr.String())
			s.mu.Unlock()
		}
	}
}

func (s *rawServer) pick(now time.Time) *serverSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	for range len(s.order) {
		if s.index >= len(s.order) {
			s.index = 0
		}
		worker := s.sessions[s.order[s.index]]
		if worker != nil && worker.active && now.Sub(worker.seen) < 90*time.Second {
			// Small chunks reduce reordering of the inner TCP stream across TURN paths.
			s.chunk++
			if s.chunk >= 16 {
				s.index++
				s.chunk = 0
			}
			return worker
		}
		s.index++
		s.chunk = 0
	}
	return nil
}

// remove is called with mu held.
func (s *rawServer) remove(key string) {
	delete(s.sessions, key)
	for i, k := range s.order {
		if k == key {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	s.chunk = 0
	if s.index >= len(s.order) {
		s.index = 0
	}
}

func (s *rawServer) expire(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, worker := range s.sessions {
		ttl := 30 * time.Second
		if worker.active {
			ttl = 90 * time.Second
		}
		if now.Sub(worker.seen) > ttl {
			s.remove(key)
		}
	}
}

func (s *rawServer) sendSessionControl(worker *serverSession, kind byte) {
	if err := s.send(worker.addr, worker.codec, []byte{kind}); err != nil {
		s.cfg.Log.Debugf("RAW control send: %v", err)
		s.mu.Lock()
		s.remove(worker.addr.String())
		s.mu.Unlock()
	}
}
