package rawvpn

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/netip"
	"testing"
	"time"
)

func TestProfilesCannotBeSubstituted(t *testing.T) {
	key := testKey(t)
	for _, profile := range []string{"rtpopus", "rtpopus2", "rtpopus3"} {
		sender, _ := newCodec(profile, key, false, nil)
		wire := make([]byte, maxWire)
		n, err := sender.encode(wire, []byte{framePing})
		if err != nil {
			t.Fatal(err)
		}
		for _, other := range []string{"rtpopus", "rtpopus2", "rtpopus3"} {
			if other == profile {
				continue
			}
			receiver, _ := newCodec(other, key, true, nil)
			if _, err := receiver.decode(make([]byte, maxFrame), wire[:n]); err == nil {
				t.Fatalf("%s accepted %s", other, profile)
			}
		}
	}
}

func TestAutoServerSwitchesProfilesAndRejectsWrongKey(t *testing.T) {
	key := testKey(t)
	pc := udpSocket(t)
	dev := newTestDevice()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunServer(ctx, pc, dev, ServerConfig{ClientAddress: netip.MustParseAddr("10.77.0.2"), MTU: 1420, Profile: "auto", Key: key})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
	})
	for _, profile := range []string{"rtpopus2", "rtpopus3", "rtpopus2"} {
		path := udpSocket(t)
		cfg := ClientConfig{Address: netip.MustParseAddr("10.77.0.2"), MTU: 1420, Profile: profile, Key: key}
		cfg.group[0] = byte(len(profile))
		cfg.workerToken[0] = 1
		c, mtu, err := clientHandshake(ctx, path, pc.LocalAddr(), cfg)
		if err != nil || mtu != 1420 {
			t.Fatal("profile handover failed", profile, err)
		}
		packet := ipPacket("10.77.0.2", "1.1.1.1", 1420)
		if err := writeFrame(path, pc.LocalAddr(), c, make([]byte, maxWire), append([]byte{frameData}, packet...)); err != nil {
			t.Fatal(err)
		}
		readPacket(t, dev, packet)
	}
	// A spoofed rtpopus3-looking hello with a different key must get no challenge.
	attacker := udpSocket(t)
	hello := make([]byte, 55)
	hello[0] = frameHello
	hello[1] = 1
	hello[17] = 1
	copy(hello[33:37], []byte{10, 77, 0, 2})
	binary.BigEndian.PutUint16(hello[37:39], 1420)
	hello[39] = 1
	bad, _ := newCodec("rtpopus3", bytes.Repeat([]byte{0x95}, 32), false, nil)
	if err := writeFrame(attacker, pc.LocalAddr(), bad, make([]byte, maxWire), hello); err != nil {
		t.Fatal(err)
	}
	attacker.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if _, _, err := attacker.ReadFrom(make([]byte, maxWire)); err == nil {
		t.Fatal("unauthenticated profile was accepted")
	}
	// Receiver cannot be forced into a profile using only a forged header.
	valid, _ := newCodec("rtpopus2", key, false, nil)
	wire := make([]byte, maxWire)
	n, _ := valid.encode(wire, hello)
	wire[15] = 3
	attacker.WriteTo(wire[:n], pc.LocalAddr())
	attacker.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if _, _, err := attacker.ReadFrom(make([]byte, maxWire)); err == nil {
		t.Fatal("forged extension selected a profile")
	}
}
