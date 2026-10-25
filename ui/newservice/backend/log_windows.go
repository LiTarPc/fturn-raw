package backend

import (
	"path/filepath"
	"strings"
	"time"
)

func (a *App) appendLog(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	a.mu.Lock()
	for _, p := range append(append([]SavedProfile{}, a.profiles...), SavedProfile{Profile: a.profile}) {
		for _, secret := range []string{p.Profile.VkLink, p.Profile.Key} {
			if secret != "" {
				line = strings.ReplaceAll(line, secret, "[скрыто]")
			}
		}
	}
	a.logs = append(a.logs, time.Now().Format("15:04:05")+" "+line)
	if len(a.logs) > 500 {
		a.logs = a.logs[len(a.logs)-500:]
	}
	a.mu.Unlock()
	if a.dataDir == "" {
		return
	}
	a.logMu.Lock()
	defer a.logMu.Unlock()
	err := appendRotatingLog(filepath.Join(a.dataDir, "ui.log"), time.Now().Format(time.RFC3339)+" "+line+"\n", uiLogMaxBytes, uiLogBackups)
	a.mu.Lock()
	a.logError = ""
	if err != nil {
		a.logError = err.Error()
	}
	a.mu.Unlock()
}

// UI failures have explicit severity just like core logs; never infer it from text.
func (a *App) appendErrorLog(line string) {
	line = strings.TrimSpace(line)
	if line != "" {
		a.appendLog("[ERROR] " + line)
	}
}
