package session

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/samosvalishe/free-turn-proxy/internal/config"
	"github.com/samosvalishe/free-turn-proxy/internal/logx"
	"github.com/samosvalishe/free-turn-proxy/internal/netctl"
	"github.com/samosvalishe/free-turn-proxy/internal/rawvpn"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type stabilityLog struct {
	logx.Logger
	mu         sync.Mutex
	ready      map[string]bool
	reconnects int
}

func (l *stabilityLog) Infof(f string, args ...any) {
	s := fmt.Sprintf(f, args...)
	if strings.HasPrefix(s, "RAW stream ") && strings.Contains(s, " ready:") {
		id := strings.Fields(s)[2]
		l.mu.Lock()
		l.ready[id] = true
		l.mu.Unlock()
	}
	l.Logger.Infof("%s", s)
}
func (l *stabilityLog) Warnf(f string, args ...any) {
	s := fmt.Sprintf(f, args...)
	if strings.HasPrefix(s, "RAW stream ") && strings.Contains(s, "retrying") {
		l.mu.Lock()
		l.reconnects++
		l.mu.Unlock()
	}
	l.Logger.Warnf("%s", s)
}
func (l *stabilityLog) counts() (int, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.ready), l.reconnects
}

// Opt-in long check; uses an in-memory TUN and never changes Windows routes.
func TestLiveRawVKStability(t *testing.T) {
	value := os.Getenv("FTURN_RAW_SOAK_SECONDS")
	if value == "" {
		t.Skip("live stability check is opt-in")
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 30 {
		t.Fatal("invalid soak duration")
	}
	peer, link, keyFile := os.Getenv("FTURN_RAW_LIVE_PEER"), os.Getenv("FTURN_RAW_LIVE_LINK"), os.Getenv("FTURN_RAW_LIVE_KEY_FILE")
	if peer == "" || link == "" || keyFile == "" {
		t.Fatal("missing live connection variables")
	}
	streams := 10
	if v := os.Getenv("FTURN_RAW_LIVE_STREAMS"); v != "" {
		streams, _ = strconv.Atoi(v)
	}
	mtu := 1420
	if v := os.Getenv("FTURN_RAW_LIVE_MTU"); v != "" {
		mtu, _ = strconv.Atoi(v)
	}
	if v := os.Getenv("FTURN_RAW_CONTROL_INTERFACE"); v != "" {
		index, _ := strconv.Atoi(v)
		if index <= 0 {
			t.Fatal("invalid control interface")
		}
		if err := rawvpn.BindControlInterface(index); err != nil {
			t.Fatal(err)
		}
		defer netctl.SetControl(nil)
	}
	key, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseClient([]string{"-peer", peer, "-links", link, "-n", strconv.Itoa(streams), "-transport", "tcp", "-obf-profile", "rtpopus2", "-obf-key", strings.TrimSpace(string(key))}, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ClientID = "fturn-raw-stability-v1"
	dev := &liveProbeDevice{input: make(chan []byte, 128), output: make(chan []byte, 256), done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(seconds+180)*time.Second)
	defer cancel()
	done := make(chan error, 1)
	logger := &stabilityLog{Logger: logx.New(false), ready: map[string]bool{}}
	go func() { done <- RunRaw(ctx, cfg, dev, netip.MustParseAddr("10.77.0.2"), mtu, logger) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("live client did not stop")
		}
	}()
	startup := time.Now().Add(120 * time.Second)
	for {
		ready, _ := logger.counts()
		if ready == streams {
			break
		}
		if time.Now().After(startup) {
			t.Fatalf("only %d/%d workers authenticated", ready, streams)
		}
		time.Sleep(200 * time.Millisecond)
	}
	type counters struct {
		Sent     int     `json:"sent"`
		Received int     `json:"received"`
		Lost     int     `json:"lost_after_2s"`
		MaxRTTms float64 `json:"max_rtt_ms"`
		SumRTTms float64 `json:"sum_rtt_ms"`
	}
	type outstanding struct {
		target string
		sent   time.Time
	}
	stats := map[string]*counters{"10.77.0.1": {}, "1.1.1.1": {}}
	pending := map[uint16]outstanding{}
	var seq uint16
	start := time.Now()
	end := start.Add(time.Duration(seconds) * time.Second)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	lastReport := start
	windowSent, windowLost := 0, 0
	for time.Now().Before(end.Add(2500 * time.Millisecond)) {
		select {
		case <-ctx.Done():
			t.Fatal("live test context expired")
		case err := <-done:
			done <- err
			t.Fatalf("live client stopped: %v", err)
		case now := <-ticker.C:
			for id, p := range pending {
				if now.Sub(p.sent) > 2*time.Second {
					stats[p.target].Lost++
					windowLost++
					delete(pending, id)
				}
			}
			if now.Before(end) {
				for target := range stats {
					seq++
					packet := liveEcho(target, 64)
					binary.BigEndian.PutUint16(packet[24:26], 0x5354)
					binary.BigEndian.PutUint16(packet[26:28], seq)
					packet[22] = 0
					packet[23] = 0
					binary.BigEndian.PutUint16(packet[22:24], liveChecksum(packet[20:]))
					pending[seq] = outstanding{target, now}
					stats[target].Sent++
					windowSent++
					select {
					case dev.input <- packet:
					default:
						stats[target].Lost++
						windowLost++
						delete(pending, seq)
					}
				}
			}
			if now.Sub(lastReport) >= time.Minute {
				_, reconnects := logger.counts()
				t.Logf("STABILITY minute=%d sent=%d late_or_lost=%d reconnects=%d", int(now.Sub(start).Minutes()), windowSent, windowLost, reconnects)
				windowSent = 0
				windowLost = 0
				lastReport = now
			}
		case packet := <-dev.output:
			if len(packet) < 28 || packet[9] != 1 {
				continue
			}
			h := int(packet[0]&15) * 4
			if len(packet) < h+8 || packet[h] != 0 || binary.BigEndian.Uint16(packet[h+4:h+6]) != 0x5354 {
				continue
			}
			id := binary.BigEndian.Uint16(packet[h+6 : h+8])
			p, ok := pending[id]
			if !ok {
				continue
			}
			src := netip.AddrFrom4([4]byte(packet[12:16])).String()
			if src != p.target {
				continue
			}
			rtt := float64(time.Since(p.sent).Microseconds()) / 1000
			s := stats[p.target]
			if rtt > 2000 {
				s.Lost++
				windowLost++
			} else {
				s.Received++
				s.SumRTTms += rtt
				if rtt > s.MaxRTTms {
					s.MaxRTTms = rtt
				}
			}
			delete(pending, id)
		}
	}
	_, reconnects := logger.counts()
	report := struct {
		Seconds    int                  `json:"seconds"`
		Streams    int                  `json:"streams"`
		Transport  string               `json:"transport"`
		MTU        int                  `json:"mtu"`
		Reconnects int                  `json:"reconnects"`
		Targets    map[string]*counters `json:"targets"`
	}{seconds, streams, "tcp", mtu, reconnects, stats}
	data, _ := json.MarshalIndent(report, "", "  ")
	if path := os.Getenv("FTURN_RAW_SOAK_REPORT"); path != "" {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Error(err)
		}
	}
	t.Logf("STABILITY RESULT %s", data)
	for target, s := range stats {
		if s.Sent == 0 || s.Received == 0 {
			t.Errorf("no traffic delivered to %s", target)
		}
		if s.Lost*100 > s.Sent*2 {
			t.Errorf("more than 2%% late/lost packets to %s: %d/%d", target, s.Lost, s.Sent)
		}
	}
}
