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
	"sync/atomic"
	"time"

	"github.com/samosvalishe/free-turn-proxy/internal/logx"
	"github.com/samosvalishe/free-turn-proxy/internal/provider"
	"github.com/samosvalishe/free-turn-proxy/internal/proxy/allocpace"
	"github.com/samosvalishe/free-turn-proxy/internal/proxy/udprelay"
)

type ClientConfig struct {
	workerToken  [16]byte // stable across reconnects of one logical worker
	group        [16]byte
	Peer         *net.UDPAddr
	Address      netip.Addr
	MTU          int
	Profile      string
	Key          []byte
	Host, Port   string
	TransportUDP bool
	Streams      int
	Provider     provider.Provider
	Connected    *atomic.Int32
	OnTURN       func(net.IP)
	Log          logx.Logger
}

func (c ClientConfig) validate() error {
	if !c.Address.Is4() || c.Peer == nil || c.Provider == nil {
		return errors.New("raw: client needs IPv4 address, peer, and TURN provider")
	}
	if c.Streams < 1 || c.Streams > 64 {
		return errors.New("raw: total TURN streams must be 1..64")
	}
	if err := validateMTU(c.MTU); err != nil {
		return err
	}
	_, err := newCodec(c.Profile, c.Key, false, nil)
	return err
}

// RunClient owns dev for its lifetime and closes it to interrupt a blocked read.
func RunClient(ctx context.Context, dev Device, cfg ClientConfig) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	if _, err := rand.Read(cfg.group[:]); err != nil {
		return err
	}
	cfg.Log = logx.OrNop(cfg.Log)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(runCtx, func() { _ = dev.Close() })
	defer stop()
	packets := make(chan []byte, 256)
	retirements := make(chan []byte, cfg.Streams*2)
	fatal := make(chan error, 1)
	report := func(err error) {
		select {
		case fatal <- err:
		default:
		}
		cancel()
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		buf := make([]byte, MaxMTU+1)
		for {
			n, err := dev.Read(buf[1:])
			if err != nil {
				if runCtx.Err() == nil {
					report(fmt.Errorf("raw: read TUN: %w", err))
				}
				return
			}
			src, _, err := ipv4(buf[1:1+n], cfg.MTU)
			if err != nil || src != cfg.Address {
				continue
			}
			buf[0] = frameData
			packet := append([]byte(nil), buf[:n+1]...)
			select {
			case packets <- packet:
			default:
			} // bounded queue; IP owns retransmission
		}
	})
	pacer := allocpace.New(allocpace.DefaultInterval)
	for id := 1; id <= cfg.Streams; id++ {
		workerCfg := cfg
		if _, err := rand.Read(workerCfg.workerToken[:]); err != nil {
			report(err)
			break
		}
		wg.Go(func() {
			for runCtx.Err() == nil {
				if !pacer.Wait(runCtx) {
					return
				}
				err := clientStream(runCtx, dev, workerCfg, id, packets, retirements)
				if runCtx.Err() != nil {
					return
				}
				if errors.Is(err, provider.ErrFatalNoStreams) {
					report(err)
					return
				}
				cfg.Log.Warnf("RAW stream %d: %v; retrying", id, err)
				select {
				case <-runCtx.Done():
					return
				case <-time.After(3 * time.Second):
				}
			}
		})
	}
	wg.Wait()
	select {
	case err := <-fatal:
		return err
	default:
		return nil
	}
}

func clientStream(ctx context.Context, dev Device, cfg ClientConfig, id int, packets <-chan []byte, retirements chan []byte) error {
	if until := cfg.Provider.BackoffUntilUnix(); until > time.Now().Unix() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Until(time.Unix(until, 0))):
		}
	}
	getCreds := func(ctx context.Context, id int) (string, string, []string, error) {
		creds, err := cfg.Provider.GetCredentials(ctx, id)
		if err == nil && cfg.OnTURN != nil {
			for _, candidate := range creds.ServerAddrs {
				if cfg.Host != "" {
					_, port, splitErr := net.SplitHostPort(candidate)
					if splitErr != nil {
						continue
					}
					if cfg.Port != "" {
						port = cfg.Port
					}
					candidate = net.JoinHostPort(cfg.Host, port)
				}
				if addr, resolveErr := net.ResolveUDPAddr("udp", candidate); resolveErr == nil {
					cfg.OnTURN(addr.IP)
				}
			}
		}
		return creds.User, creds.Pass, creds.ServerAddrs, err
	}
	stream, err := udprelay.DialTURNWithMaintenance(ctx, cfg.Host, cfg.Port, cfg.TransportUDP, cfg.Peer, id, getCreds, cfg.Log, true)
	if err != nil {
		if cfg.Provider.IsAuthError(err) {
			cfg.Provider.HandleAuthError(id)
		}
		return err
	}
	defer stream.Close()
	relay := stream.Relay
	if cfg.OnTURN != nil {
		cfg.OnTURN(stream.ServerUDPAddr.IP)
	}
	cfg.Provider.ResetErrors(id)
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(streamCtx, func() { _ = relay.SetDeadline(time.Now()) })
	defer stop()
	go func() {
		select {
		case <-streamCtx.Done():
		case <-stream.PermDead:
			cancel()
		}
	}()
	negotiated, mtu, err := clientHandshake(streamCtx, relay, cfg.Peer, cfg)
	if err != nil {
		return err
	}
	// The TUN MTU is fixed before startup. Reject a smaller server MTU rather than
	// silently dropping packets after telling the local IP stack they fit.
	if mtu != cfg.MTU {
		return fmt.Errorf("raw: server MTU %d differs from client MTU %d", mtu, cfg.MTU)
	}
	if cfg.Connected != nil {
		cfg.Connected.Add(1)
		defer cfg.Connected.Add(-1)
	}

	cfg.Log.Infof("RAW stream %d ready: relay=%s client=%s MTU=%d (no WG/DTLS/KCP)", id, relay.LocalAddr(), cfg.Address, mtu)
	// Best effort shutdown frees the old worker immediately on quick reconnect.
	defer func() {
		var out [maxWire]byte
		n, err := negotiated.encode(out[:], []byte{frameClose})
		if err == nil {
			_ = relay.SetWriteDeadline(time.Now().Add(250 * time.Millisecond))
			_, _ = relay.WriteTo(out[:n], cfg.Peer)
		}
	}()
	readDone := make(chan error, 1)
	go func() {
		wireBuf, plainBuf := make([]byte, maxWire+1), make([]byte, maxFrame)
		lastValid := time.Now()
		for {
			_ = relay.SetReadDeadline(lastValid.Add(35 * time.Second))
			n, addr, err := relay.ReadFrom(wireBuf)
			if err != nil {
				readDone <- err
				return
			}
			if addr.String() != cfg.Peer.String() {
				continue
			}
			plain, err := negotiated.decode(plainBuf, wireBuf[:n])
			if err != nil || len(plain) == 0 {
				continue
			}
			switch plain[0] {
			case frameData, frameFragment:
				packet, err := negotiated.fragments.accept(plain, cfg.MTU, time.Now())
				if err != nil || packet == nil {
					continue
				}
				_, dst, err := ipv4(packet, cfg.MTU)
				if err != nil || dst != cfg.Address {
					continue
				}
				lastValid = time.Now()
				if _, err := dev.Write(packet); err != nil {
					readDone <- err
					return
				}
			case framePong, frameReady:
				if len(plain) == 1 {
					lastValid = time.Now()
				}
			}
		}
	}()
	readerJoined := false
	defer func() {
		cancel()
		_ = relay.SetDeadline(time.Now())
		if !readerJoined {
			<-readDone
		}
	}()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	wireBuf := make([]byte, maxWire)
	defer func() {
		if ctx.Err() != nil {
			return
		}
		// The failed path cannot deliver its own goodbye. Ask a healthy path to
		// retire this exact old session; a late notice cannot retire its successor.
		notice := make([]byte, 33)
		notice[0] = frameRetire
		copy(notice[1:17], cfg.workerToken[:])
		copy(notice[17:], negotiated.serverToken[:])
		select {
		case retirements <- notice:
		default:
			cfg.Log.Warnf("RAW stream %d: retirement queue full", id)
		}
	}()
	for {
		var packet []byte
		select {
		case <-streamCtx.Done():
			return streamCtx.Err()
		case err := <-readDone:
			readerJoined = true
			return err
		case packet = <-retirements:
		case packet = <-packets:
		case <-ticker.C:
			packet = []byte{framePing}
		}
		if err := writeFrame(relay, cfg.Peer, negotiated, wireBuf, packet); err != nil {
			return err
		}
	}
}

func writeOneFrame(pc net.PacketConn, peer net.Addr, c *codec, wireBuf, plain []byte) error {
	n, err := c.encode(wireBuf, plain)
	if err != nil {
		return err
	}
	if err := pc.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return err
	}
	_, err = pc.WriteTo(wireBuf[:n], peer)
	return err
}

func clientHandshake(ctx context.Context, pc net.PacketConn, peer net.Addr, cfg ClientConfig) (*codec, int, error) {
	base, err := newCodec(cfg.Profile, cfg.Key, false, nil)
	if err != nil {
		return nil, 0, err
	}
	var clientToken [16]byte
	if _, err := rand.Read(clientToken[:]); err != nil {
		return nil, 0, err
	}
	hello := make([]byte, 39)
	hello[0] = frameHello
	copy(hello[1:17], clientToken[:])
	ip := cfg.Address.As4()
	copy(hello[17:33], cfg.group[:])
	copy(hello[33:37], ip[:])
	binary.BigEndian.PutUint16(hello[37:], uint16(cfg.MTU))
	if cfg.workerToken != ([16]byte{}) {
		hello = append(hello, cfg.workerToken[:]...)
	}
	wireBuf, readBuf, plainBuf := make([]byte, maxWire), make([]byte, maxWire+1), make([]byte, maxFrame)
	var session *codec
	var serverToken [16]byte
	mtu := 0
	for attempt := 0; attempt < 20; attempt++ {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		message, sendCodec := hello, base
		if session != nil {
			message = append([]byte{frameConfirm}, serverToken[:]...)
			sendCodec = session
		}
		if err := writeFrame(pc, peer, sendCodec, wireBuf, message); err != nil {
			return nil, 0, err
		}
		_ = pc.SetReadDeadline(time.Now().Add(time.Second))
		for {
			n, addr, err := pc.ReadFrom(readBuf)
			if err != nil {
				if e, ok := err.(net.Error); ok && e.Timeout() {
					break
				}
				return nil, 0, err
			}
			if addr.String() != peer.String() {
				continue
			}
			recvCodec := base
			if session != nil {
				recvCodec = session
			}
			plain, err := recvCodec.decode(plainBuf, readBuf[:n])
			if err != nil || len(plain) == 0 {
				continue
			}
			if session != nil {
				if len(plain) == 1 && plain[0] == frameReady {
					_ = pc.SetReadDeadline(time.Time{})
					return session, mtu, nil
				}
				continue
			}
			if len(plain) != 35 || plain[0] != frameChallenge || !bytes.Equal(plain[1:17], clientToken[:]) {
				continue
			}
			copy(serverToken[:], plain[17:33])
			mtu = int(binary.BigEndian.Uint16(plain[33:]))
			if err := validateMTU(mtu); err != nil {
				return nil, 0, err
			}
			tokens := append(append([]byte(nil), clientToken[:]...), serverToken[:]...)
			tokens = append(tokens, cfg.group[:]...)
			if cfg.workerToken != ([16]byte{}) {
				tokens = append(tokens, cfg.workerToken[:]...)
			}
			session, err = newCodec(cfg.Profile, cfg.Key, false, tokens)
			if err != nil {
				return nil, 0, err
			}
			session.serverToken = serverToken
			break
		}
	}
	return nil, 0, errors.New("raw: encrypted handshake timed out; check server, profile, key, client IP and MTU")
}
