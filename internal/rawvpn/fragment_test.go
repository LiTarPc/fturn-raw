package rawvpn

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"
)

type fragmentCapture struct{ packets [][]byte }

func (c *fragmentCapture) WriteTo(p []byte, _ net.Addr) (int, error) {
	c.packets = append(c.packets, append([]byte(nil), p...))
	return len(p), nil
}
func (*fragmentCapture) ReadFrom([]byte) (int, net.Addr, error) { return 0, nil, net.ErrClosed }
func (*fragmentCapture) Close() error                           { return nil }
func (*fragmentCapture) LocalAddr() net.Addr                    { return &net.UDPAddr{} }
func (*fragmentCapture) SetDeadline(time.Time) error            { return nil }
func (*fragmentCapture) SetReadDeadline(time.Time) error        { return nil }
func (*fragmentCapture) SetWriteDeadline(time.Time) error       { return nil }

func TestLargeIPEncryptedFragmentRoundTrip(t *testing.T) {
	for _, profile := range []string{"rtpopus", "rtpopus2"} {
		for _, size := range []int{1350, 1351, 1492, 1500} {
			key := testKey(t)
			sender, _ := newCodec(profile, key, false, nil)
			receiver, _ := newCodec(profile, key, true, nil)
			capture := &fragmentCapture{}
			packet := ipPacket("10.77.0.2", "1.1.1.1", size)
			if err := writeFrame(capture, &net.UDPAddr{}, sender, make([]byte, maxWire), append([]byte{frameData}, packet...)); err != nil {
				t.Fatal(err)
			}
			want := 1
			if size > maxSingleIP {
				want = 2
			}
			if len(capture.packets) != want {
				t.Fatalf("%s size=%d: got %d datagrams", profile, size, len(capture.packets))
			}
			var assembled []byte
			for i := len(capture.packets) - 1; i >= 0; i-- { // Out-of-order fragments must work.
				wire := capture.packets[i]
				if len(wire)+28 > 1431 {
					t.Fatal("inner MTU increase enlarged the outer datagram")
				}
				plain, err := receiver.decode(make([]byte, maxFrame), wire)
				if err != nil {
					t.Fatal(err)
				}
				assembled, err = receiver.fragments.accept(plain, 1500, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				if i > 0 && assembled != nil {
					t.Fatal("incomplete packet reached the TUN boundary")
				}
			}
			if !bytes.Equal(assembled, packet) {
				t.Fatalf("%s size=%d: reassembled IP changed", profile, size)
			}
			if len(receiver.fragments.packets) != 0 {
				t.Fatal("completed assembly was retained")
			}
			if _, err := receiver.decode(make([]byte, maxFrame), capture.packets[0]); !errors.Is(err, ErrReplay) {
				t.Fatal("encrypted fragment replay accepted")
			}
		}
	}
}

func testFragment(id uint64, total, offset int) []byte {
	n := min(fragmentChunk, total-offset)
	p := make([]byte, fragmentHeader+n)
	p[0] = frameFragment
	binary.BigEndian.PutUint64(p[1:9], id)
	binary.BigEndian.PutUint16(p[9:11], uint16(total))
	binary.BigEndian.PutUint16(p[11:13], uint16(offset))
	return p
}
func TestFragmentLimitsExpiryAndValidation(t *testing.T) {
	now := time.Now()
	var a fragmentAssembler
	first := testFragment(1, 1500, 0)
	if p, err := a.accept(first, 1500, now); err != nil || p != nil {
		t.Fatal("first fragment failed")
	}
	if _, err := a.accept(first, 1500, now); err == nil {
		t.Fatal("duplicate accepted")
	}
	if _, err := a.accept(first, 1280, now); err == nil {
		t.Fatal("negotiated MTU bypassed")
	}
	other := testFragment(1, 1492, 0)
	if _, err := a.accept(other, 1500, now); err == nil {
		t.Fatal("inconsistent total accepted")
	}
	for _, mutate := range []func([]byte) []byte{
		func(p []byte) []byte { return p[:12] },
		func(p []byte) []byte { return p[:len(p)-1] },
		func(p []byte) []byte { binary.BigEndian.PutUint16(p[9:11], 1501); return p },
		func(p []byte) []byte { binary.BigEndian.PutUint16(p[11:13], 1); return p },
		func(p []byte) []byte { binary.BigEndian.PutUint16(p[11:13], 65535); return p },
	} {
		if _, err := a.accept(mutate(append([]byte(nil), first...)), 1500, now); err == nil {
			t.Fatal("malformed fragment accepted")
		}
	}
	if p, err := a.accept(testFragment(1, 1500, fragmentChunk), 1500, now.Add(fragmentTTL)); err != nil || p != nil {
		t.Fatal("expired first fragment was used")
	}
	var bounded fragmentAssembler
	for id := uint64(0); id < maxAssemblies; id++ {
		if _, err := bounded.accept(testFragment(id, 1500, 0), 1500, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := bounded.accept(testFragment(999, 1500, 0), 1500, now); err == nil {
		t.Fatal("assembly cap bypassed")
	}
	if _, err := bounded.accept(testFragment(999, 1500, 0), 1500, now.Add(fragmentTTL)); err != nil {
		t.Fatal("expired assemblies did not free capacity")
	}
	var independent fragmentAssembler
	if p, err := independent.accept(testFragment(1, 1500, fragmentChunk), 1500, now); err != nil || p != nil {
		t.Fatal("assembly crossed session boundary")
	}
}
