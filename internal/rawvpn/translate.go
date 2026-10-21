package rawvpn

import (
	"encoding/binary"
	"errors"
	"net/netip"
)

// translateIPv4 gives each client group a distinct address on the server TUN,
// while preserving the address embedded in existing shared configuration links.
// Incremental checksums follow RFC 1624, including fragmented TCP/UDP packets.
func translateIPv4(packet []byte, from, to netip.Addr, source bool) error {
	if from == to {
		return nil
	}
	if _, _, err := ipv4(packet, MaxMTU); err != nil {
		return err
	}
	offset := 16
	if source {
		offset = 12
	}
	if netip.AddrFrom4([4]byte(packet[offset:offset+4])) != from {
		return errors.New("raw: unexpected address for client translation")
	}
	rewriteAddress(packet, offset, to)
	header := int(packet[0]&15) * 4
	fragment := int(binary.BigEndian.Uint16(packet[6:8])&0x1fff) * 8
	// ICMP error payloads quote the original IP header and transport header.
	// Reverse the corresponding address there too, so PMTU discovery works.
	if packet[9] == 1 && fragment == 0 && len(packet) >= header+8 {
		switch packet[header] {
		case 3, 4, 5, 11, 12:
			quote := packet[header+8:]
			if len(quote) < 20 || quote[0]>>4 != 4 {
				return nil
			}
			qh := int(quote[0]&15) * 4
			if qh < 20 || qh > len(quote) || int(binary.BigEndian.Uint16(quote[2:4])) < qh {
				return nil
			}
			innerOffset := 28 - offset // outer source -> quoted destination, and vice versa
			if netip.AddrFrom4([4]byte(quote[innerOffset:innerOffset+4])) != from {
				return nil
			}
			// A quote may be truncated. Update only checksum fields present in it.
			// Account for every changed word in the outer ICMP checksum, even
			// when the ICMP message itself is fragmented.
			var before [MaxMTU]byte
			copy(before[:], quote)
			rewriteAddress(quote, innerOffset, to)
			sum := binary.BigEndian.Uint16(packet[header+2 : header+4])
			for i := 0; i+1 < len(quote); i += 2 {
				old, next := binary.BigEndian.Uint16(before[i:i+2]), binary.BigEndian.Uint16(quote[i:i+2])
				if old != next {
					sum = replaceChecksumWord(sum, old, next)
				}
			}
			binary.BigEndian.PutUint16(packet[header+2:header+4], sum)
		}
	}
	return nil
}

// rewriteAddress accepts complete packets and truncated ICMP quotes. Fragment
// offsets identify the fragment containing a transport checksum, including TCP
// headers split across tiny fragments. UDP checksum zero remains disabled.
func rewriteAddress(packet []byte, offset int, address netip.Addr) {
	old := [4]byte(packet[offset : offset+4])
	next := address.As4()
	adjust := func(field []byte, transport bool) {
		sum := binary.BigEndian.Uint16(field)
		if transport && packet[9] == 17 && sum == 0 {
			return
		}
		for i := 0; i < 4; i += 2 {
			sum = replaceChecksumWord(sum, binary.BigEndian.Uint16(old[i:i+2]), binary.BigEndian.Uint16(next[i:i+2]))
		}
		if transport && (packet[9] == 17 || packet[9] == 136) && sum == 0 {
			sum = 0xffff
		}
		binary.BigEndian.PutUint16(field, sum)
	}
	adjust(packet[10:12], false)
	header := int(packet[0]&15) * 4
	fragment := int(binary.BigEndian.Uint16(packet[6:8])&0x1fff) * 8
	checksumOffset := -1
	switch packet[9] {
	case 6: // TCP pseudo-header
		checksumOffset = 16
	case 17, 136: // UDP and UDP-Lite pseudo-header
		checksumOffset = 6
	}
	pos := header + checksumOffset - fragment
	if checksumOffset >= 0 && pos >= header && pos+2 <= len(packet) {
		adjust(packet[pos:pos+2], true)
	}
	copy(packet[offset:offset+4], next[:])
}

func replaceChecksumWord(checksum, old, next uint16) uint16 {
	sum := uint32(^checksum) + uint32(^old) + uint32(next)
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}
