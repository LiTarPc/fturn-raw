package rawvpn

import (
	"encoding/binary"
	"errors"
	"net/netip"
)

const (
	frameData byte = iota
	frameHello
	frameChallenge
	frameConfirm
	frameReady
	framePing
	framePong
	frameClose
	frameFragment
	frameRetire
)

// ipv4 checks lengths before inspecting addresses, including fragmented IPv4.
func ipv4(packet []byte, mtu int) (src, dst netip.Addr, err error) {
	if len(packet) < 20 || len(packet) > mtu || packet[0]>>4 != 4 {
		return src, dst, errors.New("raw: invalid IPv4 packet size/version")
	}
	header := int(packet[0]&15) * 4
	if header < 20 || header > len(packet) || int(binary.BigEndian.Uint16(packet[2:4])) != len(packet) {
		return src, dst, errors.New("raw: malformed IPv4 lengths")
	}
	src = netip.AddrFrom4([4]byte(packet[12:16]))
	dst = netip.AddrFrom4([4]byte(packet[16:20]))
	return src, dst, nil
}

func validateMTU(mtu int) error {
	if mtu < 576 || mtu > MaxMTU {
		return errors.New("raw: MTU must be between 576 and 1500")
	}
	return nil
}
