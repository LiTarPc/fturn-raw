package backend

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (a *App) routeArgs(action string, pid int, mode string) []string {
	return []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(runtimeDirectory(a.root), "routes.ps1"), "-Action", action, "-Mode", mode, "-StateFile", filepath.Join(a.dataDir, "network", "route-state.json"), "-LegacyDirectory", runtimeDirectory(a.root), "-OwnerPid", strconv.Itoa(os.Getpid()), "-ClientPid", strconv.Itoa(pid)}
}

func readGuardReady(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	if scanner.Scan() && strings.TrimSpace(scanner.Text()) == "READY" {
		return nil
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return errors.New("Network recovery guard did not acknowledge readiness")
}

func (a *App) startRouteGuard(ctx context.Context, g, pid int, mode string) error {
	guard := command(context.Background(), "powershell.exe", a.routeArgs("Watch", pid, mode)...)
	guard.Dir = runtimeDirectory(a.root)
	pipe, err := guard.StdoutPipe()
	if err != nil {
		return err
	}
	// The acknowledgement is the only output consumed by the UI; after it the
	// independent guard must outlive a forcibly terminated UI process.
	if err = guard.Start(); err != nil {
		return err
	}
	ready := make(chan error, 1)
	go func() { ready <- readGuardReady(pipe) }()
	select {
	case err = <-ready:
	case <-ctx.Done():
		err = ctx.Err()
	case <-time.After(10 * time.Second):
		err = errors.New("Network recovery guard startup timed out")
	}
	if err != nil {
		_ = guard.Process.Kill()
		_ = guard.Wait()
		return err
	}
	go func() {
		_, _ = io.Copy(io.Discard, pipe)
		_ = guard.Wait()
		if ctx.Err() != nil {
			return
		}
		a.mu.Lock()
		cancel := a.cancel
		current := a.generation == g
		a.mu.Unlock()
		if current && cancel != nil {
			a.appendLog("Network recovery guard stopped unexpectedly; disconnecting Raw")
			cancel()
		}
	}()
	return nil
}
