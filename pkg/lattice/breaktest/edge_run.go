package breaktest

// @module-feature breaktest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// edgeVerifiers drops the verifiers whose command applies SQL object files
// ({patched_sql}): they check the database, and a literal in client code is
// not something they can notice, so they are neither run nor counted as not
// run. What is left is every verifier that could.
func (r *runner) edgeVerifiers(t target) target {
	groups, _ := r.groups(t.verifiers)
	drop := map[string]bool{}
	for _, g := range groups {
		if g.usesSQLFiles() {
			for _, s := range g.tests {
				drop[s.Test] = true
			}
		}
	}
	if len(drop) == 0 {
		return t
	}
	var sels []results.Selector
	for _, s := range t.verifiers {
		if !drop[s.Test] {
			sels = append(sels, s)
		}
	}
	var tests []schema.GraphSymbol
	for _, v := range t.tests {
		if !drop[v.FQN] {
			tests = append(tests, v)
		}
	}
	t.verifiers, t.tests = sels, tests
	return t
}

// runEdge breaks one hop: the string literal that names its target, at the
// hop's out end, gets EdgeSuffix appended so the key no longer matches, and
// the promise's verifiers run. A hop the run cannot alter exactly (skipped
// by the caller, a literal that is not where the graph says, a request an
// altered path would still reach) reads unknown with the reason, and nothing
// runs. A survived run carries the EDGE_BREAK_SURVIVED finding.
// @enforces breaktest:INV-19
func (r *runner) runEdge(ctx context.Context, t target) (Run, error) {
	h := t.hop
	run := Run{Promise: t.promise, Enforcer: h.From, Method: MethodEdge, Hop: &Hop{From: h.From, To: h.To, Kind: h.Kind},
		Verifiers: []string{}, Outcome: Unknown}
	if h.Skip != "" {
		run.Reason = "skipped: " + h.Skip
		return run, nil
	}
	if len(h.Sites) > 0 {
		run.EnforcerFile = h.Sites[0].File
	}
	t = r.edgeVerifiers(t)
	for _, v := range t.tests {
		run.Verifiers = append(run.Verifiers, v.FQN)
	}
	sort.Strings(run.Verifiers)

	// The break: one insertion per literal, read back from the worktree.
	var plan breakPlan
	files := map[string][]byte{}
	var names []string
	for _, st := range h.Sites {
		src, ok := files[st.File]
		if !ok {
			var err error
			if src, err = os.ReadFile(filepath.Join(r.wt.Dir, filepath.FromSlash(st.File))); err != nil {
				run.Reason = "skipped: out-end source unreadable: " + err.Error()
				return run, nil
			}
			files[st.File] = src
			names = append(names, st.File)
		}
		brk, err := alterSite(h.Kind, st, src, h.Served)
		if err != nil {
			run.Reason = "skipped: " + err.Error()
			return run, nil
		}
		plan.edits = append(plan.edits, fileEdit{file: st.File, src: src, brk: brk, line: st.Line})
	}
	sort.Strings(names)
	var parts []string
	for _, n := range names {
		parts = append(parts, n+"\x00"+sha(files[n]))
	}
	run.EnforcerHash = sha([]byte(strings.Join(parts, "\n")))

	groups, noRunner := r.groups(t.verifiers)
	run.ConfigHash = r.configHash(groups, noRunner)
	run.InputsHash = r.inputsHash(ctx, t, groups)
	var vparts []string
	for _, v := range t.tests {
		vh, err := r.spanHash(v)
		if err != nil {
			run.Reason = "verifier source unreadable: " + err.Error()
			return run, nil
		}
		vparts = append(vparts, vh)
	}
	sort.Strings(vparts)
	run.VerifierHash = sha([]byte(strings.Join(vparts, "\n")))

	if hit, ok := r.prior[run.cacheKey()]; ok && len(t.tests) > 0 && !r.baselineKnownFailing(groups) {
		run.Baseline, run.Outcome, run.Reason, run.Cached = hit.Baseline, hit.Outcome, hit.Reason, true
		run.RanVerifiers, run.NotRunVerifiers, run.FailedTests = hit.RanVerifiers, hit.NotRunVerifiers, hit.FailedTests
		run.Finding = edgeFinding(run)
		return run, nil
	}
	if len(t.tests) == 0 {
		run.Reason = "no verifier of this promise can notice a hop breaking (none runs outside the database)"
		return run, nil
	}
	if err := r.measure(ctx, &run, t, schema.GraphSymbol{Language: "typescript"}, plan, nil, groups, noRunner); err != nil {
		return run, err
	}
	run.Finding = edgeFinding(run)
	return run, nil
}

// edgeFinding is EDGE_BREAK_SURVIVED for a survived run.
func edgeFinding(run Run) string {
	if run.Outcome == Survived {
		return CodeEdgeBreakSurvived
	}
	return ""
}

// EdgeSummary describes an edge run for a person: the hop and what the
// verifiers did.
func EdgeSummary(r Run) string {
	if r.Hop == nil {
		return ""
	}
	return fmt.Sprintf("%s -%s-> %s", r.Hop.From, r.Hop.Kind, r.Hop.To)
}
