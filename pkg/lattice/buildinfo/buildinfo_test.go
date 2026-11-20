package buildinfo

import (
	"runtime/debug"
	"testing"
)

// A source build with no link-time flags reports the commit and time the Go
// toolchain embedded, marks a modified tree, and takes the module version of
// `go install ...@vX.Y.Z`; values set by -ldflags are never replaced.
// @verifies cli:INV-3
func TestFillFromBuildInfo(t *testing.T) {
	reset := func(v, c, d string) { Version, Commit, Date = v, c, d }
	defer reset(Version, Commit, Date)

	settings := []debug.BuildSetting{
		{Key: "vcs.revision", Value: "c87509a605597d88bb23d5f166e0f200fe698eea"},
		{Key: "vcs.time", Value: "2026-11-20T21:00:00Z"},
		{Key: "vcs.modified", Value: "true"},
	}

	reset("dev", "none", "unknown")
	fill(&debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: settings})
	if Version != "dev" || Commit != "c87509a605597d88bb23d5f166e0f200fe698eea-dirty" || Date != "2026-11-20T21:00:00Z" {
		t.Errorf("source build: %s %s %s", Version, Commit, Date)
	}

	reset("dev", "none", "unknown")
	fill(&debug.BuildInfo{Main: debug.Module{Version: "v0.16.0"}})
	if Version != "0.16.0" || Commit != "none" {
		t.Errorf("go install: %s %s", Version, Commit)
	}

	reset("0.15.1", "7b28cd4", "2026-11-10T19:00:00Z")
	fill(&debug.BuildInfo{Main: debug.Module{Version: "v9.9.9"}, Settings: settings})
	if Version != "0.15.1" || Commit != "7b28cd4" || Date != "2026-11-10T19:00:00Z" {
		t.Errorf("link-time values were replaced: %s %s %s", Version, Commit, Date)
	}
}
