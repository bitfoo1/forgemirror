// Package version reports which build of forgemirror is running.
//
// The values come from the build info the Go toolchain embeds into every
// binary (module version from the VCS tag, commit, dirty flag). That replaces
// -ldflags "-X ..." stamping: no mutable globals, and `go install ...@vX.Y.Z`,
// `go build` and goreleaser all report the same version without extra flags.
package version

import (
	"fmt"
	"runtime/debug"
)

// develVersion is what the toolchain reports when it cannot derive a version
// from VCS, e.g. `go run` or a build outside a git checkout.
const develVersion = "(devel)"

// Info describes the running binary.
type Info struct {
	Version   string
	Commit    string
	GoVersion string
	Modified  bool
}

// Get returns the build information of the running binary.
func Get() Info {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return Info{Version: develVersion}
	}

	return FromBuildInfo(bi)
}

// FromBuildInfo extracts an [Info] from a [debug.BuildInfo].
func FromBuildInfo(bi *debug.BuildInfo) Info {
	info := Info{
		Version:   bi.Main.Version,
		GoVersion: bi.GoVersion,
	}
	if info.Version == "" {
		info.Version = develVersion
	}

	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			info.Commit = s.Value
		case "vcs.modified":
			info.Modified = s.Value == "true"
		}
	}

	return info
}

// String renders the info for `forgemirror --version` and the startup log.
func (i Info) String() string {
	commit := i.Commit
	if commit == "" {
		commit = "unknown"
	}
	if i.Modified {
		commit += "-dirty"
	}

	return fmt.Sprintf("%s (commit %s, %s)", i.Version, commit, i.GoVersion)
}
