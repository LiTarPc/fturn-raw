package backend

import (
	"fmt"
	"os/exec"
)

// A failed scanner no longer drains the pipe. Stop this exact child before
// Wait, otherwise a writer can block forever even after it reported ready.
func waitCoreOutput(cmd *exec.Cmd, scanErr error) error {
	if scanErr != nil {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
		return fmt.Errorf("reading core output: %w", scanErr)
	}
	return cmd.Wait()
}

// Generation validation and mutation must share a lock: an old process can
// still deliver buffered output after Disconnect and the next Connect.
func (a *App) retryStream(generation int, stream string) (int, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.generation != generation {
		return len(a.ready), false
	}
	delete(a.ready, stream)
	return len(a.ready), true
}
