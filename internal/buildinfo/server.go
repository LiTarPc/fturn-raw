package buildinfo

import (
	_ "embed"
	"strings"
)

//go:embed server-version.txt
var defaultServerVersion string

// ServerBuildVersion optionally overrides the server version at release build time.
// The Windows application and client retain their own independent version.
var ServerBuildVersion string

func ServerVersion() string {
	if ServerBuildVersion != "" {
		return ServerBuildVersion
	}
	return strings.TrimSpace(defaultServerVersion)
}
