package rawvpn

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"testing"
)

func testKey(t testing.TB) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestCodecAuthenticationReplayAndDirection(t *testing.T) {
	for _, profile := range []string{"rtpopus", "rtpopus2"} {
		t.Run(profile, func(t *testing.T) {
			key := testKey(t)
			tokens := bytes.Repeat([]byte{0x47}, 32)
			client, err := newCodec(profile, key, false, tokens)
			if err != nil {
				t.Fatal(err)
			}
			server, err := newCodec(profile, key, true, tokens)
			if err != nil {
				t.Fatal(err)
			}
			wireBuf := make([]byte, maxWire)
			message := []byte("IP packet with authenticated headers")
			n, err := client.encode(wireBuf, message)
			if err != nil {
				t.Fatal(err)
			}
			encrypted := append([]byte(nil), wireBuf[:n]...)
			tampered := append([]byte(nil), encrypted...)
			// A forged high counter must not move the authenticated replay window.
			binary.BigEndian.PutUint64(tampered[client.header-8:client.header], ^uint64(0))
			if _, err := server.decode(make([]byte, maxFrame), tampered); err == nil {
				t.Fatal("forged nonce accepted")
			}
			if _, err := client.decode(make([]byte, maxFrame), encrypted); err == nil {
				t.Fatal("reflected client packet accepted")
			}
			plain, err := server.decode(make([]byte, maxFrame), encrypted)
			if err != nil || !bytes.Equal(plain, message) {
				t.Fatalf("decode: %q, %v", plain, err)
			}
			if !bytes.Equal(encrypted, wireBuf[:n]) {
				t.Fatal("decode mutated input")
			}
			if _, err := server.decode(make([]byte, maxFrame), encrypted); !errors.Is(err, ErrReplay) {
				t.Fatalf("replay not rejected: %v", err)
			}
			wrong, _ := newCodec(profile, testKey(t), true, tokens)
			if _, err := wrong.decode(make([]byte, maxFrame), encrypted); err == nil {
				t.Fatal("wrong key accepted")
			}
			stale, _ := newCodec(profile, key, true, bytes.Repeat([]byte{0x48}, 32))
			if _, err := stale.decode(make([]byte, maxFrame), encrypted); err == nil {
				t.Fatal("old session accepted after challenge changed")
			}
			n, err = server.encode(wireBuf, message)
			if err != nil {
				t.Fatal(err)
			}
			plain, err = client.decode(make([]byte, maxFrame), wireBuf[:n])
			if err != nil || !bytes.Equal(plain, message) {
				t.Fatalf("reverse decode: %v", err)
			}
		})
	}
}

func TestReplayWindowReorderingAndBoundaries(t *testing.T) {
	var w replayWindow
	for _, n := range []uint64{100, 103, 101, 102, 104} {
		if !w.accept(n) {
			t.Fatalf("out-of-order %d rejected", n)
		}
	}
	if w.accept(101) {
		t.Fatal("duplicate accepted")
	}
	if !w.accept(100 + replaySize) {
		t.Fatal("advance rejected")
	}
	if w.accept(100) {
		t.Fatal("stale packet accepted")
	}
	if !w.accept(100 + replaySize - 1) {
		t.Fatal("unseen packet inside window rejected")
	}
	if w.accept(100 + replaySize - 1) {
		t.Fatal("duplicate after shift accepted")
	}
	var edge replayWindow
	if !edge.accept(^uint64(0)-1) || !edge.accept(^uint64(0)) || edge.accept(0) {
		t.Fatal("counter boundary handling failed")
	}
}

func TestRawDisallowsUnencryptedProfiles(t *testing.T) {
	for _, profile := range []string{"none", "", "rtpopus3"} {
		if _, err := newCodec(profile, testKey(t), false, nil); err == nil {
			t.Fatalf("unsupported profile %q accepted", profile)
		}
	}
	if _, err := newCodec("rtpopus2", []byte("short"), false, nil); err == nil {
		t.Fatal("short key accepted")
	}
}

func BenchmarkEncryptedRawRoundTrip(b *testing.B) {
	key := testKey(b)
	client, _ := newCodec("rtpopus2", key, false, bytes.Repeat([]byte{1}, 32))
	server, _ := newCodec("rtpopus2", key, true, bytes.Repeat([]byte{1}, 32))
	plain, encrypted, out := make([]byte, DefaultMTU+1), make([]byte, maxWire), make([]byte, maxFrame)
	b.SetBytes(DefaultMTU)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n, err := client.encode(encrypted, plain)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := server.decode(out, encrypted[:n]); err != nil {
			b.Fatal(err)
		}
	}
}
