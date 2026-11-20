package impact

// @module-feature impact

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/breaktest"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/results"
)

// TestPlan lists the verifier tests of the promises a change touches, and
// the tests that exercise the touched lines no promise guards, grouped by
// the configured break.runners, so a script can run only the tests that
// matter. It only lists: nothing is run.
type TestPlan struct {
	Runners   []TestRunner    `json:"runners"`
	Unmatched []UnmatchedTest `json:"unmatched"`
	// Uncovered are the touched symbols no promise guards and no evidence
	// finds a test for: run the full suite before merge.
	Uncovered []UncoveredSymbol `json:"uncovered"`
}

// TestRunner is one break.runners entry and the touched verifiers it runs.
type TestRunner struct {
	Name string `json:"name"`
	// Dir is where the command runs, relative to the directory that holds
	// lattice/; "" is that directory.
	Dir string `json:"dir"`
	// Command is the runner's command as configured, placeholders kept.
	Command string `json:"command"`
	// Run is the ready-to-run command line, from Dir: placeholders filled
	// and shell-quoted. Argv is the same command as an argument list.
	Run      string   `json:"run"`
	Argv     []string `json:"argv"`
	Files    []string `json:"files"`
	Filter   string   `json:"filter"`
	JUnit    string   `json:"junit,omitempty"`
	Tests    []string `json:"tests"`
	Promises []string `json:"promises"`
	// Why says, per test, why it is in the set: "promise <id>" for a
	// verifier of a touched promise, "unguarded <symbol> via <evidence>"
	// (coverage, call_graph or heuristic) for a test that exercises a
	// touched symbol no promise guards.
	Why map[string][]string `json:"why"`
}

// UnmatchedTest is a verifier file of a touched promise that no break.runners
// entry matches. It is listed, never dropped.
type UnmatchedTest struct {
	File     string              `json:"file"`
	Tests    []string            `json:"tests"`
	Promises []string            `json:"promises"`
	Reason   string              `json:"reason"`
	Why      map[string][]string `json:"why"`
}

// Why a verifier file is unmatched.
const (
	ReasonNoRunnerMatches = "no break.runners entry matches"
	ReasonNoRunners       = "no break.runners entry matches (none is configured)"
)

// junitName is what {junit} is filled with: a report in the runner's dir.
func junitName(r config.Runner) string { return "junit-" + r.Name + ".xml" }

// testPlan lists the verifiers of every touched promise (one an enforcer
// change touches (changed, removed or added enforcement, or an affected
// criterion's enforcer) and one a verifier change touches (a changed test
// symbol, or any change to a file that holds a verifier), and the tests that
// exercise a touched symbol no promise guards (see unguardedTests). They are
// grouped by the first break.runners entry whose glob matches, exactly as
// `lattice break` groups them; the rest are unmatched, with the reason.
// @enforces impact:INV-6
func testPlan(in Input, pm pathMap, rep Report, drafts []AffectedCriterion) TestPlan {
	kg := in.Graph
	touched := map[string]bool{}
	add := func(refs ...string) {
		for _, r := range refs {
			if r != "" {
				touched[r] = true
			}
		}
	}
	verifies := map[string][]string{} // test FQN -> promises it verifies
	byFile := map[string][]string{}   // graph path -> verifier FQNs
	for _, t := range kg.Tests {
		refs := normRefs(t.Verifies, t.Feature)
		if len(refs) == 0 {
			continue
		}
		verifies[t.FQN] = append(verifies[t.FQN], refs...)
		byFile[t.File] = append(byFile[t.File], t.FQN)
	}

	for _, cs := range rep.ChangedSymbols {
		if cs.Test {
			add(verifies[cs.Symbol]...)
			add(verifies[cs.OldSymbol]...)
			continue
		}
		add(cs.Enforces...)
	}
	for _, re := range rep.RemovedEnforcement {
		add(re.Invariant)
	}
	for _, list := range [][]AffectedCriterion{rep.AffectedCriteria, drafts} {
		for _, ac := range list {
			for _, e := range ac.Enforcers {
				add(e.Invariant)
			}
		}
	}
	for _, fc := range rep.ChangedFiles {
		if fc.NewPath == "" || fc.Status == "D" {
			continue
		}
		if gp, _, ok := pm.toGraph(fc.NewPath); ok {
			for _, fqn := range byFile[gp] {
				add(verifies[fqn]...)
			}
		}
	}

	why, uncovered := unguardedTests(in, pm, rep)
	promisesOf := map[string][]string{}
	whyOf := map[string][]string{}
	var sels []results.Selector
	for _, s := range results.Selectors(kg, false) {
		var ps []string
		for _, p := range verifies[s.Test] {
			if touched[p] && !contains(ps, p) {
				ps = append(ps, p)
			}
		}
		if len(ps) == 0 && len(why[s.Test]) == 0 {
			continue
		}
		sortRefs(ps)
		promisesOf[s.Test] = ps
		for _, p := range ps {
			whyOf[s.Test] = append(whyOf[s.Test], "promise "+p)
		}
		whyOf[s.Test] = append(whyOf[s.Test], why[s.Test]...)
		sels = append(sels, s)
	}

	plan := TestPlan{Runners: []TestRunner{}, Unmatched: []UnmatchedTest{}, Uncovered: uncovered}
	cfg := *in.Break
	groups, none := breaktest.ByRunner(cfg, filepath.Dir(in.Workspace.LatticeDir), sels, junitName)
	for _, g := range groups {
		tr := TestRunner{Name: g.Runner.Name, Dir: g.Runner.Dir, Command: strings.Join(g.Runner.Command, " "),
			Run: breaktest.ShellLine(g.Argv), Argv: g.Argv, Files: g.Files, Filter: g.Filter, JUnit: g.JUnit,
			Promises: []string{}, Why: map[string][]string{}}
		for _, s := range g.Tests {
			tr.Tests = appendNew(tr.Tests, s.Test)
			tr.Promises = appendNew(tr.Promises, promisesOf[s.Test]...)
			tr.Why[s.Test] = whyOf[s.Test]
		}
		sort.Strings(tr.Tests)
		sortRefs(tr.Promises)
		plan.Runners = append(plan.Runners, tr)
	}
	reason := ReasonNoRunnerMatches
	if len(cfg.Runners) == 0 {
		reason = ReasonNoRunners
	}
	idx := map[string]int{}
	for _, s := range none {
		i, ok := idx[s.File]
		if !ok {
			i = len(plan.Unmatched)
			idx[s.File] = i
			plan.Unmatched = append(plan.Unmatched, UnmatchedTest{File: s.File, Reason: reason,
				Promises: []string{}, Why: map[string][]string{}})
		}
		u := &plan.Unmatched[i]
		u.Tests = appendNew(u.Tests, s.Test)
		u.Promises = appendNew(u.Promises, promisesOf[s.Test]...)
		u.Why[s.Test] = whyOf[s.Test]
		sortRefs(u.Promises)
	}
	return plan
}

// sortRefs orders promise refs like INV-2 before INV-10.
func sortRefs(refs []string) {
	sort.SliceStable(refs, func(i, j int) bool { return lessID(refs[i], refs[j]) })
}

func appendNew(list []string, items ...string) []string {
	for _, s := range items {
		if !contains(list, s) {
			list = append(list, s)
		}
	}
	return list
}

// WriteTests prints the plan as a shell script: one line per runner, its
// ready-to-run command (in a subshell that enters the runner's dir) with the
// runner, its promises and the unguarded symbols it exercises in a trailing
// comment, then one comment line per unmatched test file, then the
// uncovered symbols, if any.
func WriteTests(w io.Writer, p TestPlan) {
	if len(p.Runners) == 0 && len(p.Unmatched) == 0 && len(p.Uncovered) == 0 {
		fmt.Fprintln(w, "# no touched promise has a verifier test")
		return
	}
	for _, r := range p.Runners {
		line := r.Run
		if r.Dir != "" && r.Dir != "." {
			line = "(cd " + breaktest.ShellLine([]string{r.Dir}) + " && " + r.Run + ")"
		}
		fmt.Fprintf(w, "%s  # %s: %s\n", line, r.Name, reasons(r.Promises, r.Why))
	}
	for _, u := range p.Unmatched {
		fmt.Fprintf(w, "# unmatched %s (%s): %s\n", u.File, reasons(u.Promises, u.Why), u.Reason)
	}
	if len(p.Uncovered) > 0 {
		fmt.Fprintln(w, "# uncovered: touched lines no promise guards and no known test exercises")
		for _, u := range p.Uncovered {
			loc := u.File
			if u.Lines != "" {
				loc += ":" + u.Lines
			}
			fmt.Fprintf(w, "#   %s  %s\n", loc, u.Symbol)
		}
		fmt.Fprintln(w, "# run the full suite before merge")
	}
}

// reasons is the trailing comment of a runner or unmatched line: its
// promises, then each unguarded reason of its tests.
func reasons(promises []string, why map[string][]string) string {
	parts := append([]string{}, promises...)
	var extra []string
	for _, list := range why {
		for _, r := range list {
			if strings.HasPrefix(r, "unguarded ") {
				extra = appendNew(extra, r)
			}
		}
	}
	sort.Strings(extra)
	return strings.Join(append(parts, extra...), ", ")
}
