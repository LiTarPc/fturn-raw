// Package rawvpn transports IPv4 packets over encrypted RTP/TURN datagrams.
// Its wire protocol is experimental and intentionally separate from legacy DTLS.
package rawvpn

import (
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"github.com/samosvalishe/free-turn-proxy/internal/wire"
	"github.com/samosvalishe/free-turn-proxy/internal/wire/rtpopus"
	"github.com/samosvalishe/free-turn-proxy/internal/wire/rtpopus2"
	"golang.org/x/crypto/chacha20poly1305"
)

const (
	DefaultMTU = 1280
	MaxMTU     = 1500
	maxFrame   = MaxMTU + 1
	maxWire    = maxSingleIP + 1 + 64
	replaySize = 2048
)

var ErrReplay = errors.New("raw: replayed or stale packet")

// DeriveKey separates protocol versions, directions, and negotiated sessions.
func deriveKey(master []byte, direction string, tokens []byte) []byte {
	h := hmac.New(sha256.New, master)
	h.Write([]byte("fturn-raw-v1/" + direction + "\x00"))
	h.Write(tokens)
	return h.Sum(nil)
}

type codec struct {
	serverToken [16]byte // authenticated remote session identity, for retirement notices
	send        wire.Codec
	recv        cipher.AEAD
	header      int
	mu          sync.Mutex
	windows     map[[4]byte]*replayWindow
	tick        uint64
	fragments   fragmentAssembler
}

type replayWindow struct {
	high    uint64
	started bool
	bits    [replaySize / 64]uint64
	used    uint64
}

func (w *replayWindow) accept(n uint64) bool {
	if w.started && n <= w.high && w.high-n >= replaySize {
		return false
	}
	if !w.started || n > w.high {
		if !w.started || n-w.high >= replaySize {
			clear(w.bits[:])
		} else {
			for i := w.high + 1; ; i++ {
				slot := i % replaySize
				w.bits[slot/64] &^= uint64(1) << (slot % 64)
				if i == n {
					break
				}
			}
		}
		w.high, w.started = n, true
	}
	slot := n % replaySize
	bit := uint64(1) << (slot % 64)
	if w.bits[slot/64]&bit != 0 {
		return false
	}
	w.bits[slot/64] |= bit
	return true
}

func newCodec(profile string, key []byte, server bool, tokens []byte) (*codec, error) {
	if len(key) != 32 {
		return nil, errors.New("raw: a dedicated 32-byte obfuscation key is required")
	}
	sendDirection, recvDirection := "client-to-server", "server-to-client"
	if server {
		sendDirection, recvDirection = recvDirection, sendDirection
	}
	sendKey := deriveKey(key, sendDirection, tokens)
	var send wire.Codec
	var err error
	switch profile {
	case wire.ProfileRTPOpus:
		send, err = rtpopus.NewConn(sendKey, server)
	case wire.ProfileRTPOpus2:
		send, err = rtpopus2.NewConn(sendKey, server)
	default:
		return nil, fmt.Errorf("raw: profile must be rtpopus or rtpopus2 (got %q)", profile)
	}
	if err != nil {
		return nil, err
	}
	recv, err := chacha20poly1305.New(deriveKey(key, recvDirection, tokens))
	if err != nil {
		return nil, err
	}
	return &codec{send: send, recv: recv, header: send.HeaderLen(), windows: make(map[[4]byte]*replayWindow)}, nil
}

func (c *codec) encode(dst, plain []byte) (int, error) { return c.send.WrapInto(dst, plain) }

// Authentication precedes replay tracking. Decode does not modify wireBytes,
// so a server may try a negotiated key and then the bootstrap key.
func (c *codec) decode(dst, wireBytes []byte) ([]byte, error) {
	if len(wireBytes) < c.header+16 || len(wireBytes) > maxWire {
		return nil, errors.New("raw: invalid encrypted packet length")
	}
	if wireBytes[0] != 0x80 && c.header == 24 || wireBytes[0] != 0x90 && c.header == 36 || wireBytes[1]&0x7f != 111 {
		return nil, errors.New("raw: invalid RTP header")
	}
	if c.header == 36 && (binary.BigEndian.Uint16(wireBytes[12:14]) != 0xbede || binary.BigEndian.Uint16(wireBytes[14:16]) != 2) {
		return nil, errors.New("raw: invalid RTP extension")
	}
	nonce := wireBytes[c.header-12 : c.header]
	plain, err := c.recv.Open(dst[:0], nonce, wireBytes[c.header:], wireBytes[:c.header])
	if err != nil {
		return nil, err
	}
	var prefix [4]byte
	copy(prefix[:], nonce[:4])
	n := binary.BigEndian.Uint64(nonce[4:])
	c.mu.Lock()
	defer c.mu.Unlock()
	w := c.windows[prefix]
	if w == nil {
		// Bootstrap messages can span many reconnects. Their replay cache is bounded;
		// even an old hello receives a fresh server challenge and fresh session keys.
		if len(c.windows) == 128 {
			var oldest [4]byte
			min := ^uint64(0)
			for p, v := range c.windows {
				if v.used < min {
					oldest, min = p, v.used
				}
			}
			delete(c.windows, oldest)
		}
		w = &replayWindow{}
		c.windows[prefix] = w
	}
	if !w.accept(n) {
		return nil, ErrReplay
	}
	c.tick++
	w.used = c.tick
	return plain, nil
}
