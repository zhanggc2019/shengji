// Package buildinfo describes the version and release channel of this build.
package buildinfo

import (
	_ "embed"
	"strings"
)

//go:embed version.txt
var baseVersion string

// These values are set by the Windows packaging script.
var (
	releaseVersion   string
	UpdateRepository string
	OfflineInstaller string
)

func Version() string {
	if releaseVersion != "" {
		return releaseVersion
	}
	return strings.TrimSpace(baseVersion)
}
