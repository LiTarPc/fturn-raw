package rawvpn

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"time"
)

// Keep the outer datagram ceiling unchanged when the inner TUN MTU grows.
// rtpopus2 + IPv4/UDP yields at most 1351+52+28 = 1431 bytes.
const (
	maxSingleIP    = 1350
	fragmentHeader = 13 // type, random packet ID, total length, offset
	fragmentChunk  = maxSingleIP + 1 - fragmentHeader
	maxAssemblies  = 64
	fragmentTTL    = 3 * time.Second
)

type fragmentAssembly struct {
	data    [MaxMTU]byte
	total   int
	parts   [2]bool
	started time.Time
}
type fragmentAssembler struct {
	mu      sync.Mutex
	packets map[uint64]*fragmentAssembly
}

// accept is called only after AEAD authentication and replay validation.
// Incomplete packets never reach the TUN. A codec isolates one worker/session.
func (a *fragmentAssembler) accept(plain []byte, mtu int, now time.Time) ([]byte, error) {
	if len(plain) == 0 {
		return nil, errors.New("raw: empty data frame")
	}
	if plain[0] == frameData {
		if len(plain)-1 > maxSingleIP {
			return nil, errors.New("raw: oversized unfragmented data")
		}
		return plain[1:], nil
	}
	if plain[0] != frameFragment || len(plain) <= fragmentHeader {
		return nil, errors.New("raw: invalid fragment frame")
	}
	id := binary.BigEndian.Uint64(plain[1:9])
	total := int(binary.BigEndian.Uint16(plain[9:11]))
	offset := int(binary.BigEndian.Uint16(plain[11:13]))
	if total <= maxSingleIP || total > MaxMTU || total > mtu || offset >= total || offset%fragmentChunk != 0 {
		return nil, errors.New("raw: invalid fragment range")
	}
	part := offset / fragmentChunk
	if part >= 2 || len(plain)-fragmentHeader != min(fragmentChunk, total-offset) {
		return nil, errors.New("raw: invalid fragment length")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.packets == nil {
		a.packets = make(map[uint64]*fragmentAssembly)
	}
	for key, p := range a.packets {
		if now.Sub(p.started) >= fragmentTTL {
			delete(a.packets, key)
		}
	}
	p := a.packets[id]
	if p == nil {
		if len(a.packets) >= maxAssemblies {
			return nil, errors.New("raw: fragment assembly limit")
		}
		p = &fragmentAssembly{total: total, started: now}
		a.packets[id] = p
	}
	if p.total != total || p.parts[part] {
		return nil, errors.New("raw: inconsistent or duplicate fragment")
	}
	copy(p.data[offset:total], plain[fragmentHeader:])
	p.parts[part] = true
	if !p.parts[0] || !p.parts[1] {
		return nil, nil
	}
	delete(a.packets, id)
	return p.data[:total], nil
}

func writeFrame(pc net.PacketConn, peer net.Addr, c *codec, wireBuf, plain []byte) error {
	if len(plain) == 0 || plain[0] != frameData || len(plain)-1 <= maxSingleIP {
		return writeOneFrame(pc, peer, c, wireBuf, plain)
	}
	packet := plain[1:]
	if len(packet) > MaxMTU {
		return errors.New("raw: IP packet exceeds maximum MTU")
	}
	var frame [maxSingleIP + 1]byte
	frame[0] = frameFragment
	if _, err := rand.Read(frame[1:9]); err != nil {
		return err
	}
	binary.BigEndian.PutUint16(frame[9:11], uint16(len(packet)))
	for offset := 0; offset < len(packet); offset += fragmentChunk {
		n := min(fragmentChunk, len(packet)-offset)
		binary.BigEndian.PutUint16(frame[11:13], uint16(offset))
		copy(frame[fragmentHeader:], packet[offset:offset+n])
		if err := writeOneFrame(pc, peer, c, wireBuf, frame[:fragmentHeader+n]); err != nil {
			return err
		}
	}
	return nil
}
