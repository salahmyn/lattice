package reach_test

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/golang"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Go build-constrained files declare the same name once per platform. The
// adapter keeps the first FQN and suffixes the others; a call resolves to
// the first by name, so every variant must be reached with it, methods of a
// variant type included.
// @verifies adapters.go:INV-5
func TestGoBuildVariantsAreReached(t *testing.T) {
	mods := parse(t, golang.New(), map[string]string{
		"cmd/tool/main.go": `package main

import "example.com/m/pkg/proc"

func main() {
	proc.Start()
}
`,
		"pkg/proc/proc.go": `package proc

func Start() {
	setGroup()
	var h handle
	h.close()
}
`,
		"pkg/proc/proc_other.go": `//go:build !unix

package proc

func setGroup() {}

type handle struct{}

func (handle) close() {}
`,
		"pkg/proc/proc_unix.go": `//go:build unix

package proc

func setGroup() {}

type handle struct{}

func (handle) close() {}
`,
		"pkg/proc/proc_test.go": `package proc_test

func setGroup() {}
`,
	})
	g := reach.Build(mods, reach.Options{})
	res := reach.Classify(g, g.Roots(reach.RootInput{}))
	for _, fqn := range []string{
		"pkg.proc.setGroup", "pkg.proc.setGroup@proc_unix",
		"pkg.proc.handle.close", "pkg.proc.handle@proc_unix.close",
	} {
		if c := res.Class[fqn]; c != schema.ReachEntry {
			t.Errorf("%s: class %q, want entry; edges%s", fqn, c, joined(edges(g)))
		}
	}
	// The external test package's own setGroup is no variant of the
	// production one.
	if hasEdge(g, "pkg.proc.setGroup", "pkg.proc.setGroup@proc_test") {
		t.Errorf("a test package's declaration was linked as a build variant")
	}
}

// Go: an unexported method name is distinct in every package, so x.run()
// on a value of unknown type names only a run of the caller's package. A
// same-named unexported method of another package is no candidate: it must
// neither make the call ambiguous nor resolve it when it is the only one.
// @verifies reachability:INV-9
func TestGoUnexportedMembersStayInPackage(t *testing.T) {
	mods := parse(t, golang.New(), map[string]string{
		"pkg/check/check.go": `package check

type computation struct{}

func (c *computation) run() {}

func (c *computation) render() {}

type Report struct{}

func (Report) Write() {}
`,
		"pkg/impact/git.go": `package impact

type repoGit struct{}

func (r repoGit) run() {}

func open() repoGit { return repoGit{} }

func Top() {
	g := open()
	g.run()
	v := lookup()
	v.render()
	v.Write()
}

func lookup() any { return nil }
`,
	})
	g := reach.Build(mods, reach.Options{})
	checkResolution(t, g, []resolveCase{
		{what: "own package's unexported method", from: "pkg.impact.Top", to: "pkg.impact.repoGit.run"},
		{what: "exported method stays visible", from: "pkg.impact.Top", to: "pkg.check.Report.Write"},
	}, [][2]string{
		{"pkg.impact.Top", "pkg.check.computation.run"},
		{"pkg.impact.Top", "pkg.check.computation.render"},
	})
	for _, u := range g.Unresolved {
		for _, c := range u.Candidates {
			if c == "pkg.check.computation.run" {
				t.Errorf("%s %s: another package's unexported method is a candidate: %v", u.From, u.Name, u.Candidates)
			}
		}
	}
}
