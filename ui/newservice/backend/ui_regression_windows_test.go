package backend

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSnapshotRevisionIncreasesAcrossStateChanges(t *testing.T) {
	a := newApp(t.TempDir())
	first := a.GetSnapshot()
	a.mu.Lock()
	a.state = "connected"
	a.bypassActive = true
	a.mu.Unlock()
	next := a.GetSnapshot()
	if next.Revision <= first.Revision || next.State != "connected" || !next.BypassActive || first.State != "idle" {
		t.Fatalf("incoherent snapshots: %#v %#v", first, next)
	}
	var wg sync.WaitGroup
	results := make(chan uint64, 32)
	for range 32 {
		wg.Add(1)
		go func() { defer wg.Done(); results <- a.GetSnapshot().Revision }()
	}
	wg.Wait()
	close(results)
	seen := map[uint64]bool{}
	for revision := range results {
		if revision <= next.Revision || seen[revision] {
			t.Fatalf("duplicate/stale revision %d", revision)
		}
		seen[revision] = true
	}
}
func TestOldRetryCannotRemoveNewSessionStream(t *testing.T) {
	a := &App{generation: 2, ready: map[string]bool{"1": true, "2": true}}
	if count, valid := a.retryStream(1, "1"); valid || count != 2 || !a.ready["1"] {
		t.Fatal("old process mutated new session")
	}
	if count, valid := a.retryStream(2, "1"); !valid || count != 1 || a.ready["1"] {
		t.Fatal("current retry not applied")
	}
}
func TestCoreOutputHelper(t *testing.T) {
	mode := os.Getenv("FTURN_UI_OUTPUT_HELPER")
	if mode == "" {
		return
	}
	_, _ = os.Stdout.WriteString("RAW stream 1 ready\n")
	switch mode {
	case "oversized":
		_, _ = os.Stdout.WriteString(strings.Repeat("x", 2*1024*1024))
		time.Sleep(time.Hour)
	case "cancel":
		time.Sleep(time.Hour)
	case "crash":
		os.Exit(7)
	}
	os.Exit(0)
}
func TestCoreOutputTermination(t *testing.T) {
	for _, mode := range []string{"oversized", "eof", "cancel", "crash"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := command(ctx, os.Args[0], "-test.run=^TestCoreOutputHelper$")
			cmd.Env = append(os.Environ(), "FTURN_UI_OUTPUT_HELPER="+mode)
			pipe, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
			}()
			scanner := bufio.NewScanner(pipe)
			scanner.Buffer(make([]byte, 8192), 1024*1024)
			if !scanner.Scan() || !strings.Contains(scanner.Text(), "ready") {
				t.Fatal("helper never ready")
			}
			if mode == "cancel" {
				cancel()
			}
			for scanner.Scan() {
			}
			started := time.Now()
			err = waitCoreOutput(cmd, scanner.Err())
			if time.Since(started) > 2*time.Second {
				t.Fatal("wait did not finish promptly")
			}
			switch mode {
			case "oversized":
				if !errors.Is(err, bufio.ErrTooLong) {
					t.Fatalf("lost scanner cause: %v", err)
				}
			case "eof":
				if err != nil {
					t.Fatal(err)
				}
			default:
				if err == nil {
					t.Fatal("expected nonzero child exit")
				}
			}
			if cmd.ProcessState == nil {
				t.Fatal("child not reaped")
			}
		})
	}
}
func TestLogRotationBoundsFilesAndKeepsLatest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui.log")
	for i := 0; i < 20; i++ {
		if err := appendRotatingLog(path, strings.Repeat("x", 70)+"\n", 128, 3); err != nil {
			t.Fatal(err)
		}
	}
	if err := appendRotatingLog(path, "latest\n", 128, 3); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("files=%d", len(entries))
	}
	for _, entry := range entries {
		info, _ := entry.Info()
		if info.Size() > 128 {
			t.Fatalf("unbounded %s", entry.Name())
		}
	}
	latest, _ := os.ReadFile(path)
	if !strings.HasSuffix(string(latest), "latest\n") {
		t.Fatal("latest missing")
	}
	if err := appendRotatingLog(path, strings.Repeat("я", 256), 128, 3); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Size() > 128 {
		t.Fatal("oversized entry bypassed limit")
	}
}
func TestLogFailureVisibleAndMemoryStillAvailable(t *testing.T) {
	a := newApp(t.TempDir())
	if err := os.Mkdir(filepath.Join(a.dataDir, "ui.log"), 0700); err != nil {
		t.Fatal(err)
	}
	a.appendLog("kept in memory")
	s := a.GetSnapshot()
	if s.LogError == "" || len(s.Logs) == 0 || !strings.Contains(s.Logs[len(s.Logs)-1], "kept in memory") {
		t.Fatal("disk failure lost")
	}
}
func TestConcurrentLogsRemainRedacted(t *testing.T) {
	a := newApp(t.TempDir())
	a.profile.Key = "test-secret"
	var wg sync.WaitGroup
	for range 40 {
		wg.Add(1)
		go func() { defer wg.Done(); a.appendLog("line test-secret") }()
	}
	wg.Wait()
	contents, err := os.ReadFile(filepath.Join(a.dataDir, "ui.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "test-secret") || len(strings.Split(strings.TrimSpace(string(contents)), "\n")) != 40 {
		t.Fatal("log leak or interleaved writes")
	}
}

func TestShowRequestPreservesSessionAndCancelsPendingHide(t *testing.T) {
	for _, state := range []string{"visible", "minimized", "hidden", "starting"} {
		t.Run(state, func(t *testing.T) {
			a := newApp(t.TempDir())
			host := &fakeWindow{minimized: state == "minimized"}
			a.desktop.host = host
			a.desktop.hidden.Store(state == "hidden")
			a.desktop.ready.Store(true)
			a.desktop.hideOnReady.Store(true)
			a.state = "connected"
			a.generation = 7
			a.ready = map[string]bool{"1": true}
			if state != "starting" {
				a.domReady.Store(true)
			}
			a.RequestShow()
			if state == "starting" {
				if host.shown != 0 {
					t.Fatal("window used before startup")
				}
				close(a.startupDone)
				a.DomReady(context.Background())
			}
			a.desktop.applyPendingHide()
			if host.shown != 1 || host.minimized || a.desktop.hidden.Load() || host.hidden != 0 {
				t.Fatalf("restore failed: %#v", host)
			}
			if a.state != "connected" || a.generation != 7 || len(a.ready) != 1 {
				t.Fatal("restore changed VPN session")
			}
		})
	}
}
