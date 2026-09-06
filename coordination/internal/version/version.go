// Package version reports build identity for /v1/version and the operator UI.
package version

import (
	"runtime"
	"runtime/debug"
	"strings"
)

// Set at link time via -ldflags "-X .../version.Version=... -X .../version.Commit=..."
var (
	Version = "dev"
	Commit  = ""
)

// Info is the JSON shape for GET /v1/version.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	GoVersion string `json:"go_version"`
	Module    string `json:"module,omitempty"`
}

// Get returns build/version metadata. Prefer ldflags; fall back to module build info.
func Get() Info {
	info := Info{
		Version:   Version,
		Commit:    Commit,
		GoVersion: runtime.Version(),
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		info.Module = bi.Main.Path
		if info.Version == "" || info.Version == "dev" {
			if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
				info.Version = bi.Main.Version
			}
		}
		if info.Commit == "" {
			for _, s := range bi.Settings {
				switch s.Key {
				case "vcs.revision":
					info.Commit = s.Value
					if len(info.Commit) > 12 {
						info.Commit = info.Commit[:12]
					}
				}
			}
		}
	}
	info.Version = strings.TrimSpace(info.Version)
	if info.Version == "" {
		info.Version = "dev"
	}
	return info
}
