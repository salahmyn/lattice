package breaktest

// @module-feature breaktest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// ErrDirty is returned for a working tree with uncommitted changes when
// AllowDirty is off.
var ErrDirty = errors.New("working tree has uncommitted changes (commit them, or pass --allow-dirty)")

// Options controls a break run.
type Options struct {
	Promise    string // "feature:INV-n", "" for every promise
	Feature    string // limit to one feature, "" for all
	Timeout    time.Duration
	AllowDirty bool
	Config     config.Config
	LatticeDir string // absolute path of lattice/
	CodeRoot   string // absolute path of the primary code root
}

// target is one run to make: an enforcer of a promise, and the verifiers
// that should notice when it breaks.
type target struct {
	promise   string
	enforcers []schema.GraphSymbol // one, or all of the promise's for a script
	verifiers []results.Selector
	tests     []schema.GraphSymbol
	script    string // absolute path of the patch, "" for a generated break
}

// promiseKey normalises an invariant reference to "feature:INV-n".
func promiseKey(feature, ref string) string {
	if strings.Contains(ref, ":") {
		return ref
	}
	return feature + ":" + ref
}

// plan lists the runs: one per (promise, enforcer), or one per promise when
// a script replaces the generated break. Promises without an enforcer have
// nothing to break and no run.
func plan(kg schema.KnowledgeGraph, opts Options) []target {
	enforcers := map[string][]schema.GraphSymbol{}
	for _, s := range kg.Symbols {
		for _, inv := range s.EnforcesInvariants {
			k := promiseKey(s.Feature, inv)
			enforcers[k] = append(enforcers[k], s)
		}
	}
	selectors := map[string]results.Selector{}
	for _, sel := range results.Selectors(kg, true) {
		selectors[sel.Test] = sel
	}
	type pair struct {
		sels  []results.Selector
		tests []schema.GraphSymbol
	}
	verifiers := map[string]*pair{}
	for _, t := range kg.Tests {
		for _, v := range t.Verifies {
			k := promiseKey(t.Feature, v)
			p := verifiers[k]
			if p == nil {
				p = &pair{}
				verifiers[k] = p
			}
			p.sels = append(p.sels, selectors[t.FQN])
			p.tests = append(p.tests, t)
		}
	}

	keys := make([]string, 0, len(enforcers))
	for k := range enforcers {
		if opts.Promise != "" && k != opts.Promise {
			continue
		}
		if opts.Feature != "" && !strings.HasPrefix(k, opts.Feature+":") {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out []target
	for _, k := range keys {
		syms := dedupSymbols(enforcers[k])
		var sels []results.Selector
		var tests []schema.GraphSymbol
		if p := verifiers[k]; p != nil {
			sels, tests = p.sels, dedupSymbols(p.tests)
		}
		feature, inv, _ := strings.Cut(k, ":")
		script := filepath.Join(opts.LatticeDir, "breaks", feature, inv+".patch")
		if _, err := os.Stat(script); err != nil {
			script = ""
		}
		if script != "" {
			out = append(out, target{promise: k, enforcers: syms, verifiers: sels, tests: tests, script: script})
			continue
		}
		for _, s := range syms {
			out = append(out, target{promise: k, enforcers: []schema.GraphSymbol{s}, verifiers: sels, tests: tests})
		}
	}
	return out
}

func dedupSymbols(in []schema.GraphSymbol) []schema.GraphSymbol {
	seen := map[string]bool{}
	var out []schema.GraphSymbol
	for _, s := range in {
		if !seen[s.FQN] {
			seen[s.FQN] = true
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FQN < out[j].FQN })
	return out
}

// runner carries the state of one `lattice break` invocation.
type runner struct {
	opts      Options
	wt        *Worktree
	prior     map[[4]string]Run
	baselines map[string]baseline // by verifier set, shared by promises
}

type baseline struct {
	status string // pass, fail, error
	reason string
}

// Execute breaks each promise's enforcer on a temporary worktree and reports
// whether its verifiers noticed. The report is returned, not written.
// Without AllowDirty a working tree with uncommitted changes is refused.
// @enforces breaktest:INV-2
func Execute(ctx context.Context, kg schema.KnowledgeGraph, opts Options) (Report, error) {
	rep := Report{Schema: Schema, GeneratedAt: time.Now().UTC().Format(time.RFC3339), Runs: []Run{}}
	repo, err := OpenRepo(ctx, opts.CodeRoot)
	if err != nil {
		return rep, err
	}
	if repo.Dirty && !opts.AllowDirty {
		return rep, ErrDirty
	}
	rep.Commit, rep.Dirty = repo.Head, repo.Dirty
	if opts.Timeout <= 0 {
		opts.Timeout = 120 * time.Second
	}

	targets := plan(kg, opts)
	prior := Read(opts.LatticeDir)
	r := &runner{opts: opts, prior: map[[4]string]Run{}, baselines: map[string]baseline{}}
	for _, p := range prior.Runs {
		if p.cacheable() {
			r.prior[p.cacheKey()] = p
		}
	}
	if len(targets) > 0 {
		if r.wt, err = NewWorktree(ctx, repo, repo.Dirty); err != nil {
			return rep, err
		}
		defer r.wt.Close()
	}
	for _, t := range targets {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		run, err := r.runTarget(ctx, t)
		if err != nil {
			return rep, err
		}
		rep.Runs = append(rep.Runs, run)
	}

	// A filtered run keeps the other promises' runs from the same commit, so
	// one promise can be re-broken without losing the rest of the file.
	if (opts.Promise != "" || opts.Feature != "") && prior.Commit == rep.Commit && prior.Dirty == rep.Dirty {
		have := map[string]bool{}
		for _, run := range rep.Runs {
			have[run.Promise] = true
		}
		for _, p := range prior.Runs {
			if !have[p.Promise] && !selected(p.Promise, opts) {
				rep.Runs = append(rep.Runs, p)
			}
		}
	}
	return rep, nil
}

func selected(promise string, opts Options) bool {
	if opts.Promise != "" {
		return promise == opts.Promise
	}
	return strings.HasPrefix(promise, opts.Feature+":")
}

// spanHash hashes the lines a symbol covers in a file of the worktree.
func (r *runner) spanHash(sym schema.GraphSymbol) (string, error) {
	src, err := os.ReadFile(filepath.Join(r.wt.Dir, sym.File))
	if err != nil {
		return "", err
	}
	lines := strings.SplitAfter(string(src), "\n")
	from, to := sym.Line-1, sym.EndLine
	if to < sym.Line {
		to = sym.Line
	}
	if from < 0 || from >= len(lines) {
		return "", fmt.Errorf("%s: line %d is outside the file", sym.File, sym.Line)
	}
	if to > len(lines) {
		to = len(lines)
	}
	return sha([]byte(strings.Join(lines[from:to], ""))), nil
}

// runTarget makes one run. Only a failure that leaves the worktree in an
// unknown state is an error; everything else is an outcome.
func (r *runner) runTarget(ctx context.Context, t target) (Run, error) {
	start := time.Now()
	run, err := r.runTargetOutcome(ctx, t)
	run.DurationMS = time.Since(start).Milliseconds()
	return run, err
}

// @enforces breaktest:INV-4
func (r *runner) runTargetOutcome(ctx context.Context, t target) (Run, error) {
	enf := t.enforcers[0]
	run := Run{Promise: t.promise, Enforcer: enf.FQN, EnforcerFile: enf.File,
		Method: MethodEarlyReturn, Verifiers: []string{}, Outcome: Unknown}
	for _, v := range t.tests {
		run.Verifiers = append(run.Verifiers, v.FQN)
	}
	sort.Strings(run.Verifiers)

	// Hashes first: they key the cache and tell a stale break from a fresh one.
	var parts []string
	for _, e := range t.enforcers {
		h, err := r.spanHash(e)
		if err != nil {
			run.Reason = "enforcer source unreadable: " + err.Error()
			return run, nil
		}
		parts = append(parts, h)
	}
	run.EnforcerHash = parts[0]
	if len(parts) > 1 {
		run.EnforcerHash = sha([]byte(strings.Join(parts, "\n")))
	}
	var vparts []string
	for _, v := range t.tests {
		h, err := r.spanHash(v)
		if err != nil {
			run.Reason = "verifier source unreadable: " + err.Error()
			return run, nil
		}
		vparts = append(vparts, h)
	}
	sort.Strings(vparts)
	run.VerifierHash = sha([]byte(strings.Join(vparts, "\n")))

	var patch []byte
	if t.script != "" {
		var err error
		if patch, err = os.ReadFile(t.script); err != nil {
			run.Reason = "script unreadable: " + err.Error()
			return run, nil
		}
		run.Method = MethodScripted
		run.Script = path.Join("lattice", "breaks", strings.ReplaceAll(t.promise, ":", "/")+".patch")
		run.ScriptHash = sha(patch)
	}

	if hit, ok := r.prior[run.cacheKey()]; ok && len(t.tests) > 0 {
		run.Baseline, run.Outcome, run.Reason, run.Cached = hit.Baseline, hit.Outcome, hit.Reason, true
		return run, nil
	}
	if len(t.tests) == 0 {
		run.Reason = "no verifier is anchored to this promise"
		return run, nil
	}

	// The break. A generated one needs a generator for the enforcer's kind.
	var brk Break
	var src []byte
	if t.script == "" {
		var err error
		if src, err = os.ReadFile(filepath.Join(r.wt.Dir, enf.File)); err != nil {
			run.Reason = "enforcer source unreadable: " + err.Error()
			return run, nil
		}
		brk, err = Generate(enf.Language, enf.File, src, Symbol{Name: enf.Name, Kind: enf.Kind, Line: enf.Line})
		if err != nil {
			run.Reason = err.Error()
			return run, nil
		}
	}

	groups, why := r.groups(t.verifiers)
	if why != "" && len(groups) == 0 {
		run.Baseline, run.Reason = BaselineError, why
		return run, nil
	}

	// Baseline: the verifiers on the unbroken code.
	base := r.baseline(ctx, groups)
	run.Baseline = base.status
	if base.status != BaselinePass {
		run.Reason = base.reason
		return run, nil
	}

	// Break, run, restore.
	res, err := r.broken(ctx, t, brk, src, patch, groups)
	if err != nil {
		return run, err
	}
	switch res.status {
	case statusFail:
		run.Outcome, run.Reason = Killed, res.reason
	case statusOK:
		run.Outcome = Survived
		if why != "" {
			run.Outcome, run.Reason = Unknown, why
		}
	case statusBuild:
		run.Reason = "break " + res.reason
	case statusTimeout:
		run.Reason = "verifiers " + res.reason + " after the break"
	default:
		run.Reason = res.reason
	}
	return run, nil
}

// broken applies the break, runs the verifiers and undoes the break, always.
func (r *runner) broken(ctx context.Context, t target, brk Break, src, patch []byte, groups []group) (execResult, error) {
	enf := t.enforcers[0]
	if t.script != "" {
		if err := r.wt.ApplyPatch(ctx, t.script, false); err != nil {
			return execResult{status: statusError, reason: "script does not apply: " + firstLine(err.Error())}, nil
		}
		res := r.runGroups(ctx, groups)
		if err := r.wt.ApplyPatch(context.Background(), t.script, true); err != nil {
			if rerr := r.wt.Reset(context.Background()); rerr != nil {
				return res, fmt.Errorf("%w: %v", errWorktree, rerr)
			}
		}
		return res, nil
	}
	file := filepath.Join(r.wt.Dir, enf.File)
	if err := os.WriteFile(file, brk.Apply(src), 0o644); err != nil {
		return execResult{}, err
	}
	res := r.runGroups(ctx, groups)
	if err := os.WriteFile(file, src, 0o644); err != nil {
		return res, fmt.Errorf("%w: %v", errWorktree, err)
	}
	return res, nil
}

// group is the verifiers of one language, run by one command.
type group struct {
	language string
	tests    []results.Selector
}

// groups splits the verifiers by language and drops the languages with no
// runner. The reason is set when any language was dropped.
func (r *runner) groups(sels []results.Selector) ([]group, string) {
	by := map[string]*group{}
	var order []string
	var missing []string
	for _, s := range sels {
		g := by[s.Language]
		if g == nil {
			g = &group{language: s.Language}
			by[s.Language] = g
			order = append(order, s.Language)
		}
		g.tests = append(g.tests, s)
	}
	sort.Strings(order)
	var out []group
	for _, lang := range order {
		if lang == "go" || r.opts.Config.Break.Command(lang) != "" {
			out = append(out, *by[lang])
		} else {
			missing = append(missing, lang)
		}
	}
	why := ""
	if len(missing) > 0 {
		why = "no runner configured for " + strings.Join(missing, ", ") + " (set break.commands in config.yaml)"
	}
	return out, why
}

// baseline runs the verifiers unbroken once per distinct set.
func (r *runner) baseline(ctx context.Context, groups []group) baseline {
	var key []string
	for _, g := range groups {
		for _, t := range g.tests {
			key = append(key, t.Test)
		}
	}
	sort.Strings(key)
	k := strings.Join(key, "\x00")
	if b, ok := r.baselines[k]; ok {
		return b
	}
	res := r.runGroups(ctx, groups)
	b := baseline{status: BaselinePass}
	switch res.status {
	case statusFail:
		b = baseline{BaselineFail, "baseline fails: " + res.reason}
	case statusOK:
	case statusBuild:
		b = baseline{BaselineError, "baseline " + res.reason}
	default:
		b = baseline{BaselineError, "baseline " + res.reason}
	}
	// A timeout or an interrupt says nothing about the next run.
	if res.status != statusTimeout && res.status != statusError {
		r.baselines[k] = b
	}
	return b
}

// runGroups runs every group; the first group that does not pass decides.
func (r *runner) runGroups(ctx context.Context, groups []group) execResult {
	last := execResult{status: statusOK}
	for _, g := range groups {
		res := r.runGroup(ctx, g)
		if res.status != statusOK {
			return res
		}
		last = res
	}
	return last
}

func (r *runner) runGroup(ctx context.Context, g group) execResult {
	if g.language == "go" && r.opts.Config.Break.Command("go") == "" {
		return r.runGo(ctx, g)
	}
	tmpl := r.opts.Config.Break.Command(g.language)
	files, filter := templateArgs(g)
	cmd := strings.NewReplacer("{files}", files, "{filter}", filter).Replace(tmpl)
	return command(ctx, "", r.wt.Dir, r.opts.Timeout, "sh", "-c", cmd)
}

// templateArgs builds the {files} and {filter} placeholders of a command
// template, already shell-quoted.
func templateArgs(g group) (files, filter string) {
	seen := map[string]bool{}
	var fs, names []string
	for _, t := range g.tests {
		if !seen[t.File] {
			seen[t.File] = true
			fs = append(fs, shellQuote(t.File))
		}
		switch g.language {
		case "python":
			names = append(names, leafName(t))
		case "php":
			names = append(names, regexp.QuoteMeta(leafName(t)))
		default:
			title := t.Name
			if title == "" {
				title = leafName(t)
			}
			names = append(names, regexp.QuoteMeta(title))
		}
	}
	sort.Strings(fs)
	sort.Strings(names)
	switch g.language {
	case "python":
		return strings.Join(fs, " "), shellQuote(strings.Join(names, " or "))
	case "php":
		return strings.Join(fs, " "), shellQuote("/::(" + strings.Join(names, "|") + ")$/")
	}
	return strings.Join(fs, " "), shellQuote("(" + strings.Join(names, "|") + ")$")
}

func leafName(s results.Selector) string {
	if i := strings.LastIndex(s.Test, "."); i >= 0 {
		return s.Test[i+1:]
	}
	return s.Test
}

// runGo runs the Go verifiers with `go test`, one invocation per module,
// naming the top-level tests so only the promise's verifiers run.
func (r *runner) runGo(ctx context.Context, g group) execResult {
	type mod struct {
		dir   string
		pkgs  map[string]bool
		tests map[string]bool
	}
	mods := map[string]*mod{}
	var order []string
	for _, t := range g.tests {
		dir := r.moduleDir(path.Dir(t.File))
		m := mods[dir]
		if m == nil {
			m = &mod{dir: dir, pkgs: map[string]bool{}, tests: map[string]bool{}}
			mods[dir] = m
			order = append(order, dir)
		}
		rel, _ := filepath.Rel(filepath.FromSlash(dir), filepath.FromSlash(path.Dir(t.File)))
		pkg := "./" + filepath.ToSlash(rel)
		if rel == "." {
			pkg = "."
		}
		m.pkgs[pkg] = true
		if pat := t.Filters["go"]; len(pat) == 2 {
			top := strings.SplitN(pat[1], "/", 2)[0]
			m.tests[strings.TrimSuffix(strings.TrimPrefix(top, "^"), "$")] = true
		}
	}
	sort.Strings(order)
	for _, d := range order {
		m := mods[d]
		pkgs, tests := keys(m.pkgs), keys(m.tests)
		args := []string{"test", "-vet=off", "-count=1", "-v", "-run", "^(" + strings.Join(tests, "|") + ")$"}
		args = append(args, pkgs...)
		res := command(ctx, "go", filepath.Join(r.wt.Dir, filepath.FromSlash(d)), r.opts.Timeout, "go", args...)
		if res.status == statusOK {
			if name, ok := goVerifiersPassed(res.output, tests); !ok {
				return execResult{status: statusError, reason: "verifier " + name + " did not run and pass", output: res.output}
			}
		}
		if res.status != statusOK {
			return res
		}
	}
	return execResult{status: statusOK}
}

// moduleDir is the nearest directory at or above dir (relative to the code
// root) that holds a go.mod, "." when none does.
func (r *runner) moduleDir(dir string) string {
	for d := dir; ; d = path.Dir(d) {
		if _, err := os.Stat(filepath.Join(r.wt.Dir, filepath.FromSlash(d), "go.mod")); err == nil {
			return d
		}
		if d == "." || d == "/" || d == "" {
			return "."
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
