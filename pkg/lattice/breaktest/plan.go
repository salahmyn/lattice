package breaktest

// @module-feature breaktest

import (
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/results"
)

// RunnerTests is the verifiers one break.runners entry runs in one
// invocation, and the command that runs only them, as `lattice break`
// builds it. It only describes the command; nothing is run.
type RunnerTests struct {
	Runner config.Runner
	// Files are the verifier files as the command receives them: relative to
	// Runner.Dir when it is set (absolute when a file is outside it).
	Files []string
	// Filter is the {filter} value: a pattern selecting only the verifiers.
	Filter string
	// JUnit is the value {junit} was filled with, "" when the command has none.
	JUnit string
	// Argv is the command with every placeholder filled.
	Argv  []string
	Tests []results.Selector
}

// ByRunner groups verifiers the way `lattice break` does, by the first
// break.runners entry whose glob matches the verifier's file, and fills each
// runner's command. Verifiers no break.runners entry matches are returned
// apart, including ones `lattice break` would run with `go test` or a
// break.commands template. root is the directory that holds lattice/; junit
// names the {junit} report of a runner whose command has one.
func ByRunner(cfg config.Break, root string, sels []results.Selector, junit func(config.Runner) string) ([]RunnerTests, []results.Selector) {
	groups, none := groupVerifiers(cfg, sels)
	var out []RunnerTests
	for _, g := range groups {
		if g.kind != kindRunner {
			none = append(none, g.tests...)
			continue
		}
		files, filter := templateRaw(g)
		files = relativeTo(root, g.runner.Dir, files)
		j := ""
		if g.usesJUnit() && junit != nil {
			j = junit(g.runner)
		}
		out = append(out, RunnerTests{Runner: g.runner, Files: files, Filter: filter, JUnit: j,
			Argv: expandArgv(g.runner.Command, files, filter, j), Tests: g.tests})
	}
	sort.SliceStable(none, func(i, j int) bool {
		if none[i].File != none[j].File {
			return none[i].File < none[j].File
		}
		return none[i].Test < none[j].Test
	})
	return out, none
}

// ShellLine joins an argument list into one shell command line, quoting only
// the arguments that need it.
func ShellLine(argv []string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		if a != "" && strings.Trim(a, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./:=@%+,") == "" {
			out[i] = a
		} else {
			out[i] = shellQuote(a)
		}
	}
	return strings.Join(out, " ")
}
