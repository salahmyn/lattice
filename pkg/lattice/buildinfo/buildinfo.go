// Package buildinfo carries version metadata stamped in at link time.
package buildinfo

// These are overridden via -ldflags -X at build time (see .goreleaser.yaml).
var (
	Version = "0.7.0"
	Commit  = "none"
	Date    = "unknown"
)

// SchemaVersion is the version of the lattice.json knowledge-graph schema.
// It is independent of the binary version.
const SchemaVersion = "1.0"
