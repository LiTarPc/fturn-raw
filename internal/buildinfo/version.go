package buildinfo

import (
	_ "embed"
	"strings"
)

// Version can override the Raw client core version at release build time.
// It is independent of the Windows application and server package versions.
var Version = "development"

//go:embed core-version.txt
var defaultCoreVersion string

func CoreVersion() string {
	if Version != "" && Version != "development" {
		return Version
	}
	return strings.TrimSpace(defaultCoreVersion)
}
