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

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// runner carries the state of one `lattice break` invocation.
type runner struct {
	opts  Options
	repo  Repo
	wt    *Worktree
	prior map[[6]string]Run
	// shared are this run's baseline invocations by invocationKey, shared by
	// the promises whose groups render the same command.
	shared map[string]invocation
	// store keeps passing baselines across runs at the same tree; nil on a
	// dirty tree.
	store *baselineFile
	// tree is HEAD's tree, "" on a dirty tree. It names what a cached
	// outcome was measured on.
	tree string
	// listing is HEAD's files with their blob shas, read once for the runs
	// whose runners declare inputs.
	listing    []treeFile
	listingErr error
	listed     bool
	counts     BaselineCounts
	junitSeq   int
	sqlSeq     int

	setupDone bool
	setupErr  error
	// setupRuns counts the times break.setup ran: the first, and each one a
	// failed restore or a suspicious baseline asked for.
	setupRuns int
	// swapInvokes counts the commands that ran with the SQL object files. A
	// swap since the last setup (swapDirty) is how a database may have been
	// left in a state setup would not have made.
	swapInvokes int
	swapDirty   bool
}

// Execute breaks each promise's enforcer on a temporary worktree and reports
// whether its verifiers noticed. The report is returned, not written.
// Without AllowDirty a working tree with uncommitted changes is refused.
// With it, the report records dirty: true; Lattice's own state (the
// workspace's .ledger/ and .cache/) never makes the tree dirty.
// @enforces breaktest:INV-2, breaktest:INV-9
func Execute(ctx context.Context, kg schema.KnowledgeGraph, opts Options) (Report, error) {
	rep := Report{Schema: Schema, GeneratedAt: time.Now().UTC().Format(time.RFC3339), Runs: []Run{}}
	repo, err := OpenRepo(ctx, opts.CodeRoot, opts.LatticeDir)
	if err != nil {
		return rep, err
	}
	if repo.Dirty && !opts.AllowDirty {
		return rep, fmt.Errorf("%w: %s", ErrDirty, nameFew(repo.DirtyFiles, 5))
	}
	rep.Commit, rep.Dirty = repo.Head, repo.Dirty
	if opts.Timeout <= 0 {
		opts.Timeout = 120 * time.Second
	}

	targets := plan(kg, opts)
	prior := Read(opts.LatticeDir)
	r := &runner{opts: opts, repo: repo, prior: map[[6]string]Run{}, shared: map[string]invocation{}}
	for _, p := range prior.Runs {
		if p.cacheable() {
			r.prior[p.cacheKey()] = p
		}
	}
	// Baselines are kept by HEAD's tree, which a dirty tree is not: it
	// neither reuses nor replaces them.
	if !repo.Dirty {
		if tree, err := git(ctx, repo.Top, "rev-parse", "HEAD^{tree}"); err == nil {
			r.tree = tree
			r.store = loadBaselines(opts.LatticeDir, tree, baselineStamp(tree, repo.Prefix, opts.Config.Break))
			rep.baselines = r.store
		}
	}
	if len(targets) > 0 {
		release, err := acquireLock(opts.LatticeDir, opts.warn())
		if err != nil {
			return rep, err
		}
		defer release()
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
	rep.Baselines = r.counts
	rep.SetupRuns = r.setupRuns

	// A filtered run keeps the other promises' runs from the same commit, so
	// one promise can be re-broken without losing the rest of the file.
	if (opts.Promise != "" || opts.Feature != "") && prior.Commit == rep.Commit && prior.Dirty == rep.Dirty {
		have := map[string]bool{}
		for _, run := range rep.Runs {
			have[run.Promise] = true
		}
		for _, p := range prior.Runs {
			if !have[p.Promise] && !opts.Selects(p.Promise) {
				rep.Runs = append(rep.Runs, p)
			}
		}
	}
	return rep, nil
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

// configHash is what the runner configuration adds to the cache key: the
// resolved command of each group, the setup commands, the node_modules link
// and the generator version. Changing any of them invalidates the outcomes.
func (r *runner) configHash(groups []group, noRunner []results.Selector) string {
	type grp struct {
		Name, Kind, Template string
		Runner               any `json:",omitempty"`
	}
	desc := struct {
		Version  string
		Setup    []string
		Restore  []string `json:",omitempty"`
		Link     bool
		Groups   []grp
		NoRunner []string
	}{Version: GeneratorVersion, Setup: r.opts.Config.Break.Setup, Restore: r.opts.Config.Break.Restore, Link: r.opts.Config.Break.LinksNodeModules()}
	for _, g := range groups {
		d := grp{Name: g.name, Kind: g.kind.String(), Template: g.tmpl}
		if g.kind == kindRunner {
			d.Runner = g.runner
		}
		desc.Groups = append(desc.Groups, d)
	}
	for _, s := range noRunner {
		desc.NoRunner = append(desc.NoRunner, s.Test)
	}
	sort.Strings(desc.NoRunner)
	b, _ := json.Marshal(desc)
	return sha(b)
}

// planFor generates the break of an enforcer from the worktree's sources. The
// file actually edited is plan.edits[0].file: a SQL break goes where the
// object's effective definition is set, which may not be the file of its
// CREATE, and that is the file a run reports as its enforcer's.
func (r *runner) planFor(enf schema.GraphSymbol) (breakPlan, error) {
	src, err := os.ReadFile(filepath.Join(r.wt.Dir, enf.File))
	if err != nil {
		return breakPlan{}, fmt.Errorf("enforcer source unreadable: %w", err)
	}
	return planBreak(enf.Language, enf.File, src, symbolOf(enf), func(f string) ([]byte, error) {
		return os.ReadFile(filepath.Join(r.wt.Dir, f))
	})
}

// @enforces breaktest:INV-4, breaktest:INV-16
func (r *runner) runTargetOutcome(ctx context.Context, t target) (Run, error) {
	enf := t.enforcers[0]
	run := Run{Promise: t.promise, Enforcer: enf.FQN, EnforcerFile: enf.File,
		Method: MethodEarlyReturn, Verifiers: []string{}, Outcome: Unknown}
	for _, v := range t.tests {
		run.Verifiers = append(run.Verifiers, v.FQN)
	}
	sort.Strings(run.Verifiers)

	groups, noRunner := r.groups(t.verifiers)
	run.ConfigHash = r.configHash(groups, noRunner)
	run.InputsHash = r.inputsHash(ctx, t, groups)

	// Hashes first: they key the cache and tell a stale break from a fresh one.
	var parts []string
	for _, e := range t.enforcers {
		h, err := r.spanHash(e)
		if err != nil {
			run.Reason = "enforcer source unreadable: " + err.Error()
			return run, nil
		}
		parts = append(parts, h)
		// A SQL object's effective definition may be in a later statement,
		// so those statements are part of what the break edits.
		for _, a := range e.Altered {
			h, err := r.spanHash(schema.GraphSymbol{File: a.File, Line: a.Line, EndLine: a.EndLine})
			if err != nil {
				run.Reason = "enforcer source unreadable: " + err.Error()
				return run, nil
			}
			parts = append(parts, h)
		}
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

	// A cached outcome is not taken over a baseline known to fail at this
	// tree: it is run, which shows what the tree does now.
	if hit, ok := r.prior[run.cacheKey()]; ok && len(t.tests) > 0 && !r.baselineKnownFailing(groups) {
		if t.script == "" {
			if plan, err := r.planFor(enf); err == nil {
				run.EnforcerFile = plan.edits[0].file
			}
		}
		run.Baseline, run.Outcome, run.Reason, run.Cached = hit.Baseline, hit.Outcome, hit.Reason, true
		run.RanVerifiers, run.NotRunVerifiers, run.FailedTests = hit.RanVerifiers, hit.NotRunVerifiers, hit.FailedTests
		return run, nil
	}
	if len(t.tests) == 0 {
		run.Reason = "no verifier is anchored to this promise"
		return run, nil
	}

	// The break. A generated one needs a generator for the enforcer's kind.
	var plan breakPlan
	if t.script == "" {
		var err error
		if plan, err = r.planFor(enf); err != nil {
			run.Reason = err.Error()
			return run, nil
		}
		run.EnforcerFile = plan.edits[0].file
	}

	notRunWhy := map[string]string{}
	for _, s := range noRunner {
		notRunWhy[s.Test] = "no runner matches " + s.File
	}
	// A command that names the SQL object files runs only where there are
	// some: a break with no isolated statement reads unknown, and nothing
	// runs, baseline included.
	var sw sqlFiles
	if anyUsesSQLFiles(groups) {
		var why string
		var err error
		if sw, why, err = r.swapFiles(enf.Language, t.script != "", plan, symbolOf(enf)); err != nil {
			run.Reason = "SQL object files: " + err.Error()
			return run, nil
		}
		if !sw.ok() {
			run.Reason = sqlUnavailable + why
			return run, nil
		}
	}
	if len(groups) == 0 {
		run.Baseline = BaselineError
		run.NotRunVerifiers = run.Verifiers
		run.Reason = noRunnerReason(noRunner)
		return run, nil
	}
	if err := r.ensureSetup(ctx); err != nil {
		run.Baseline, run.Reason = BaselineError, err.Error()
		return run, nil
	}

	// Baseline: the verifiers on the unbroken code. After a SQL swap since
	// the last setup, a failing baseline may be a database the restore left
	// wrong, so it is retried once on a fresh setup.
	swapBefore, swapped := r.swapDirty, r.swapInvokes
	base := r.baseline(ctx, groups, sw.asBaseline())
	if base.status == BaselineFail && swapBefore {
		base = r.healBaseline(ctx, groups, sw)
	}
	r.restoreSQL(ctx, sw.asBaseline(), r.swapInvokes > swapped)
	run.Baseline = base.status
	if base.status != BaselinePass {
		run.Reason = base.reason
		for _, s := range noRunner {
			run.NotRunVerifiers = append(run.NotRunVerifiers, s.Test)
		}
		sort.Strings(run.NotRunVerifiers)
		return run, nil
	}
	run.RanVerifiers = base.ran
	ran := map[string]bool{}
	for _, v := range base.ran {
		ran[v] = true
	}
	for _, v := range run.Verifiers {
		if !ran[v] {
			run.NotRunVerifiers = append(run.NotRunVerifiers, v)
			if notRunWhy[v] == "" {
				notRunWhy[v] = "no test ran for it"
			}
		}
	}

	// Break, run, restore.
	swapped = r.swapInvokes
	res, err := r.broken(ctx, t, plan, patch, groups, sw)
	if err != nil {
		return run, err
	}
	// The worktree is back to the unbroken code, so a setup the restore asks
	// for builds the original database. The outcome below is not changed.
	r.restoreSQL(ctx, sw, r.swapInvokes > swapped)
	note := ""
	if base.unchecked || res.unchecked {
		note = " (executed test count not checked: the runner has no {junit})"
	}
	switch res.status {
	case statusFail:
		run.Outcome, run.Reason, run.FailedTests = Killed, res.reason+note, res.failed
	case statusOK:
		run.Outcome, run.Reason = survivedOrPartial(&run, res.ran, notRunWhy)
		run.Reason += note
	case statusBuild:
		run.Reason = "break " + res.reason
	case statusTimeout:
		run.Reason = "verifiers " + res.reason + " after the break"
	default:
		run.Reason = res.reason
	}
	return run, nil
}

// survivedOrPartial reads a break that every group passed. It is survived
// only when each verifier ran both at baseline and after the break; when any
// did not (no runner, no test selected, or absent from the JUnit report), the
// break was never shown to the ones that could have noticed it, so the run is
// unknown, with a partial reason. after are the verifiers that ran after the
// break; the ones that ran only at baseline join the run's not-run list.
// @enforces breaktest:INV-4, breaktest:INV-7
func survivedOrPartial(run *Run, after []string, why map[string]string) (outcome, reason string) {
	ranAfter := map[string]bool{}
	for _, v := range after {
		ranAfter[v] = true
	}
	var both []string
	for _, v := range run.RanVerifiers {
		if ranAfter[v] {
			both = append(both, v)
			continue
		}
		run.NotRunVerifiers = append(run.NotRunVerifiers, v)
		why[v] = "did not run after the break"
	}
	run.RanVerifiers = both
	if len(run.NotRunVerifiers) == 0 {
		return Survived, ""
	}
	sort.Strings(run.NotRunVerifiers)
	return Unknown, fmt.Sprintf("%sthe %d verifier(s) that ran passed after the break; %s",
		PartialPrefix, len(both), notRunReason(run.NotRunVerifiers, why))
}

// PartialPrefix starts the reason of a partial run: the verifiers that ran
// passed after the break, but some did not run, so it reads unknown.
const PartialPrefix = "partial: "

// IsPartial reports whether a reason is a partial run's. A summary groups
// such runs under one reason.
func IsPartial(reason string) bool {
	return strings.HasPrefix(reason, PartialPrefix)
}

// noRunnerReason says which languages or files have no runner.
func noRunnerReason(sels []results.Selector) string {
	seen := map[string]bool{}
	var langs []string
	for _, s := range sels {
		if !seen[s.Language] {
			seen[s.Language] = true
			langs = append(langs, s.Language)
		}
	}
	sort.Strings(langs)
	return noRunnerPrefix + strings.Join(langs, ", ") + noRunnerSuffix
}

const (
	noRunnerPrefix = "no runner configured for "
	noRunnerSuffix = " (set break.runners or break.commands in config.yaml)"
)

// NoRunnerLanguages returns the languages a "no runner configured" reason
// names, and whether the reason is one. A summary groups such runs by the
// runner each one lacks, so one missing runner is reported once.
func NoRunnerLanguages(reason string) ([]string, bool) {
	rest, ok := strings.CutPrefix(reason, noRunnerPrefix)
	if !ok {
		return nil, false
	}
	rest, ok = strings.CutSuffix(rest, noRunnerSuffix)
	if !ok || rest == "" {
		return nil, false
	}
	return strings.Split(rest, ", "), true
}

// notRunReason names up to three verifiers that did not run, and why.
func notRunReason(notRun []string, why map[string]string) string {
	if len(notRun) == 0 {
		return ""
	}
	var parts []string
	for i, v := range notRun {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("and %d more", len(notRun)-3))
			break
		}
		parts = append(parts, v+" ("+why[v]+")")
	}
	return fmt.Sprintf("%d verifier(s) did not run: %s", len(notRun), strings.Join(parts, "; "))
}

// broken applies the break, runs the verifiers and undoes the break, always.
func (r *runner) broken(ctx context.Context, t target, plan breakPlan, patch []byte, groups []group, sw sqlFiles) (execResult, error) {
	if t.script != "" {
		if err := r.wt.ApplyPatch(ctx, t.script, false); err != nil {
			return execResult{status: statusError, reason: "script does not apply: " + firstLine(err.Error())}, nil
		}
		res := r.runGroups(ctx, groups, sw)
		if err := r.wt.ApplyPatch(context.Background(), t.script, true); err != nil {
			if rerr := r.wt.Reset(context.Background()); rerr != nil {
				return res, fmt.Errorf("%w: %v", errWorktree, rerr)
			}
			// Reset cleans untracked files, the node_modules links among them.
			r.setupDone = false
			if serr := r.ensureSetup(context.Background()); serr != nil {
				return res, fmt.Errorf("%w: %v", errWorktree, serr)
			}
		}
		return res, nil
	}
	// Write every edit, run, and put every file back, whatever happened.
	var wrote []fileEdit
	var werr error
	for _, e := range byFile(plan.edits) {
		if werr = os.WriteFile(filepath.Join(r.wt.Dir, e.file), e.brk.Apply(e.src), 0o644); werr != nil {
			break
		}
		wrote = append(wrote, e)
	}
	var res execResult
	if werr == nil {
		res = r.runGroups(ctx, groups, sw)
	}
	var rerr error
	for _, e := range wrote {
		if err := os.WriteFile(filepath.Join(r.wt.Dir, e.file), e.src, 0o644); err != nil && rerr == nil {
			rerr = err
		}
	}
	if rerr != nil {
		return res, fmt.Errorf("%w: %v", errWorktree, rerr)
	}
	return res, werr
}

// groupKind says how a group of verifiers is run.
type groupKind int

const (
	kindGo     groupKind = iota // `go test -run`, built in
	kindRunner                  // a break.runners entry, run without a shell
	kindShell                   // a break.commands.<lang> template, run by sh
)

func (k groupKind) String() string { return [...]string{"go", "runner", "shell"}[k] }

// group is the verifiers one runner runs in one invocation.
type group struct {
	name     string // "go", the runner's name, or the language of a template
	kind     groupKind
	language string // the first verifier's, which picks the filter syntax
	runner   config.Runner
	tmpl     string
	tests    []results.Selector
}

// groups splits a promise's verifiers by runner: the first break.runners
// entry that matches the file, else the language's break.commands template,
// else Go's built-in. Verifiers no runner covers are returned apart.
// @enforces breaktest:INV-6
func (r *runner) groups(sels []results.Selector) ([]group, []results.Selector) {
	return groupVerifiers(r.opts.Config.Break, sels)
}

// groupVerifiers is the grouping of groups, for a break configuration.
func groupVerifiers(cfg config.Break, sels []results.Selector) ([]group, []results.Selector) {
	by := map[string]*group{}
	var order []string
	var none []results.Selector
	for _, s := range sels {
		var key string
		g := group{language: s.Language}
		if rn, ok := cfg.RunnerFor(s.File); ok {
			key, g.name, g.kind, g.runner = "runner:"+rn.Name+"\x00"+strings.Join(rn.Command, " "), rn.Name, kindRunner, rn
		} else if s.Language == "go" && cfg.Command("go") == "" {
			key, g.name, g.kind = "go", "go", kindGo
		} else if c := cfg.Command(s.Language); c != "" {
			key, g.name, g.kind, g.tmpl = "shell:"+s.Language, s.Language, kindShell, c
		} else {
			none = append(none, s)
			continue
		}
		if by[key] == nil {
			by[key] = &g
			order = append(order, key)
		}
		by[key].tests = append(by[key].tests, s)
	}
	sort.Strings(order)
	out := make([]group, 0, len(order))
	for _, k := range order {
		out = append(out, *by[k])
	}
	return out, none
}

// runGroups runs every group and combines the results. The verifiers fail
// together: one group failing is enough, and every group must pass for the
// run to pass.
func (r *runner) runGroups(ctx context.Context, groups []group, sql sqlFiles) execResult {
	var all []execResult
	for _, g := range groups {
		res := r.runGroup(ctx, g, sql)
		all = append(all, res)
		if res.status == statusTimeout || ctx.Err() != nil {
			break
		}
	}
	return combine(all)
}

// combine folds the results of several groups into one. A timeout or an
// error outranks a failure, which outranks a build failure, so a failing
// test is never hidden by another group's missing build. A run that executed
// no test at all is an error, never a pass.
// @enforces breaktest:INV-7
func combine(all []execResult) execResult {
	out := execResult{status: statusOK}
	rank := map[status]int{statusOK: 0, statusBuild: 1, statusFail: 2, statusError: 3, statusTimeout: 4}
	ran := map[string]bool{}
	executed := 0
	for _, res := range all {
		if rank[res.status] > rank[out.status] {
			out.status, out.reason, out.output = res.status, res.reason, res.output
		}
		for _, v := range res.ran {
			if !ran[v] {
				ran[v] = true
				out.ran = append(out.ran, v)
			}
		}
		out.failed = append(out.failed, res.failed...)
		out.unchecked = out.unchecked || res.unchecked
		executed += res.executed
	}
	sort.Strings(out.ran)
	sort.Strings(out.failed)
	if out.status == statusFail && len(out.failed) > 0 {
		out.reason = "failed: " + nameList(out.failed)
	}
	if out.status == statusOK && executed == 0 {
		out.status, out.reason = statusError, "the filter matched no tests: none of the verifiers ran"
	}
	out.executed = executed
	return out
}

// nameList joins up to three names and counts the rest.
func nameList(names []string) string {
	if len(names) <= 3 {
		return strings.Join(names, "; ")
	}
	return strings.Join(names[:3], "; ") + fmt.Sprintf("; and %d more", len(names)-3)
}

func (r *runner) runGroup(ctx context.Context, g group, sql sqlFiles) execResult {
	return r.invoke(ctx, g, sql).result(g)
}

// invoke runs a group's command once and keeps its result and its JUnit
// report, before they are accounted for any promise's verifiers. A command
// that names the SQL object files is never run without them.
// @enforces breaktest:INV-15
func (r *runner) invoke(ctx context.Context, g group, sql sqlFiles) invocation {
	if g.kind == kindGo {
		return invocation{res: r.runGo(ctx, g)}
	}
	if g.usesSQLFiles() && !sql.ok() {
		// Never run a command with its placeholders unfilled.
		return invocation{res: execResult{status: statusError, reason: sqlUnavailable}}
	}
	junit := ""
	if g.usesJUnit() {
		r.junitSeq++
		junit = filepath.Join(r.wt.tmp, fmt.Sprintf("junit-%d.xml", r.junitSeq))
	}
	if g.usesSQLFiles() {
		r.swapInvokes++
		r.swapDirty = true
	}
	dir, argv := render(g, r.wt.Dir, junit, sql)
	inv := invocation{res: command(ctx, "", dir, r.opts.Timeout, argv[0], argv[1:]...), junit: junit != ""}
	if inv.junit {
		inv.report, inv.reportErr = os.ReadFile(junit)
	}
	return inv
}

// render builds the directory and the argument list a runner or template
// group runs, in a worktree at root, with the JUnit report at junit and the
// SQL object files of a SQL break in sql.
func render(g group, root, junit string, sql sqlFiles) (dir string, argv []string) {
	files, filter := templateRaw(g)
	if g.kind == kindRunner {
		return filepath.Join(root, filepath.FromSlash(g.runner.Dir)),
			expandArgvSQL(g.runner.Command, relativeTo(root, g.runner.Dir, files), filter, junit, sql)
	}
	var quoted []string
	for _, f := range files {
		quoted = append(quoted, shellQuote(f))
	}
	pairs := []string{"{files}", strings.Join(quoted, " "), "{filter}", shellQuote(filter), "{junit}", shellQuote(junit)}
	if sql.ok() {
		pairs = append(pairs, patchedSQLToken, shellQuote(sql.patched), originalSQLToken, shellQuote(sql.original))
	}
	cmd := strings.NewReplacer(pairs...).Replace(g.tmpl)
	return root, []string{"sh", "-c", cmd}
}

// usesJUnit reports whether the group's command asks for a JUnit report.
func (g group) usesJUnit() bool {
	if g.kind == kindShell {
		return strings.Contains(g.tmpl, "{junit}")
	}
	for _, a := range g.runner.Command {
		if strings.Contains(a, "{junit}") {
			return true
		}
	}
	return false
}

// expandArgv substitutes a runner command's placeholders. {files} as its own
// element becomes one argument per file; elsewhere the files are joined with
// spaces.
func expandArgv(cmd, files []string, filter, junit string) []string {
	return expandArgvSQL(cmd, files, filter, junit, sqlFiles{})
}

// expandArgvSQL is expandArgv with the SQL object files of a SQL break.
// {patched_sql} and {original_sql} are left as written when there are none.
func expandArgvSQL(cmd, files []string, filter, junit string, sql sqlFiles) []string {
	var out []string
	for _, a := range cmd {
		if a == "{files}" {
			out = append(out, files...)
			continue
		}
		pairs := []string{"{files}", strings.Join(files, " "), "{filter}", filter, "{junit}", junit}
		if sql.ok() {
			pairs = append(pairs, patchedSQLToken, sql.patched, originalSQLToken, sql.original)
		}
		a = strings.NewReplacer(pairs...).Replace(a)
		out = append(out, a)
	}
	return out
}

// relativeTo makes the root-relative file paths relative to dir (also
// root-relative), so a runner that sets dir is given paths it can resolve.
// A file outside dir keeps an absolute path in the worktree.
func relativeTo(root, dir string, files []string) []string {
	if dir == "" || dir == "." {
		return files
	}
	out := make([]string, len(files))
	for i, f := range files {
		rel, err := filepath.Rel(filepath.FromSlash(dir), filepath.FromSlash(f))
		if err != nil || strings.HasPrefix(rel, "..") {
			out[i] = filepath.Join(root, filepath.FromSlash(f))
		} else {
			out[i] = filepath.ToSlash(rel)
		}
	}
	return out
}

// account turns a runner's exit status into what ran and what failed. With a
// JUnit report it counts the tests that executed and reads the failing ones;
// without one the verifiers it was given are taken as run, and the result is
// marked unchecked.
func account(g group, res execResult, junit string) execResult {
	if junit == "" {
		return accountReport(g, res, false, nil, nil)
	}
	data, err := os.ReadFile(junit)
	return accountReport(g, res, true, data, err)
}

// accountReport is account for a report already read: data is its content
// and readErr the error reading it, when the command asked for one. It does
// not change what res holds, so one invocation can be accounted for several
// promises' verifiers.
func accountReport(g group, res execResult, junit bool, data []byte, readErr error) execResult {
	res.ran, res.failed = nil, nil
	if !junit {
		res.unchecked = true
		res.executed = len(g.tests)
		if res.status == statusOK || res.status == statusFail {
			for _, t := range g.tests {
				res.ran = append(res.ran, t.Test)
			}
		}
		return res
	}
	err := readErr
	var set results.Set
	if err == nil {
		set, err = results.ParseJUnit(data)
	}
	if err != nil {
		if res.status == statusOK {
			return execResult{status: statusError, output: res.output,
				reason: "the runner wrote no readable JUnit report to {junit}, so the executed tests cannot be counted"}
		}
		res.unchecked = true
		return res
	}
	for _, t := range g.tests {
		if o, ok := set.Lookup(t.Test); ok && o != results.Skip {
			res.ran = append(res.ran, t.Test)
		}
	}
	// What counts is a verifier that ran: some runners report a case for a
	// file whose tests the filter all skipped.
	res.executed = len(res.ran)
	if res.status == statusFail {
		if res.failed = set.FailedCases(); len(res.failed) > 0 {
			res.reason = "failed: " + nameList(res.failed)
		}
	}
	return res
}

// templateRaw builds the files and the filter of a command template, before
// any shell quoting.
func templateRaw(g group) (files []string, filter string) {
	seen := map[string]bool{}
	var names []string
	for _, t := range g.tests {
		if !seen[t.File] {
			seen[t.File] = true
			files = append(files, t.File)
		}
		switch t.Language {
		case "python":
			names = append(names, leafName(t))
		case "php":
			names = append(names, regexp.QuoteMeta(leafName(t)))
		default:
			names = append(names, t.TitlePattern())
		}
	}
	sort.Strings(files)
	sort.Strings(names)
	switch g.language {
	case "python":
		return files, strings.Join(names, " or ")
	case "php":
		return files, "/::(" + strings.Join(names, "|") + ")$/"
	}
	return files, "(" + strings.Join(names, "|") + ")$"
}

// templateArgs builds the {files} and {filter} placeholders of a command
// template, already shell-quoted.
func templateArgs(g group) (files, filter string) {
	fs, f := templateRaw(g)
	for i := range fs {
		fs[i] = shellQuote(fs[i])
	}
	return strings.Join(fs, " "), shellQuote(f)
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
		sels  map[string]string // top-level test name -> verifier
	}
	mods := map[string]*mod{}
	var order []string
	for _, t := range g.tests {
		dir := r.moduleDir(path.Dir(t.File))
		m := mods[dir]
		if m == nil {
			m = &mod{dir: dir, pkgs: map[string]bool{}, tests: map[string]bool{}, sels: map[string]string{}}
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
			top = strings.TrimSuffix(strings.TrimPrefix(top, "^"), "$")
			m.tests[top] = true
			m.sels[top] = t.Test
		}
	}
	sort.Strings(order)
	out := execResult{status: statusOK}
	for _, d := range order {
		m := mods[d]
		pkgs, tests := keys(m.pkgs), keys(m.tests)
		args := []string{"test", "-vet=off", "-count=1", "-v", "-run", "^(" + strings.Join(tests, "|") + ")$"}
		args = append(args, pkgs...)
		res := command(ctx, "go", filepath.Join(r.wt.Dir, filepath.FromSlash(d)), r.opts.Timeout, "go", args...)
		switch res.status {
		case statusOK:
			if name, ok := goVerifiersPassed(res.output, tests); !ok {
				return execResult{status: statusError, reason: "verifier " + name + " did not run and pass", output: res.output}
			}
			for _, top := range tests {
				out.ran = append(out.ran, m.sels[top])
			}
			out.executed += len(tests)
		case statusFail:
			res.failed = goFailedTests(res.output)
			if len(res.failed) > 0 {
				res.reason = "failed: " + nameList(res.failed)
			}
			return res
		default:
			return res
		}
	}
	return out
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

// nameFew joins up to n paths, then says how many more there are.
func nameFew(paths []string, n int) string {
	if len(paths) <= n {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(paths[:n], ", "), len(paths)-n)
}
