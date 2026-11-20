package breaktest

// @module-feature breaktest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// FailsOnBaseSchema is the version stamp of fails-on-base.json. The check
// reads the file by this contract and does not import this package.
const FailsOnBaseSchema = "lattice.fails_on_base/1"

// Outcomes of a fails-on-base run. Unknown (shared with breaks) is never a
// pass and never a fail.
const (
	FailsOnBase  = "fails_on_base"  // the test detects the old behaviour
	PassesOnBase = "passes_on_base" // the test does not tell old from new
)

// At-head results of a verifier, run before the base.
const (
	HeadPass  = "pass"
	HeadFail  = "fail"
	HeadError = "error"
)

// Change is what a fails-on-base run needs from the change under review
// (lattice impact computes it): the base, the verifier tests the change adds
// or edits, and every path that differs between the base and the working
// tree.
type Change struct {
	Rev  string // the base as the user wrote it
	Base string // merge-base(Rev, HEAD)
	// Tests are the changed test symbols: FQN to "added" or "modified".
	Tests map[string]string
	// Files are the paths, relative to the checkout's top, that differ
	// between Base and the working tree, both sides of a rename included.
	Files []string
}

// FailsOnBaseReport is lattice/.cache/results/fails-on-base.json.
type FailsOnBaseReport struct {
	Schema  string `json:"schema"`
	BaseRev string `json:"base_rev"`
	Base    string `json:"base"`
	Head    string `json:"head"`
	// CIRun is the CI run that wrote the file, or "local" (results.CIRunID).
	CIRun       string `json:"ci_run,omitempty"`
	Dirty       bool   `json:"dirty"`
	GeneratedAt string `json:"generated_at"`
	// Overlay are the working-tree files copied onto the base commit, relative
	// to the checkout's top: the verifier files, the changed test files of
	// their directories and the changed files under their testdata/.
	Overlay   []string  `json:"overlay"`
	Verifiers []BaseRun `json:"verifiers"`
}

// BaseRun is one new or changed verifier, run at head and on the base code.
type BaseRun struct {
	Verifier string   `json:"verifier"`
	File     string   `json:"file"`
	Promises []string `json:"promises"`
	Change   string   `json:"change"` // added | modified
	// Head is the verifier's result at head; empty when it never ran.
	Head        string   `json:"head,omitempty"`
	Outcome     string   `json:"outcome"`
	Reason      string   `json:"reason,omitempty"`
	FailedTests []string `json:"failed_tests,omitempty"`
	DurationMS  int64    `json:"duration_ms"`
}

// FailsOnBasePath is where fails-on-base.json lives under the lattice directory.
func FailsOnBasePath(latticeDir string) string {
	return filepath.Join(latticeDir, ".cache", "results", "fails-on-base.json")
}

// ReadFailsOnBase loads fails-on-base.json. A missing or unreadable file is
// an empty report.
func ReadFailsOnBase(latticeDir string) FailsOnBaseReport {
	var r FailsOnBaseReport
	data, err := os.ReadFile(FailsOnBasePath(latticeDir))
	if err != nil || json.Unmarshal(data, &r) != nil || r.Schema != FailsOnBaseSchema {
		return FailsOnBaseReport{}
	}
	return r
}

// WriteFailsOnBase saves the report, verifiers sorted by name.
func WriteFailsOnBase(latticeDir string, r FailsOnBaseReport) error {
	r.Schema = FailsOnBaseSchema
	if r.CIRun == "" {
		r.CIRun = results.CIRunID()
	}
	if r.GeneratedAt == "" {
		r.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if r.Verifiers == nil {
		r.Verifiers = []BaseRun{}
	}
	if r.Overlay == nil {
		r.Overlay = []string{}
	}
	sort.SliceStable(r.Verifiers, func(i, j int) bool { return r.Verifiers[i].Verifier < r.Verifiers[j].Verifier })
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	p := FailsOnBasePath(latticeDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0o644)
}

// baseTarget is one verifier to run on the base.
type baseTarget struct {
	test     schema.GraphSymbol
	sel      results.Selector
	promises []string
	change   string
}

// RunFailsOnBase runs the new and changed verifiers of the selected promises
// twice: at head (HEAD plus the working tree's changes), then on the base
// commit's code with the head's test files copied on top. Both run in
// temporary worktrees; the working tree is only read. A dirty tree is
// refused unless AllowDirty, and recorded truthfully either way.
// @enforces breaktest:INV-2, breaktest:INV-10
func RunFailsOnBase(ctx context.Context, kg schema.KnowledgeGraph, opts Options, ch Change) (FailsOnBaseReport, error) {
	rep := FailsOnBaseReport{Schema: FailsOnBaseSchema, BaseRev: ch.Rev, Base: ch.Base,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339), Overlay: []string{}, Verifiers: []BaseRun{}}
	repo, err := OpenRepo(ctx, opts.CodeRoot, opts.LatticeDir)
	if err != nil {
		return rep, err
	}
	if repo.Dirty && !opts.AllowDirty {
		return rep, fmt.Errorf("%w: %s", ErrDirty, nameFew(repo.DirtyFiles, 5))
	}
	rep.Head, rep.Dirty, rep.CIRun = repo.Head, repo.Dirty, results.CIRunID()
	if opts.Timeout <= 0 {
		opts.Timeout = 120 * time.Second
	}

	targets := baseTargets(kg, opts, ch)
	rep.Overlay = overlayFiles(kg, repo.Prefix, targets, ch.Files)
	if len(targets) > 0 {
		head, err := NewWorktree(ctx, repo, repo.Dirty)
		if err != nil {
			return rep, err
		}
		defer head.Close()
		base, err := NewWorktreeAt(ctx, repo, ch.Base)
		if err != nil {
			return rep, err
		}
		defer base.Close()
		if err := base.CopyIn(rep.Overlay); err != nil {
			return rep, err
		}
		rh := &runner{opts: opts, repo: repo, wt: head, baselines: map[string]baseline{}}
		rb := &runner{opts: opts, repo: repo, wt: base, baselines: map[string]baseline{}}
		for _, t := range targets {
			if err := ctx.Err(); err != nil {
				return rep, err
			}
			start := time.Now()
			run := runOnBase(ctx, rh, rb, t)
			run.DurationMS = time.Since(start).Milliseconds()
			rep.Verifiers = append(rep.Verifiers, run)
		}
	}

	// A filtered run keeps the other verifiers of the same change.
	if opts.Promise != "" || opts.Feature != "" {
		prior := ReadFailsOnBase(opts.LatticeDir)
		if prior.Base == rep.Base && prior.Head == rep.Head && prior.Dirty == rep.Dirty {
			have := map[string]bool{}
			for _, v := range rep.Verifiers {
				have[v.Verifier] = true
			}
			for _, v := range prior.Verifiers {
				if !have[v.Verifier] {
					rep.Verifiers = append(rep.Verifiers, v)
				}
			}
		}
	}
	return rep, nil
}

// baseTargets lists the verifier tests the change adds or edits, of the
// promises the options select. A verifier of an untouched promise does not
// exist: editing it touches its promise.
func baseTargets(kg schema.KnowledgeGraph, opts Options, ch Change) []baseTarget {
	sels := map[string]results.Selector{}
	for _, s := range results.Selectors(kg, true) {
		sels[s.Test] = s
	}
	by := map[string]*baseTarget{}
	for _, t := range kg.Tests {
		change, ok := ch.Tests[t.FQN]
		if !ok || len(t.Verifies) == 0 {
			continue
		}
		var promises []string
		for _, v := range t.Verifies {
			if p := promiseKey(t.Feature, v); opts.Selects(p) {
				promises = append(promises, p)
			}
		}
		if len(promises) == 0 {
			continue
		}
		bt := by[t.FQN]
		if bt == nil {
			bt = &baseTarget{test: t, sel: sels[t.FQN], change: change}
			by[t.FQN] = bt
		}
		bt.promises = append(bt.promises, promises...)
	}
	out := make([]baseTarget, 0, len(by))
	for _, k := range keys(boolSet(by)) {
		bt := by[k]
		bt.promises = uniqueSorted(bt.promises)
		out = append(out, *bt)
	}
	return out
}

// overlayFiles picks the working-tree files copied onto the base: each
// verifier's file, and of the changed paths, the test files in a verifier's
// directory (helpers a new test may need, or that moved out of a file the
// base still has) and anything under that directory's testdata/. Production
// code is never copied: it is what the run compares.
func overlayFiles(kg schema.KnowledgeGraph, prefix string, targets []baseTarget, changed []string) []string {
	toTop := func(f string) string {
		if prefix == "" {
			return f
		}
		return path.Join(filepath.ToSlash(prefix), f)
	}
	testFiles := map[string]bool{}
	for _, t := range kg.Tests {
		testFiles[toTop(t.File)] = true
	}
	dirs := map[string]bool{}
	picked := map[string]bool{}
	for _, t := range targets {
		f := toTop(t.test.File)
		picked[f] = true
		dirs[path.Dir(f)] = true
	}
	for _, c := range changed {
		c = filepath.ToSlash(c)
		dir := path.Dir(c)
		switch {
		case dirs[dir] && (testFiles[c] || isTestFileName(path.Base(c))):
			picked[c] = true
		default:
			for d := range dirs {
				if strings.HasPrefix(c, path.Join(d, "testdata")+"/") {
					picked[c] = true
				}
			}
		}
	}
	return keys(picked)
}

// isTestFileName recognises the usual test file names, for a changed file
// the graph does not hold (one the change deletes, or one with no test).
func isTestFileName(base string) bool {
	switch {
	case strings.HasSuffix(base, "_test.go"), strings.HasSuffix(base, "_test.py"),
		strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py"), base == "conftest.py":
		return true
	}
	return strings.Contains(base, ".test.") || strings.Contains(base, ".spec.")
}

// runOnBase runs one verifier at head, then on the base. Only a verifier
// that passes at head and fails on the base reads fails_on_base; only one
// that passes at head and runs and passes on the base reads passes_on_base.
// Everything else is unknown with its reason.
// @enforces breaktest:INV-10
func runOnBase(ctx context.Context, rh, rb *runner, t baseTarget) BaseRun {
	run := BaseRun{Verifier: t.test.FQN, File: t.test.File, Promises: t.promises, Change: t.change, Outcome: Unknown}
	if t.sel.Test == "" {
		run.Reason = "no test selector for this verifier"
		return run
	}
	groups, none := rh.groups([]results.Selector{t.sel})
	if len(groups) == 0 {
		run.Reason = noRunnerReason(none)
		return run
	}
	if err := rh.ensureSetup(ctx); err != nil {
		run.Head, run.Reason = HeadError, "at head: "+err.Error()
		return run
	}
	head := rh.runGroups(ctx, groups)
	switch {
	case head.status == statusFail:
		run.Head, run.Reason = HeadFail, "fails at head too, so the base run says nothing: "+head.reason
		return run
	case head.status != statusOK || !contains(head.ran, t.test.FQN):
		why := head.reason
		if why == "" {
			why = "the verifier did not run"
		}
		run.Head, run.Reason = HeadError, "at head: "+why
		return run
	}
	run.Head = HeadPass
	if err := rb.ensureSetup(ctx); err != nil {
		run.Reason = "on base: " + err.Error()
		return run
	}
	base := rb.runGroups(ctx, groups)
	run.Outcome, run.Reason, run.FailedTests = judgeBase(groups[0].kind == kindGo, head, base, t.test.FQN)
	return run
}

// judgeBase reads the base run of a verifier that passed at head. A failure
// counts only when the test itself failed: a non-Go test whose import does
// not resolve on the base (a module or export the change adds) did not load,
// so it reads unknown, like a Go test that does not compile there. goRun says
// the run was Go's built-in `go test`, whose build failures are their own
// status.
// @enforces breaktest:INV-10
func judgeBase(goRun bool, head, base execResult, fqn string) (outcome, reason string, failed []string) {
	note := ""
	if head.unchecked || base.unchecked {
		note = " (executed test count not checked: the runner has no {junit})"
	}
	if !goRun && (base.status == statusFail || base.status == statusBuild) {
		if l := loadLine(base.output); l != "" {
			return Unknown, "does not load on base (a module or export it imports may be new in this change): " + l, nil
		}
	}
	switch base.status {
	case statusFail:
		return FailsOnBase, base.reason + note, base.failed
	case statusOK:
		if !contains(base.ran, fqn) {
			return Unknown, "on base: the verifier did not run", nil
		}
		return PassesOnBase, "passes on the base code too: it does not tell the old behaviour from the new" + note, nil
	case statusBuild:
		why := strings.TrimPrefix(base.reason, "does not compile: ")
		if l := compilerLine.FindString(base.output); l != "" {
			why = clip(l)
		}
		return Unknown, "does not compile on base (an API it calls may be new in this change): " + why, nil
	}
	// A timeout, a runner that could not run, or no test executed.
	return Unknown, "on base: " + base.reason, nil
}

// loadMarkers are the messages test runners print when a test file cannot
// load because an import does not resolve: Node, vitest, jest, tsx, Vite,
// webpack, tsc, Deno, Python and pytest. Only resolution and load errors are
// listed; an assertion or a TypeError in a test body is a real failure.
var loadMarkers = regexp.MustCompile(`does not provide an export named|Cannot find module|ERR_MODULE_NOT_FOUND|` +
	`Failed to resolve import|Module not found|has no exported member|\bTS(2305|2307|2724)\b|` +
	`SyntaxError: The requested module|\bImportError\b|\bModuleNotFoundError\b|cannot import name|ERROR collecting`)

// loadLine returns the first line of a runner's output that says a test
// file did not load, "" when there is none.
func loadLine(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if loadMarkers.MatchString(l) {
			return clip(l)
		}
	}
	return ""
}

// compilerLine is a compiler's "file:line:col: message" line, which says
// more than a runner's summary of a build failure.
var compilerLine = regexp.MustCompile(`(?m)^\s*\S+:\d+:\d+: .+$`)

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func boolSet[V any](m map[string]V) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

func uniqueSorted(in []string) []string {
	m := map[string]bool{}
	for _, s := range in {
		m[s] = true
	}
	return keys(m)
}
