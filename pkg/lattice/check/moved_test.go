package check

import (
	"sort"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/impact"
)

// --- an enforcer moved within one change ---

// Literal codes, so these tests read the contract as a client does.
const (
	movedCode   = "ENFORCER_MOVED"
	moveGapCode = "MOVE_NOT_BREAK_TESTED"
)

// enforcers sets the head row's per-enforcer break outcomes and folds them
// into the row's break_tested signal the way the audit does: a survival wins
// over a kill, a kill over the rest.
func enforcers(outcomes map[string]string) rowOpt {
	return func(r *audit.Row) {
		var names []string
		for n := range outcomes {
			names = append(names, n)
		}
		sort.Strings(names)
		r.Enforcers, r.Evidence.Enforcers = nil, nil
		var killed, survived []string
		for _, n := range names {
			r.Enforcers = append(r.Enforcers, audit.EnforcerOutcome{Symbol: n, Outcome: outcomes[n]})
			r.Evidence.Enforcers = append(r.Evidence.Enforcers, audit.EnforcerEvidence{FQN: n})
			switch outcomes[n] {
			case audit.OutcomeKilled:
				killed = append(killed, n)
			case audit.OutcomeSurvived:
				survived = append(survived, n)
			}
		}
		switch {
		case len(survived) > 0:
			r.Signals.BreakTested = audit.Signal{Value: "survived", Honesty: audit.Fact, Items: survived}
			r.Class = audit.ClassFake
		case len(killed) > 0:
			r.Signals.BreakTested = audit.Signal{Value: "killed", Honesty: audit.Fact, Items: killed}
			r.Class = audit.ClassBreakTested
		default:
			r.Signals.BreakTested = audit.Signal{Value: "unknown", Honesty: audit.Fact, Detail: "no break run for this promise"}
			r.Class = audit.ClassExercised
		}
	}
}

// moveOf is a change that deletes the helper bookshop.guard, an enforcer of
// promise, keeps its caller bookshop.Check (also an enforcer) and adds the
// given enforcers of the promise in a new file.
func moveOf(promise string, added ...string) impact.Report {
	im := impact.Report{
		RemovedEnforcement: []impact.RemovedEnforcement{{Symbol: "bookshop.guard", Invariant: promise, File: "bookshop/guard.go",
			Line: 9, Reason: impact.ReasonSymbolDeleted}},
		ChangedSymbols: []impact.ChangedSymbol{
			{Symbol: "bookshop.guard", Kind: "function", File: "bookshop/guard.go", OldLines: "9-12", Change: impact.ChangeRemoved,
				Feature: "bookshop", Enforces: []string{promise}},
			{Symbol: "bookshop.Check", Kind: "function", File: "bookshop/check.go", Lines: "5-8", OldLines: "5-8", Change: impact.ChangeModified,
				Feature: "bookshop", Enforces: []string{promise}},
		},
	}
	for _, a := range added {
		im.AddedEnforcement = append(im.AddedEnforcement, impact.EnforcerChange{Symbol: a, File: "shared/rules.go",
			Invariant: promise, Change: impact.ChangeEnforcementAdded})
		im.ChangedSymbols = append(im.ChangedSymbols, impact.ChangedSymbol{Symbol: a, Kind: "function", File: "shared/rules.go",
			Lines: "3-6", Change: impact.ChangeAdded, Feature: "bookshop", Enforces: []string{promise}})
	}
	return im
}

func moveGap(r Report, promise string) (Gap, bool) {
	g, ok := gapOf(r, promise, SigBreakTested)
	if !ok || g.Code != moveGapCode {
		return Gap{}, false
	}
	return g, true
}

func moveNotesOf(r Report, promise string) []Note {
	var out []Note
	for _, p := range r.Promises {
		if p.Promise != promise {
			continue
		}
		for _, n := range p.Notes {
			if n.Code == movedCode {
				out = append(out, n)
			}
		}
	}
	return out
}

// Only kept enforcers were broken and killed: that says nothing about the
// added one, so the gap stays and blocks the merge.
// @verifies check:INV-14
func TestMoveKeptEnforcerKillDoesNotClear(t *testing.T) {
	p := "bookshop:INV-1"
	base := auditOf("base", good(p, 2))
	head := auditOf("head", good(p, 2, enforcers(map[string]string{
		"bookshop.Check": audit.OutcomeKilled, "shared.Guard": audit.OutcomeNotRun})))
	rep := Compute(scenario(&base, head, moveOf(p, "shared.Guard")))
	g, ok := moveGap(rep, p)
	if !ok || g.State == Pass || g.Delta != DeltaNew || !g.Blocks || !strings.Contains(g.Text, "shared.Guard") {
		t.Fatalf("move gap = %+v in %+v", g, rep.Gaps)
	}
	if rep.Verdict.State != Unproven || rep.MergeAllowed || rep.Merge.Code != CodeNewGap || len(rep.Weakenings) != 0 {
		t.Fatalf("verdict %s merge %v rule %+v weak %+v", rep.Verdict.State, rep.MergeAllowed, rep.Merge, rep.Weakenings)
	}
	row := rep.Promises[0]
	if row.Signals.BreakTested.State == Pass || row.Signals.BreakTested.Code != moveGapCode {
		t.Errorf("the row must not read the kept enforcer's kill as the move's: %+v", row.Signals.BreakTested)
	}
	if !contains(row.Touched, "enforcer_moved") || contains(row.Touched, "enforcer_removed") {
		t.Errorf("touched = %v", row.Touched)
	}
	ns := moveNotesOf(rep, p)
	if len(ns) != 1 || ns[0].Text != "bookshop.guard no longer enforces bookshop:INV-1; shared.Guard now does" ||
		ns[0].Next != "run lattice break at head so a break of shared.Guard is killed, then re-run the check; no human step is needed" {
		t.Errorf("notes = %+v", ns)
	}
	if row.NextAction != ns[0].Next {
		t.Errorf("next action = %q", row.NextAction)
	}

	// An unconfirmed promise: the move's next still leads, since its gap is
	// the one that blocks.
	head = auditOf("head", good(p, 2, unconfirmed(), enforcers(map[string]string{
		"bookshop.Check": audit.OutcomeKilled, "shared.Guard": audit.OutcomeNotRun})))
	unconfirmedBase := auditOf("base", good(p, 2, unconfirmed()))
	rep = Compute(scenario(&unconfirmedBase, head, moveOf(p, "shared.Guard")))
	if got := rep.Promises[0].NextAction; got != ns[0].Next {
		t.Errorf("next action of an unconfirmed promise = %q", got)
	}
}

// Each added enforcer needs its own kill: with two added and one killed, the
// gap stays and names the other.
// @verifies check:INV-14
func TestMoveEachAddedEnforcerNeedsAKill(t *testing.T) {
	p := "bookshop:INV-1"
	base := auditOf("base", good(p, 2))
	head := auditOf("head", good(p, 2, enforcers(map[string]string{
		"bookshop.Check": audit.OutcomeKilled, "shared.Guard": audit.OutcomeKilled, "shared.Bound": audit.OutcomeNotRun})))
	rep := Compute(scenario(&base, head, moveOf(p, "shared.Guard", "shared.Bound")))
	g, ok := moveGap(rep, p)
	if !ok || !g.Blocks || !strings.Contains(g.Text, "shared.Bound") || strings.Contains(g.Text, "shared.Guard at head") {
		t.Fatalf("move gap = %+v in %+v", g, rep.Gaps)
	}
	if rep.MergeAllowed {
		t.Fatalf("one added enforcer is not break-tested: merge %v", rep.MergeAllowed)
	}
	if ns := moveNotesOf(rep, p); len(ns) != 1 || !strings.Contains(ns[0].Text, "shared.Bound, shared.Guard now do") {
		t.Errorf("notes = %+v", ns)
	}

	// Both killed: the move is break-tested and nothing else is missing.
	head = auditOf("head", good(p, 2, enforcers(map[string]string{
		"bookshop.Check": audit.OutcomeKilled, "shared.Guard": audit.OutcomeKilled, "shared.Bound": audit.OutcomeKilled})))
	rep = Compute(scenario(&base, head, moveOf(p, "shared.Guard", "shared.Bound")))
	if _, open := moveGap(rep, p); open || rep.Verdict.State != Assured || !rep.MergeAllowed {
		t.Fatalf("both killed: verdict %s merge %v gaps %+v", rep.Verdict.State, rep.MergeAllowed, rep.Gaps)
	}
	if ns := moveNotesOf(rep, p); len(ns) != 1 || ns[0].Next != "" {
		t.Errorf("a closed move keeps its note, with nothing to do: %+v", ns)
	}
}

// The added enforcer's break survives: the gap stays and reads fail, never a
// pass.
// @verifies check:INV-14
func TestMoveSurvivedBreakNeverPasses(t *testing.T) {
	p := "bookshop:INV-1"
	for _, baseRow := range []audit.Row{good(p, 2), good(p, 2, noBreak())} {
		base := auditOf("base", baseRow)
		head := auditOf("head", good(p, 2, enforcers(map[string]string{
			"bookshop.Check": audit.OutcomeKilled, "shared.Guard": audit.OutcomeSurvived})))
		rep := Compute(scenario(&base, head, moveOf(p, "shared.Guard")))
		g, ok := moveGap(rep, p)
		if !ok || g.State != Fail || !g.Blocks || !strings.Contains(g.Text, "passed with shared.Guard broken") {
			t.Fatalf("base %s: move gap = %+v in %+v", baseRow.Signals.BreakTested.Value, g, rep.Gaps)
		}
		if rep.MergeAllowed || rep.Verdict.State == Assured {
			t.Fatalf("base %s: verdict %s merge %v", baseRow.Signals.BreakTested.Value, rep.Verdict.State, rep.MergeAllowed)
		}
	}
}

// A removed enforcer with only a kept enforcer left is a removal, not a move:
// BROKEN. An enforcer added for another promise does not make it a move.
// @verifies check:INV-1
// @verifies check:INV-14
func TestRemovedWithOnlyKeptEnforcerIsBroken(t *testing.T) {
	p, q := "bookshop:INV-1", "bookshop:INV-2"
	base := auditOf("base", good(p, 2), good(q, 2))
	head := auditOf("head", good(p, 2), good(q, 2))
	for name, im := range map[string]impact.Report{"kept only": moveOf(p), "added for another promise": moveOf(p)} {
		if name == "added for another promise" {
			im.AddedEnforcement = []impact.EnforcerChange{{Symbol: "shared.Guard", File: "shared/rules.go", Invariant: q,
				Change: impact.ChangeEnforcementAdded}}
		}
		rep := Compute(scenario(&base, head, im))
		if rep.Verdict.State != Broken || rep.MergeAllowed || !hasWeakening(rep, CodeEnforcerRemoved) {
			t.Fatalf("%s: verdict %s merge %v weak %+v", name, rep.Verdict.State, rep.MergeAllowed, rep.Weakenings)
		}
		if _, ok := moveGap(rep, p); ok || len(moveNotesOf(rep, p)) != 0 {
			t.Errorf("%s: a removal is not a move: %+v", name, rep.Promises)
		}
	}
}

// A tier-3 promise's enforcement never moves on the kernel's word: the
// removal reads BROKEN even with an enforcer added, at a tier 3 at base or at
// head.
// @verifies check:INV-1
// @verifies check:INV-14
func TestTier3MoveIsBroken(t *testing.T) {
	p := "bookshop:INV-1"
	for _, tiers := range [][2]int{{3, 3}, {3, 2}, {2, 3}} {
		base := auditOf("base", good(p, tiers[0]))
		head := auditOf("head", good(p, tiers[1], enforcers(map[string]string{
			"bookshop.Check": audit.OutcomeKilled, "shared.Guard": audit.OutcomeKilled})))
		rep := Compute(scenario(&base, head, moveOf(p, "shared.Guard")))
		if rep.Verdict.State != Broken || rep.MergeAllowed || !hasWeakening(rep, CodeEnforcerRemoved) {
			t.Fatalf("tiers %v: verdict %s merge %v weak %+v", tiers, rep.Verdict.State, rep.MergeAllowed, rep.Weakenings)
		}
	}
}

// The move's gap is new at every tier and whatever the base read: tier 1,
// whose own needs omit break_tested, and a tier-2 base that already had a
// break_tested gap.
// @verifies check:INV-14
func TestMoveGapIsNewWhateverTheBase(t *testing.T) {
	p := "bookshop:INV-1"

	base := auditOf("base", good(p, 1))
	head := auditOf("head", good(p, 1, enforcers(map[string]string{
		"bookshop.Check": audit.OutcomeKilled, "shared.Guard": audit.OutcomeNotRun})))
	rep := Compute(scenario(&base, head, moveOf(p, "shared.Guard")))
	if g, ok := moveGap(rep, p); !ok || g.Delta != DeltaNew || !g.Blocks || rep.MergeAllowed {
		t.Fatalf("tier 1: move gap = %+v merge %v gaps %+v", g, rep.MergeAllowed, rep.Gaps)
	}
	if !contains(rep.Promises[0].Needs, SigBreakTested) {
		t.Errorf("tier 1 with an open move needs break_tested: %v", rep.Promises[0].Needs)
	}

	base = auditOf("base", good(p, 2, noBreak()))
	head = auditOf("head", good(p, 2, enforcers(map[string]string{"bookshop.Check": audit.OutcomeNotRun, "shared.Guard": audit.OutcomeNotRun})))
	rep = Compute(scenario(&base, head, moveOf(p, "shared.Guard")))
	if g, ok := moveGap(rep, p); !ok || g.Delta != DeltaNew || !g.Blocks || rep.MergeAllowed || rep.Merge.Code != CodeNewGap {
		t.Fatalf("tier 2, base gap: move gap = %+v merge %v rule %+v", g, rep.MergeAllowed, rep.Merge)
	}
	if n := countPairs(rep, p, SigBreakTested); n != 1 {
		t.Errorf("the pair is listed %d times, want once: %+v", n, rep.Gaps)
	}

	// No base audit at all: the move's gap is still new, not unknown.
	rep = Compute(scenario(nil, head, moveOf(p, "shared.Guard")))
	if g, ok := moveGap(rep, p); !ok || g.Delta != DeltaNew || !g.Blocks || rep.Verdict.State != Unproven {
		t.Fatalf("no base audit: move gap = %+v verdict %s", g, rep.Verdict.State)
	}
}

func countPairs(r Report, promise, signal string) int {
	n := 0
	for _, g := range r.Gaps {
		if g.Promise == promise && g.Signal == signal {
			n++
		}
	}
	return n
}

// A move edits no statement, tier or mapping, so a confirmed tier-2 promise
// stays confirmed: not voided, not BROKEN, and ASSURED once the added
// enforcer's break is killed.
// @verifies check:INV-14
func TestConfirmedPromiseMoveStaysConfirmed(t *testing.T) {
	p := "bookshop:INV-1"
	base := auditOf("base", good(p, 2))
	head := auditOf("head", good(p, 2, enforcers(map[string]string{
		"bookshop.Check": audit.OutcomeKilled, "shared.Guard": audit.OutcomeKilled})))
	rep := Compute(scenario(&base, head, moveOf(p, "shared.Guard")))
	if hasWeakening(rep, CodeConfirmationVoid) || rep.Verdict.State != Assured || !rep.MergeAllowed {
		t.Fatalf("verdict %s merge %v weak %+v gaps %+v", rep.Verdict.State, rep.MergeAllowed, rep.Weakenings, rep.Gaps)
	}
	if s := rep.Promises[0].Signals.Confirmed; s.State != Pass {
		t.Errorf("confirmed = %+v", s)
	}
}

// Nothing but a killed break of the added enforcer in counted head evidence
// clears the move: not a waiver, not break results set aside as stale or as
// not from CI, and not the absence of a break run (INV-10 is for untouched
// promises).
// @verifies check:INV-14
func TestMoveGapIgnoresWaiversAndUncountedBreaks(t *testing.T) {
	p := "bookshop:INV-1"
	base := auditOf("base", good(p, 2))
	killedHead := auditOf("head", good(p, 2, enforcers(map[string]string{
		"bookshop.Check": audit.OutcomeKilled, "shared.Guard": audit.OutcomeKilled})))
	notRunHead := auditOf("head", good(p, 2, enforcers(map[string]string{
		"bookshop.Check": audit.OutcomeNotRun, "shared.Guard": audit.OutcomeNotRun})))

	cases := map[string]func() Input{
		"waiver": func() Input {
			in := scenario(&base, notRunHead, moveOf(p, "shared.Guard"))
			in.Waivers = []Waiver{{Subject: p + "/break_tested", Kind: "no_tool", Reason: "no break tool"}}
			return in
		},
		"stale breaks": func() Input {
			in := scenario(&base, killedHead, moveOf(p, "shared.Guard"))
			in.Evidence.Breaks = EvidenceItem{Present: true, Commit: "old", Counted: false, Code: CodeEvidenceStale}
			return in
		},
		"breaks not from CI": func() Input {
			in := scenario(&base, killedHead, moveOf(p, "shared.Guard"))
			in.CI = true
			in.Evidence.Breaks = EvidenceItem{Present: true, Commit: "head", CIRun: "local", Counted: false, Code: CodeEvidenceNotCI}
			return in
		},
		"no break run": func() Input {
			in := scenario(&base, notRunHead, moveOf(p, "shared.Guard"))
			in.Evidence.Breaks = EvidenceItem{Counted: true}
			return in
		},
	}
	for name, mk := range cases {
		rep := Compute(mk())
		g, ok := moveGap(rep, p)
		if !ok || !g.Blocks || g.Delta != DeltaNew || rep.MergeAllowed || rep.Verdict.State != Unproven {
			t.Errorf("%s: move gap = %+v verdict %s merge %v gaps %+v", name, g, rep.Verdict.State, rep.MergeAllowed, rep.Gaps)
		}
		if name == "waiver" && !g.Waived {
			t.Errorf("waiver: the gap shows the waiver: %+v", g)
		}
	}
}

// A verifier removed in the same change still falls, move or not.
// @verifies check:INV-1
func TestMoveWithVerifierRemovedIsBroken(t *testing.T) {
	p := "bookshop:INV-1"
	base := auditOf("base", good(p, 2))
	head := auditOf("head", good(p, 2, noVerifier(), enforcers(map[string]string{
		"bookshop.Check": audit.OutcomeKilled, "shared.Guard": audit.OutcomeKilled})))
	rep := Compute(scenario(&base, head, moveOf(p, "shared.Guard")))
	if rep.Verdict.State != Broken || rep.MergeAllowed || !hasWeakening(rep, CodeVerifierRemoved) {
		t.Fatalf("verdict %s merge %v weak %+v", rep.Verdict.State, rep.MergeAllowed, rep.Weakenings)
	}
}
