package breaktest

// @module-feature breaktest

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/coverage"
	"github.com/salahmyn/lattice/pkg/lattice/lines"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// coverageToken stands for the coverage report in a runner command. It
// names a path, unique per invocation, inside the break's temporary
// directory; the runner writes its report there. A tool that can only write
// into a directory may be given the path as a directory: the one coverage
// report inside it is read (see readCoverage).
const coverageToken = "{coverage}"

// Why a group has no line data.
const (
	whyNoCoverage = "the runner has no {coverage}"
	whyNoReport   = "no coverage report was written to {coverage}"
	whyPgTAP      = "pgTAP reports calls per function, not lines"
	whyNoFiles    = "the coverage report lists no file of the workspace"
)

// usesCoverage reports whether the group's command names {coverage}.
func (g group) usesCoverage() bool {
	if g.kind == kindShell {
		return strings.Contains(g.tmpl, coverageToken)
	}
	for _, a := range g.runner.Command {
		if strings.Contains(a, coverageToken) {
			return true
		}
	}
	return false
}

// coversLines reports whether a --lines baseline of the group runs with
// coverage on: Go's built-in command always does, a runner or a template
// when its command has {coverage}. Such an invocation is keyed apart from
// the plain one.
func (g group) coversLines() bool {
	return !g.isSQL() && (g.kind == kindGo || g.usesCoverage())
}

// isSQL reports whether every verifier of the group is a SQL test: pgTAP
// counts calls per function and has no line data.
func (g group) isSQL() bool {
	if len(g.tests) == 0 {
		return false
	}
	for _, t := range g.tests {
		if t.Language != "postgres" && t.Language != "sql" {
			return false
		}
	}
	return true
}

// keyOf is the invocation key of a group for this run: with --lines, a group
// that runs with coverage is keyed apart from the same command without it,
// so a plain baseline is never taken as having line data.
func (r *runner) keyOf(g group) string {
	return invocationKeyFor(g, r.opts.Lines && g.coversLines())
}

// newCoveragePath returns a fresh path in the break's temporary directory.
func (r *runner) newCoveragePath() string {
	r.covSeq++
	return filepath.Join(r.wt.tmp, fmt.Sprintf("coverage-%d", r.covSeq))
}

// readCoverage reads what an invocation wrote at path, a file or a
// directory holding exactly one coverage report, and keeps the workspace
// files. root is the directory, relative to the code root, the report's
// relative paths start from; resolve may add a mapping of its own first
// (Go's import paths). It never invents lines: a missing, unreadable or
// unrecognised report is an Invocation with the reason.
func (r *runner) readCoverage(p, root string, resolve func(string) string) lines.Invocation {
	data, why := readReport(p)
	if why != "" {
		return lines.Invocation{Unknown: why}
	}
	parsed, err := coverage.Parse(data, "")
	if err != nil {
		return lines.Invocation{Unknown: "the coverage report could not be parsed: " + firstLine(err.Error())}
	}
	return r.keepWorkspace(parsed.Report, root, resolve)
}

// keepWorkspace maps a parsed report onto the graph's files and keeps the
// workspace's own.
func (r *runner) keepWorkspace(rep coverage.Report, root string, resolve func(string) string) lines.Invocation {
	mapped := rep.Remap(func(p string) string {
		if resolve != nil {
			if q := resolve(p); q != "" {
				return q
			}
		}
		return r.paths.Resolve(p, root)
	})
	out := lines.Invocation{Files: map[string]lines.FileLines{}}
	for f, c := range mapped.Files {
		if !r.src[f] {
			continue
		}
		var inst, hit []int
		for l, n := range c.Lines {
			inst = append(inst, l)
			if n > 0 {
				hit = append(hit, l)
			}
		}
		out.Files[f] = lines.FileLines{Instrumented: lines.Compact(inst), Hit: lines.Compact(hit), Decl: c.DeclEvidence}
	}
	if len(out.Files) == 0 {
		out.Files = nil
		out.Unknown = whyNoFiles
	}
	return out
}

// readReport reads the report at p. A directory holds exactly one coverage
// report among its files (a vitest report directory also has HTML in a
// subdirectory, which is not read). why is "" when data is the report.
func readReport(p string) (data []byte, why string) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, whyNoReport
	}
	if !st.IsDir() {
		b, err := os.ReadFile(p)
		if err != nil || len(strings.TrimSpace(string(b))) == 0 {
			return nil, whyNoReport
		}
		return b, ""
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, whyNoReport
	}
	var found [][]byte
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(p, e.Name()))
		if err != nil || coverage.Detect(b) == "" {
			continue
		}
		found = append(found, b)
		names = append(names, e.Name())
	}
	switch len(found) {
	case 0:
		return nil, whyNoReport
	case 1:
		return found[0], ""
	}
	sort.Strings(names)
	return nil, "several coverage reports were written to {coverage} (" + strings.Join(names, ", ") +
		"); write one report format there"
}

// goModulePath reads the module path of the go.mod in dir, "" when none.
func goModulePath(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(ln), "module"); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}
	return ""
}

// goResolver maps a Go profile's import path (<module path>/<dir>/<file>) to
// the workspace file of a module at moduleDir, relative to the code root.
func goResolver(modPath, moduleDir string) func(string) string {
	if modPath == "" {
		return nil
	}
	return func(p string) string {
		rest, ok := strings.CutPrefix(p, modPath+"/")
		if !ok {
			return ""
		}
		if moduleDir == "." || moduleDir == "" {
			return rest
		}
		return path.Join(moduleDir, rest)
	}
}

// mergeInvocation joins the line data of the modules one Go group ran.
func mergeInvocation(parts []lines.Invocation) lines.Invocation {
	out := lines.Invocation{}
	for _, p := range parts {
		if p.Unknown != "" && out.Unknown == "" {
			out.Unknown = p.Unknown
		}
		for f, fl := range p.Files {
			if out.Files == nil {
				out.Files = map[string]lines.FileLines{}
			}
			if have, ok := out.Files[f]; ok {
				fl = unionLines(have, fl)
			}
			out.Files[f] = fl
		}
	}
	if len(out.Files) > 0 {
		out.Unknown = ""
	}
	return out
}

func unionLines(a, b lines.FileLines) lines.FileLines {
	return lines.FileLines{
		Instrumented: lines.Compact(append(lines.Expand(a.Instrumented), lines.Expand(b.Instrumented)...)),
		Hit:          lines.Compact(append(lines.Expand(a.Hit), lines.Expand(b.Hit)...)),
		Decl:         a.Decl || b.Decl,
	}
}

// loadLines reads the line data kept for a tree and stamp; a missing, stale
// or unreadable file is an empty one.
func loadLines(latticeDir, tree, stamp string) *lines.File {
	f := lines.New(tree, stamp)
	old, ok, _ := lines.Load(latticeDir)
	if !ok || old.Tree != tree || old.Stamp != stamp {
		return f
	}
	if old.Invocations != nil {
		f.Invocations = old.Invocations
	}
	if old.Promises != nil {
		f.Promises = old.Promises
	}
	return f
}

// recordLines notes, for a promise, what each group's invocation told about
// the lines its verifiers ran. It is called with the invocations of the
// baseline just made (or taken from the store), and never invents data: a
// group whose invocation did not pass, or has no report, says why.
// @enforces audit:INV-10
func (r *runner) recordLines(promise string, groups []group) {
	if r.vlines == nil {
		return
	}
	var out []lines.Group
	for _, g := range groups {
		out = append(out, r.groupLines(g))
	}
	r.vlines.Promises[promise] = out
}

func (r *runner) groupLines(g group) lines.Group {
	lg := lines.Group{Name: g.name, Shared: true}
	if g.isSQL() {
		lg.Unknown = whyPgTAP
		return lg
	}
	key := r.keyOf(g)
	inv, ok := r.last[key]
	if !ok {
		lg.Unknown = "the baseline did not run"
		return lg
	}
	if inv.res.status != statusOK {
		lg.Unknown = "the baseline did not pass"
		return lg
	}
	lg.Key = key
	if !g.coversLines() {
		lg.Unknown = whyNoCoverage
	} else if _, ok := r.vlines.Invocations[key]; !ok {
		lg.Unknown = "no line data was kept for the invocation"
	}
	lg.Tests, lg.Shared, lg.Others, lg.OthersTotal = sharedOf(g, inv)
	return lg
}

// sharedOf says what ran in an invocation beside the group's verifiers:
// the verifiers that ran, whether any other test did, and up to five of
// them. Without a JUnit report nothing says which tests ran, so the
// invocation counts as shared. A Go group runs `go test -run` with the
// verifiers' top-level names, so only a verifier that is a subtest (whose
// siblings run too) makes it shared.
func sharedOf(g group, inv invocation) (ran []string, shared bool, others []string, total int) {
	ran = append([]string(nil), inv.result(g).ran...)
	sort.Strings(ran)
	if g.kind == kindGo {
		for _, t := range g.tests {
			if pat := t.Filters["go"]; len(pat) == 2 && strings.Contains(pat[1], "/") {
				return ran, true, nil, 0
			}
		}
		return ran, false, nil, 0
	}
	if !inv.junit {
		return ran, true, nil, 0
	}
	set, err := results.ParseJUnit(inv.report)
	if err != nil {
		return ran, true, nil, 0
	}
	for _, id := range unclaimedCases(set, g.tests) {
		total++
		if len(others) < 5 {
			others = append(others, id)
		}
	}
	return ran, total > 0, others, total
}

// unclaimedCases lists the cases of a JUnit report that ran (were not
// skipped) and that no verifier of the group names.
func unclaimedCases(set results.Set, verifiers []results.Selector) []string {
	byID := map[string]map[string]results.Outcome{}
	for k, id := range set.Cases {
		if byID[id] == nil {
			byID[id] = map[string]results.Outcome{}
		}
		byID[id][k] = set.Results[k]
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []string
	for _, id := range ids {
		keys := byID[id]
		skipped := true
		for _, o := range keys {
			if o != results.Skip {
				skipped = false
			}
		}
		if skipped {
			continue
		}
		one := results.Set{Results: keys, Cases: map[string]string{}}
		for k := range keys {
			one.Cases[k] = id
		}
		claimed := false
		for _, v := range verifiers {
			if _, ok := one.Lookup(v.Test); ok {
				claimed = true
				break
			}
		}
		if !claimed {
			out = append(out, strings.TrimPrefix(id, "::"))
		}
	}
	return out
}

// editLines are the lines of the enforcer's file a generated break edits,
// [start, end], or nil when they are not known. An early return at the top
// of a body skips the rest of it, so it spans from its own line to the end
// of the symbol; a SQL break spans the lines of its insertions. Only the
// first file of the plan is described, the one the run reports.
func editLines(plan breakPlan, enf schema.GraphSymbol) []int {
	if len(plan.edits) == 0 {
		return nil
	}
	file := plan.edits[0].file
	lo, hi := 0, 0
	for _, e := range plan.edits {
		if e.file != file {
			continue
		}
		for _, b := range append([]Break{{Offset: e.brk.Offset, Text: e.brk.Text}}, e.brk.More...) {
			if b.Offset < 0 || b.Offset > len(e.src) {
				return nil
			}
			ln := 1 + strings.Count(string(e.src[:b.Offset]), "\n")
			if lo == 0 || ln < lo {
				lo = ln
			}
			if ln > hi {
				hi = ln
			}
		}
	}
	if lo == 0 {
		return nil
	}
	if enf.Language != "postgres" && enf.Language != "sql" && enf.File == file && enf.EndLine > hi {
		hi = enf.EndLine
	}
	return []int{lo, hi}
}

// keptLines returns the line data kept for an invocation key. A group that
// runs without coverage has none to keep, and its baseline stands.
func (r *runner) keptLines(key string, withLines bool) (*lines.Invocation, bool) {
	if !withLines {
		return nil, true
	}
	if r.vlines == nil {
		return nil, false
	}
	iv, ok := r.vlines.Invocations[key]
	if !ok {
		return nil, false
	}
	return &iv, true
}

// linesOnly makes the baseline of a promise whose break outcome was taken
// from an earlier run, so the lines its verifiers run are known without
// breaking anything again. Groups that need the SQL object files are not
// run: a SQL group has no line data.
func (r *runner) linesOnly(ctx context.Context, t target, groups []group) error {
	keep, _ := splitSwapGroups(groups)
	if len(keep) > 0 && r.ensureSetup(ctx) == nil {
		r.baseline(ctx, keep, sqlFiles{})
	}
	r.recordLines(t.promise, groups)
	return ctx.Err()
}

// remember keeps the invocation a baseline of this run used for a key, run or
// taken from the store, for recordLines.
func (r *runner) remember(key string, inv invocation) {
	if r.last == nil {
		r.last = map[string]invocation{}
	}
	r.last[key] = inv
}
