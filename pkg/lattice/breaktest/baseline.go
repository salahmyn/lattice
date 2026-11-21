package breaktest

// @module-feature breaktest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/results"
)

// baseline is the verifiers' result on the unbroken code, for one promise.
type baseline struct {
	status    string // pass, fail, error
	reason    string
	ran       []string
	unchecked bool
}

// BaselineCounts counts the baseline invocations of one `lattice break`
// run: the ones it started, and the ones it reused from an identical
// invocation earlier in the run or from an earlier run at the same tree.
type BaselineCounts struct {
	Run    int `json:"run"`
	Reused int `json:"reused"`
}

// invocation is what one run of a group's command left: its result as the
// runner ended, and its JUnit report. A runner's or a template's result is
// kept before accounting, so each promise that shares the invocation reads
// its own verifiers from the report; Go's is accounted by runGo, and a Go
// group's key names its verifiers.
type invocation struct {
	res       execResult
	junit     bool // the command asked for a JUnit report
	report    []byte
	reportErr error
}

// result is the invocation accounted for one group's verifiers.
func (inv invocation) result(g group) execResult {
	if g.kind == kindGo {
		return inv.res
	}
	return accountReport(g, inv.res, inv.junit, inv.report, inv.reportErr)
}

// readable reports whether the JUnit report was written and parses.
func (inv invocation) readable() bool {
	if inv.reportErr != nil {
		return false
	}
	_, err := results.ParseJUnit(inv.report)
	return err == nil
}

// reusable reports whether an identical invocation later in the run may take
// this one's result. A timeout or an error (an interrupt, a runner that did
// not start, a passing run with no readable report) says nothing about the
// next run, so it is run again.
func (inv invocation) reusable() bool {
	switch inv.res.status {
	case statusTimeout, statusError:
		return false
	case statusOK:
		return !inv.junit || inv.readable()
	}
	return true
}

// persistable reports whether a later `lattice break` at the same tree may
// take this one's result. Only a pass is kept: a failing baseline can be the
// environment (a database that is down reads like a failing test), and
// running it again is how a fixed environment is noticed.
func (inv invocation) persistable() bool {
	return inv.res.status == statusOK && inv.reusable()
}

// Placeholders that stand for the worktree and the JUnit report in an
// invocation key, which must not depend on either temporary path.
const (
	worktreeToken = "{worktree}"
	junitToken    = "{junit}"
)

// keySQL stands for the SQL object files in an invocation key: they are
// temporary files, so their paths would make every key unique.
var keySQL = sqlFiles{patched: patchedSQLToken, original: originalSQLToken}

// invocationKey names what a group's baseline runs: the runner or template
// and the command exactly as rendered ({files}, {filter} and {junit}
// substituted), with the worktree and the report path as placeholders. Two
// promises whose groups render the same command share one baseline. A Go
// group is keyed by its verifiers, which name the tests `go test -run` runs.
func invocationKey(g group) string {
	d := struct {
		Kind     string
		Name     string
		Runner   *config.Runner `json:",omitempty"`
		Template string         `json:",omitempty"`
		Dir      string         `json:",omitempty"`
		Argv     []string       `json:",omitempty"`
		Tests    []string       `json:",omitempty"`
	}{Kind: g.kind.String(), Name: g.name}
	switch g.kind {
	case kindGo:
		for _, t := range g.tests {
			d.Tests = append(d.Tests, t.Test)
		}
		sort.Strings(d.Tests)
	case kindRunner:
		rn := g.runner
		d.Runner = &rn
		d.Dir, d.Argv = render(g, worktreeToken, junitToken, keySQL)
	default:
		d.Template = g.tmpl
		d.Dir, d.Argv = render(g, worktreeToken, junitToken, keySQL)
	}
	b, _ := json.Marshal(d)
	return sha(b)
}

// baseline runs the verifiers unbroken. Each group's invocation runs at
// most once per tree; see baselineGroup.
func (r *runner) baseline(ctx context.Context, groups []group, sql sqlFiles) baseline {
	var all []execResult
	for _, g := range groups {
		res := r.baselineGroup(ctx, g, sql)
		all = append(all, res)
		if res.status == statusTimeout || ctx.Err() != nil {
			break
		}
	}
	res := combine(all)
	b := baseline{status: BaselinePass, ran: res.ran, unchecked: res.unchecked}
	switch res.status {
	case statusFail:
		b = baseline{status: BaselineFail, reason: "baseline fails: " + res.reason}
	case statusOK:
	default:
		b = baseline{status: BaselineError, reason: "baseline " + res.reason}
	}
	return b
}

// baselineGroup runs one group unbroken, or takes the result of an
// identical invocation: one earlier in this run, or a passing one that an
// earlier run kept for the same tree. Either way the group's own verifiers
// are accounted from that invocation's report, so a shared run never counts
// a verifier as executed that it did not execute.
// @enforces breaktest:INV-13
func (r *runner) baselineGroup(ctx context.Context, g group, sql sqlFiles) execResult {
	key := invocationKey(g)
	if inv, ok := r.shared[key]; ok {
		r.counts.Reused++
		return inv.result(g)
	}
	if inv, ok := r.store.get(key, g); ok {
		r.counts.Reused++
		r.shared[key] = inv
		return inv.result(g)
	}
	inv := r.invoke(ctx, g, sql)
	r.counts.Run++
	if inv.reusable() {
		r.shared[key] = inv
	}
	if inv.persistable() {
		r.store.put(key, g, inv)
	}
	return inv.result(g)
}

// BaselineSchema is the version stamp of break-baselines.json.
const BaselineSchema = "lattice.break-baselines/1"

// BaselinesPath is where the passing baselines of the last tree are kept.
func BaselinesPath(latticeDir string) string {
	return filepath.Join(latticeDir, ".cache", "results", "break-baselines.json")
}

// baselineFile is break-baselines.json: the passing baselines of one tree,
// by invocation key. Stamp covers everything besides the invocation that a
// baseline depends on, so a file with another stamp is ignored and replaced.
type baselineFile struct {
	Schema    string                       `json:"schema"`
	Tree      string                       `json:"tree"`
	Stamp     string                       `json:"stamp"`
	Baselines map[string]persistedBaseline `json:"baselines"`
}

// persistedBaseline is a passing invocation: its JUnit report for a runner
// or a template, the verifiers that ran for Go.
type persistedBaseline struct {
	JUnit    string   `json:"junit,omitempty"`
	Ran      []string `json:"ran,omitempty"`
	Executed int      `json:"executed,omitempty"`
}

// baselineStamp is what a persisted baseline depends on besides its
// invocation: the HEAD tree, the code root's place in the checkout, the
// setup commands, the node_modules link and the generator version.
func baselineStamp(tree, prefix string, cfg config.Break) string {
	b, _ := json.Marshal(struct {
		Schema, Version, Tree, Prefix string
		Setup                         []string
		Link                          bool
	}{BaselineSchema, GeneratorVersion, tree, prefix, cfg.Setup, cfg.LinksNodeModules()})
	return sha(b)
}

// loadBaselines reads the kept baselines for a tree and stamp; a missing,
// unreadable or stale file is an empty one.
func loadBaselines(latticeDir, tree, stamp string) *baselineFile {
	f := &baselineFile{Schema: BaselineSchema, Tree: tree, Stamp: stamp, Baselines: map[string]persistedBaseline{}}
	data, err := os.ReadFile(BaselinesPath(latticeDir))
	if err != nil {
		return f
	}
	var old baselineFile
	if json.Unmarshal(data, &old) != nil || old.Schema != BaselineSchema || old.Stamp != stamp || old.Tree != tree {
		return f
	}
	for k, v := range old.Baselines {
		f.Baselines[k] = v
	}
	return f
}

// get returns the kept baseline of an invocation. A nil file (a dirty
// tree) keeps none.
func (f *baselineFile) get(key string, g group) (invocation, bool) {
	if f == nil {
		return invocation{}, false
	}
	p, ok := f.Baselines[key]
	if !ok {
		return invocation{}, false
	}
	if g.kind == kindGo {
		return invocation{res: execResult{status: statusOK, ran: p.Ran, executed: p.Executed}}, true
	}
	inv := invocation{res: execResult{status: statusOK}, junit: g.usesJUnit()}
	if inv.junit {
		inv.report = []byte(p.JUnit)
		if !inv.readable() {
			return invocation{}, false
		}
	}
	return inv, true
}

// put keeps a passing invocation. A nil file keeps nothing.
func (f *baselineFile) put(key string, g group, inv invocation) {
	if f == nil {
		return
	}
	p := persistedBaseline{}
	switch {
	case g.kind == kindGo:
		p.Ran, p.Executed = inv.res.ran, inv.res.executed
	case inv.junit:
		p.JUnit = string(inv.report)
	}
	f.Baselines[key] = p
}

// write saves the file next to breaks.json.
func (f *baselineFile) write(latticeDir string) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	p := BaselinesPath(latticeDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0o644)
}
