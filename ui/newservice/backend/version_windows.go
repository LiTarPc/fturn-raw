package backend

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Version is stamped by the same release build that packages this UI.
var Version = "development"

func parseCoreVersion(output string) string {
	text := strings.TrimSpace(output)
	if !strings.HasPrefix(text, "FturnRaw core ") || strings.ContainsAny(text, "\r\n") {
		return "unknown"
	}
	version := strings.TrimPrefix(text, "FturnRaw core ")
	if version == "" || strings.ContainsAny(version, " \t") {
		return "unknown"
	}
	return version
}

type VersionInfo struct {
	AppVersion  string `json:"appVersion"`
	CoreVersion string `json:"coreVersion"`
}

func (a *App) GetVersionInfo() VersionInfo {
	return VersionInfo{AppVersion: Version, CoreVersion: a.coreVersion()}
}
func (a *App) coreVersion() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := command(ctx, filepath.Join(runtimeDirectory(a.root), "raw-client.exe"), "--version").Output()
	core := "unknown"
	if err == nil {
		core = parseCoreVersion(string(output))
	}
	return core
}
func (a *App) appendVersionLog(event string) {
	a.appendLog(fmt.Sprintf("[INFO] [VERSION] %s: FturnRaw UI=%s; core=%s", event, Version, a.coreVersion()))
}
