package rawvpn

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"testing"
)

func checkedPacket(protocol byte, source, destination string) []byte {
	p := ipPacket(source, destination, 85) // odd payload length exercises checksum padding
	p[9] = protocol
	switch protocol {
	case 6:
		p[32] = 0x50
	case 17:
		binary.BigEndian.PutUint16(p[24:26], uint16(len(p)-20))
	case 1:
		p[20], p[21] = 8, 0
	}
	fillChecksums(p)
	return p
}

// Recompute checksums from full headers and pseudo-headers, independently of
// the incremental translation implementation.
func fillChecksums(p []byte) {
	h := int(p[0]&15) * 4
	p[10], p[11] = 0, 0
	binary.BigEndian.PutUint16(p[10:12], deploymentChecksum(p[:h]))
	offset := 2
	if p[9] == 6 {
		offset = 16
	}
	if p[9] == 17 || p[9] == 136 {
		offset = 6
	}
	body := append([]byte(nil), p[h:]...)
	body[offset], body[offset+1] = 0, 0
	data := body
	if p[9] != 1 {
		pseudo := append([]byte(nil), p[12:20]...)
		pseudo = append(pseudo, 0, p[9], byte(len(body)>>8), byte(len(body)))
		data = append(pseudo, body...)
	}
	sum := deploymentChecksum(data)
	if (p[9] == 17 || p[9] == 136) && sum == 0 {
		sum = 0xffff
	}
	binary.BigEndian.PutUint16(p[h+offset:h+offset+2], sum)
}

func TestTranslationChecksumsAndRoundTrip(t *testing.T) {
	from, to := netip.MustParseAddr("10.77.0.2"), netip.MustParseAddr("10.77.0.4")
	for _, protocol := range []byte{6, 17, 136, 1} {
		for _, source := range []bool{true, false} {
			original := checkedPacket(protocol, "10.77.0.2", "1.1.1.1")
			offset := 12
			if !source {
				original = checkedPacket(protocol, "1.1.1.1", "10.77.0.2")
				offset = 16
			}
			expected := append([]byte(nil), original...)
			ip := to.As4()
			copy(expected[offset:offset+4], ip[:])
			fillChecksums(expected)
			actual := append([]byte(nil), original...)
			if err := translateIPv4(actual, from, to, source); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(actual, expected) {
				t.Fatalf("bad checksum/address protocol=%d source=%v", protocol, source)
			}
			if err := translateIPv4(actual, to, from, source); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(actual, original) {
				t.Fatalf("round-trip changed protocol=%d source=%v", protocol, source)
			}
		}
	}
}

func TestTranslationPreservesDisabledUDPChecksum(t *testing.T) {
	p := checkedPacket(17, "10.77.0.2", "1.1.1.1")
	p[26], p[27] = 0, 0
	if err := translateIPv4(p, netip.MustParseAddr("10.77.0.2"), netip.MustParseAddr("10.77.0.3"), true); err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint16(p[26:28]) != 0 {
		t.Fatal("disabled UDP checksum enabled")
	}
	if deploymentChecksum(p[:20]) != 0 {
		t.Fatal("bad IPv4 checksum")
	}
}

func TestTranslationUDPZeroChecksumUsesFFFF(t *testing.T) {
	from, to := netip.MustParseAddr("10.77.0.2"), netip.MustParseAddr("10.77.0.3")
	for _, protocol := range []byte{17, 136} {
		// Choose a payload word that makes the translated checksum zero.
		p := checkedPacket(protocol, to.String(), "1.1.1.1")
		p[28], p[29], p[26], p[27] = 0, 0, 0, 0
		pseudo := append([]byte(nil), p[12:20]...)
		pseudo = append(pseudo, 0, protocol, 0, byte(len(p)-20))
		binary.BigEndian.PutUint16(p[28:30], deploymentChecksum(append(pseudo, p[20:]...)))
		a := from.As4()
		copy(p[12:16], a[:])
		fillChecksums(p)
		if err := translateIPv4(p, from, to, true); err != nil {
			t.Fatal(err)
		}
		if binary.BigEndian.Uint16(p[26:28]) != 0xffff {
			t.Fatalf("protocol %d: computed zero checksum must use 0xffff", protocol)
		}
	}
}

func TestTranslationFragmentedTransportHeaders(t *testing.T) {
	from, to := netip.MustParseAddr("10.77.0.2"), netip.MustParseAddr("10.77.0.4")
	for _, protocol := range []byte{6, 17} {
		whole := checkedPacket(protocol, from.String(), "1.1.1.1")
		expected := append([]byte(nil), whole...)
		ip := to.As4()
		copy(expected[12:16], ip[:])
		fillChecksums(expected)
		// TCP's checksum lies in the third tiny fragment, not the first.
		for offset := 0; offset < len(whole)-20; offset += 8 {
			end := min(offset+8, len(whole)-20)
			fragment := append([]byte(nil), whole[:20]...)
			fragment = append(fragment, whole[20+offset:20+end]...)
			binary.BigEndian.PutUint16(fragment[2:4], uint16(len(fragment)))
			flags := uint16(offset / 8)
			if end < len(whole)-20 {
				flags |= 0x2000
			}
			binary.BigEndian.PutUint16(fragment[6:8], flags)
			fragment[10], fragment[11] = 0, 0
			binary.BigEndian.PutUint16(fragment[10:12], deploymentChecksum(fragment[:20]))
			if err := translateIPv4(fragment, from, to, true); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(fragment[20:], expected[20+offset:20+end]) || deploymentChecksum(fragment[:20]) != 0 {
				t.Fatalf("bad translated fragment protocol=%d offset=%d", protocol, offset)
			}
		}
	}
}

func TestTranslationICMPErrorQuotes(t *testing.T) {
	from, to := netip.MustParseAddr("10.77.0.4"), netip.MustParseAddr("10.77.0.2")
	for _, protocol := range []byte{6, 17, 1} {
		for _, source := range []bool{false, true} {
			quoted := checkedPacket(protocol, from.String(), "1.1.1.1")
			outer := checkedPacket(1, "1.1.1.1", from.String())[:28]
			innerOffset, outerOffset := 12, 16
			if source {
				quoted = checkedPacket(protocol, "1.1.1.1", from.String())
				outer = checkedPacket(1, from.String(), "1.1.1.1")[:28]
				innerOffset, outerOffset = 16, 12
			}
			for _, quoteSize := range []int{28, len(quoted)} {
				original := append(append([]byte(nil), outer...), quoted[:quoteSize]...)
				original[20], original[21] = 3, 4 // fragmentation needed (PMTU)
				binary.BigEndian.PutUint16(original[26:28], 1280)
				binary.BigEndian.PutUint16(original[2:4], uint16(len(original)))
				fillChecksums(original)
				expectedQuote := append([]byte(nil), quoted...)
				ip := to.As4()
				copy(expectedQuote[innerOffset:innerOffset+4], ip[:])
				fillChecksums(expectedQuote)
				expected := append([]byte(nil), original...)
				copy(expected[28:], expectedQuote[:quoteSize])
				copy(expected[outerOffset:outerOffset+4], ip[:])
				fillChecksums(expected)
				if err := translateIPv4(original, from, to, source); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(original, expected) {
					t.Fatalf("bad ICMP quote protocol=%d source=%v quote=%d", protocol, source, quoteSize)
				}
			}
		}
	}
}

func TestChecksumRFC1624ZeroBoundary(t *testing.T) {
	if sum := replaceChecksumWord(0xdd2f, 0x5555, 0x3285); sum != 0 {
		t.Fatalf("RFC 1624 example got %04x", sum)
	}
}
