package backend

import (
	"fmt"
	"os"
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
	f, err := os.OpenFile(filepath.Join(a.dataDir, "ui.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err == nil {
		_, _ = fmt.Fprintln(f, time.Now().Format(time.RFC3339)+" "+line)
		_ = f.Close()
	}
}
