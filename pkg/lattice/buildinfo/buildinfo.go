// Package buildinfo carries version metadata stamped in at link time.
package buildinfo

// @module-feature cli

import (
	"runtime/debug"
	"strings"
)

// These are overridden via -ldflags -X at build time (see .github/workflows/release.yml).
// A build without them (go build, go install) reads the module version and the
// VCS stamp the Go toolchain embeds, so a source build still says what it is.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// SchemaVersion is the version of the lattice.json knowledge-graph schema.
// It is independent of the binary version.
const SchemaVersion = "1.0"

func init() {
	if bi, ok := debug.ReadBuildInfo(); ok {
		fill(bi)
	}
}

// fill sets the fields that link-time flags left at their defaults from the
// build information: the module version for `go install ...@vX.Y.Z`, and the
// commit, its time and whether the tree was modified for a source build.
// @enforces cli:INV-3
func fill(bi *debug.BuildInfo) {
	if Version == "dev" {
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			Version = strings.TrimPrefix(v, "v")
		}
	}
	var rev, at string
	modified := false
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			at = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if Commit == "none" && rev != "" {
		Commit = rev
		if modified {
			Commit += "-dirty"
		}
	}
	if Date == "unknown" && at != "" {
		Date = at
	}
}
