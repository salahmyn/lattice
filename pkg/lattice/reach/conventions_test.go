package reach_test

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/golang"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/python"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/typescript"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// The runtime conventions each adapter records: Go's String/Error/
// UnmarshalYAML... are reached with their type and package-level var
// initialisers run as top-level code; a TS import('./page') uses the
// page's default export and require() binds like an import; Python's
// `if __name__ == "__main__":` is a root and a class reaches its dunders.
// @verifies reachability:INV-1, reachability:INV-3
func TestAdapterConventions(t *testing.T) {
	mods := parse(t, golang.New(), map[string]string{
		"cmd/app/main.go": `package main

import "example.com/m/pkg/kind"

var registry = map[string]func(){"x": kind.Register}

func main() { _ = kind.Level(1) }
`,
		"pkg/kind/kind.go": `package kind

type Level int

func (l Level) String() string { return "" }
func (l Level) unused() {}

func Register() {}
`,
		"pkg/other/other.go": "package other\n\nimport _ \"example.com/m/pkg/kind\"\n",
	})
	mods = append(mods, parse(t, typescript.New(), map[string]string{
		"web/router.ts":     "export const Page = lazy(() => import('./pages/home'));\nconst { tool } = require('./tool');\nexport function go() { tool(); }\n",
		"web/pages/home.ts": "export default function Home() {}\n",
		"web/tool.ts":       "export function tool() {}\n",
	})...)
	mods = append(mods, parse(t, python.New(), map[string]string{
		"jobs/run.py": `class Job:
    def __init__(self):
        pass

    def __str__(self):
        return ""

    def unused(self):
        pass


def work():
    Job()


if __name__ == "__main__":
    work()
`,
	})...)
	g := reach.Build(mods, reach.Options{})
	roots := g.Roots(reach.RootInput{EntryFiles: []reach.EntryFile{{File: "web/router.ts", Reason: "index.html"}}})
	r := reach.Classify(g, roots)
	for fqn, want := range map[string]string{
		"pkg.kind.Level":        schema.ReachEntry,
		"pkg.kind.Level.String": schema.ReachEntry, // fmt calls it: reached with the type
		"pkg.kind.Level.unused": schema.ReachUnreached,
		"pkg.kind.Register":     schema.ReachEntry, // a package-level var initialiser names it
		"web.pages.home.Home":   schema.ReachEntry, // import('./pages/home')
		"web.tool.tool":         schema.ReachUnreached,
		"jobs.run.work":         schema.ReachEntry, // the __main__ block
		"jobs.run.Job.__init__": schema.ReachEntry,
		"jobs.run.Job.__str__":  schema.ReachEntry,
		"jobs.run.Job.unused":   schema.ReachUnreached,
	} {
		if got := r.Class[fqn]; got != want {
			t.Errorf("%s: class %q, want %q", fqn, got, want)
		}
	}
	if !hasEdge(g, "web.router.go", "web.tool.tool") {
		t.Errorf("require() binds like an import:%s", joined(edges(g)))
	}
	if !hasEdge(g, "pkg.other.<top>", "pkg.kind.<top>") {
		t.Errorf("a blank import loads the package:%s", joined(edges(g, "import")))
	}
}
