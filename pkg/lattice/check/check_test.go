package check

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/confirm"
	"github.com/salahmyn/lattice/pkg/lattice/coverage"
	"github.com/salahmyn/lattice/pkg/lattice/impact"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// --- builders: the bookshop's promises, as audit rows ---

type rowOpt func(*audit.Row)

// good is a promise with every signal present: confirmed by a human, reached
// from an entry point, exercised, and break-tested.
func good(promise string, tier int, opts ...rowOpt) audit.Row {
	feature, inv, _ := strings.Cut(promise, ":")
	r := audit.Row{
		Promise: promise, Feature: feature, Invariant: inv, Statement: "A statement for " + promise,
		Tier: tier, Class: audit.ClassBreakTested,
		Signals: audit.Signals{
			Declared:    audit.Signal{Value: "yes", Honesty: audit.Fact},
			Enforced:    audit.Signal{Value: "yes", Honesty: audit.Fact},
			Verified:    audit.Signal{Value: "yes", Honesty: audit.Fact},
			Confirmed:   audit.Signal{Value: audit.ConfirmedHuman, Honesty: audit.Proxy},
			Live:        audit.Signal{Value: schema.ReachEntry, Honesty: audit.UpperBound},
			Exercised:   audit.Signal{Value: "yes", Honesty: audit.Proxy},
			BreakTested: audit.Signal{Value: "killed", Honesty: audit.Fact, Items: []string{feature + ".Guard"}},
		},
		Evidence: audit.Evidence{Verifiers: []audit.VerifierEvidence{{FQN: feature + ".TestGuard"}}},
	}
	for _, o := range opts {
		o(&r)
	}
	return r
}

func unconfirmed() rowOpt {
	return func(r *audit.Row) {
		r.Signals.Confirmed = audit.Signal{Value: audit.ConfirmedDraft, Honesty: audit.Proxy, Detail: "status draft"}
	}
}
func noBreak() rowOpt {
	return func(r *audit.Row) {
		r.Signals.BreakTested = audit.Signal{Value: "unknown", Honesty: audit.Fact, Detail: "no break run for this promise"}
		r.Class = audit.ClassExercised
	}
}
func noCoverage() rowOpt {
	return func(r *audit.Row) {
		r.Signals.Exercised = audit.Signal{Value: "unknown", Honesty: audit.Proxy, Detail: "no per-test coverage ingested"}
		r.Signals.BreakTested = audit.Signal{Value: "unknown", Honesty: audit.Fact}
		r.Class = audit.ClassDemonstratedOnly
	}
}
func survived() rowOpt {
	return func(r *audit.Row) {
		r.Signals.BreakTested = audit.Signal{Value: "survived", Honesty: audit.Fact, Items: []string{"bookshop.Guard"}}
		r.Class = audit.ClassFake
	}
}
func noVerifier() rowOpt {
	return func(r *audit.Row) { r.Evidence.Verifiers = nil }
}

func passingEvidence() Evidence {
	it := EvidenceItem{Present: true, Commit: "head", CIRun: "4812", Tied: true, Counted: true}
	return Evidence{Results: it, Coverage: it, Breaks: it, Suite: SuiteResult{Present: true, Cases: 40}}
}

func auditOf(commit string, rows ...audit.Row) audit.Report {
	return audit.Report{Schema: audit.Schema, Commit: commit, Rows: rows,
		Inputs: audit.Inputs{Results: audit.ResultsInput{Present: true}}}
}

// guardEdited is a changed enforcer of the given promise.
func guardEdited(promise string) impact.ChangedSymbol {
	feature, _, _ := strings.Cut(promise, ":")
	return impact.ChangedSymbol{Symbol: feature + ".Guard", Kind: "function", File: feature + "/guard.go", Lines: "10-19",
		Change: impact.ChangeModified, Feature: feature, Enforces: []string{promise}}
}

// scenario is one change between a base audit and a head audit.
func scenario(base *audit.Report, head audit.Report, im impact.Report) Input {
	im.Base, im.Head = "base", "head"
	in := Input{Rev: "main", Head: head, Base: base, Impact: im, Evidence: passingEvidence()}
	if base == nil {
		in.BaseNote = "no base audit: snapshot missing"
	}
	return in
}

func hasWeakening(r Report, code string) bool {
	for _, w := range r.Weakenings {
		if w.Code == code {
			return true
		}
	}
	return false
}

func gapOf(r Report, promise, signal string) (Gap, bool) {
	for _, g := range r.Gaps {
		if g.Promise == promise && g.Signal == signal {
			return g, true
		}
	}
	return Gap{}, false
}

// --- the verdicts ---

// All signals present: ASSURED, and it merges.
// @verifies check:INV-2
func TestAllSignalsAssured(t *testing.T) {
	p := "bookshop:INV-1"
	base := auditOf("base", good(p, 1))
	rep := Compute(scenario(&base, auditOf("head", good(p, 2)), impact.Report{ChangedSymbols: []impact.ChangedSymbol{guardEdited(p)}}))
	if rep.Verdict.State != Assured || !rep.MergeAllowed {
		t.Fatalf("verdict %s merge %v: %+v", rep.Verdict.State, rep.MergeAllowed, rep.Gaps)
	}
	if len(rep.Promises) != 1 || rep.Promises[0].Promise != p || rep.Promises[0].NextAction != "nothing to do" {
		t.Fatalf("rows = %+v", rep.Promises)
	}
	if rep.Contract != Contract || rep.Commit != "head" || rep.Base.SHA != "base" {
		t.Fatalf("header = %+v", rep)
	}
}

// An enforcer removed reads BROKEN, never merges, and the row says what to do.
// @verifies check:INV-1
func TestEnforcerRemovedIsBroken(t *testing.T) {
	p := "bookshop:INV-1"
	base := auditOf("base", good(p, 2))
	head := auditOf("head", good(p, 2))
	im := impact.Report{RemovedEnforcement: []impact.RemovedEnforcement{
		{Symbol: "bookshop.Guard", Invariant: p, File: "bookshop/guard.go", Line: 9, Reason: impact.ReasonTagDropped}}}
	rep := Compute(scenario(&base, head, im))
	if rep.Verdict.State != Broken || rep.MergeAllowed || !hasWeakening(rep, CodeEnforcerRemoved) {
		t.Fatalf("verdict %s merge %v weak %+v", rep.Verdict.State, rep.MergeAllowed, rep.Weakenings)
	}
	if rep.Merge.Code != CodeBroken {
		t.Errorf("merge rule = %+v", rep.Merge)
	}
	if len(rep.Promises) != 1 || !strings.Contains(rep.Promises[0].NextAction, "restore the enforcer") {
		t.Errorf("rows = %+v", rep.Promises)
	}
}

// A verifier gutted so a break killed at base survives at head reads BROKEN.
// @verifies check:INV-1
func TestGuttedVerifierIsBroken(t *testing.T) {
	p := "bookshop:INV-1"
	base := auditOf("base", good(p, 2))
	head := auditOf("head", good(p, 2, survived()))
	rep := Compute(scenario(&base, head, impact.Report{}))
	if rep.Verdict.State != Broken || !hasWeakening(rep, CodeBreakSurvived) {
		t.Fatalf("verdict %s weak %+v", rep.Verdict.State, rep.Weakenings)
	}
	if hasWeakening(rep, CodeLevelDropped) {
		t.Errorf("the survived break is reported once, not also as a level drop")
	}
	if len(rep.Promises) != 1 || rep.Promises[0].Signals.NothingFell.State != Fail {
		t.Errorf("an untouched promise that fell is on the page: %+v", rep.Promises)
	}
}

// A verifier removed, a level dropped to a known-bad class, and a red suite.
// @verifies check:INV-1
func TestOtherWeakenings(t *testing.T) {
	p, q := "bookshop:INV-1", "bookshop:INV-2"
	base := auditOf("base", good(p, 1), good(q, 1))
	dropped := good(q, 1)
	dropped.Class, dropped.Reason = audit.ClassUnenforced, "no symbol is anchored"
	head := auditOf("head", good(p, 1, noVerifier()), dropped)
	in := scenario(&base, head, impact.Report{})
	in.Evidence.Suite = SuiteResult{Present: true, Cases: 40, Failed: 2, Red: []string{"a", "b"}}
	rep := Compute(in)
	for _, code := range []string{CodeVerifierRemoved, CodeLevelDropped, CodeSuiteRed} {
		if !hasWeakening(rep, code) {
			t.Errorf("missing weakening %s in %+v", code, rep.Weakenings)
		}
	}
	if rep.Verdict.State != Broken || rep.MergeAllowed {
		t.Fatalf("verdict %s merge %v", rep.Verdict.State, rep.MergeAllowed)
	}
}

// A drop to unknown because evidence is missing is a gap, not a fall.
// @verifies check:INV-2
func TestMissingEvidenceIsNotBroken(t *testing.T) {
	p := "bookshop:INV-1"
	base := auditOf("base", good(p, 1))
	rep := Compute(scenario(&base, auditOf("head", good(p, 1, noCoverage())), impact.Report{ChangedSymbols: []impact.ChangedSymbol{guardEdited(p)}}))
	if rep.Verdict.State != Unproven || len(rep.Weakenings) != 0 {
		t.Fatalf("verdict %s weak %+v", rep.Verdict.State, rep.Weakenings)
	}
	if g, ok := gapOf(rep, p, SigExercised); !ok || g.Delta != DeltaNew || !g.Blocks || g.State != Unknown {
		t.Fatalf("exercised gap = %+v", g)
	}
}

// A confirmed promise whose statement, tier or mapping was edited reads BROKEN
// until a human re-confirms it, and the row says so.
// @verifies check:INV-1
func TestEditingConfirmedPromiseVoidsConfirmation(t *testing.T) {
	p := "bookshop:INV-1"
	base := auditOf("base", good(p, 3))
	head := auditOf("head", good(p, 3))
	cases := map[string]impact.Report{
		"statement": {ManifestDiff: impact.ManifestDiff{Features: impact.KindDiff{Changed: []impact.ManifestChange{{
			ID: "bookshop", Fields: []impact.FieldChange{{Field: "invariants[INV-1].statement", Change: "changed"}}}}}}},
		"tier": {ManifestDiff: impact.ManifestDiff{BRDs: impact.KindDiff{Changed: []impact.ManifestChange{{
			ID: "brd.bookshop", Fields: []impact.FieldChange{{Field: "success_criteria[SC-1].tier", Change: "changed", Before: 2, After: 3}}}}}}},
		"mapping": {ManifestDiff: impact.ManifestDiff{BRDs: impact.KindDiff{Changed: []impact.ManifestChange{{
			ID: "brd.bookshop", Fields: []impact.FieldChange{{Field: "success_criteria[SC-1].maps_to_invariant", Change: "changed",
				Before: "bookshop:INV-1", After: []interface{}{"bookshop:INV-2"}}}}}}}},
	}
	kg := schema.KnowledgeGraph{BRDs: []schema.BRD{{ID: "brd.bookshop", SuccessCriteria: []schema.BRDCriterion{
		{ID: "SC-1", MapsToInvariant: schema.InvariantRefs{p}}}}}}
	for name, im := range cases {
		in := scenario(&base, head, im)
		in.KG = kg
		rep := Compute(in)
		if rep.Verdict.State != Broken || !hasWeakening(rep, CodeConfirmationVoid) {
			t.Errorf("%s: verdict %s weak %+v", name, rep.Verdict.State, rep.Weakenings)
			continue
		}
		if len(rep.Promises) == 0 || !strings.Contains(rep.Promises[0].NextAction, "re-confirms") {
			t.Errorf("%s: next action = %+v", name, rep.Promises)
		}
	}

	// An unconfirmed promise has nothing to void.
	base = auditOf("base", good(p, 1, unconfirmed()))
	rep := Compute(scenario(&base, auditOf("head", good(p, 1, unconfirmed())), cases["statement"]))
	if hasWeakening(rep, CodeConfirmationVoid) {
		t.Errorf("an unconfirmed promise cannot lose a confirmation: %+v", rep.Weakenings)
	}
}

// --- UNPROVEN and the merge rule ---

// A new promise that is unconfirmed: UNPROVEN, and the merge is allowed.
// @verifies check:INV-3
func TestNewUnconfirmedPromiseMayMerge(t *testing.T) {
	p, q := "bookshop:INV-1", "bookshop:INV-2"
	base := auditOf("base", good(p, 1))
	head := auditOf("head", good(p, 1), good(q, 1, unconfirmed()))
	im := impact.Report{ManifestDiff: impact.ManifestDiff{Features: impact.KindDiff{Changed: []impact.ManifestChange{{
		ID: "bookshop", Fields: []impact.FieldChange{{Field: "invariants[INV-2]", Change: "added"}}}}}},
		ChangedSymbols: []impact.ChangedSymbol{guardEdited(q)}}
	rep := Compute(scenario(&base, head, im))
	if rep.Verdict.State != Unproven || !rep.MergeAllowed {
		t.Fatalf("verdict %s merge %v (%+v) gaps %+v", rep.Verdict.State, rep.MergeAllowed, rep.Merge, rep.Gaps)
	}
	g, ok := gapOf(rep, q, SigConfirmed)
	if !ok || !g.NewPromise || g.Blocks {
		t.Fatalf("confirmed gap = %+v", g)
	}
	if !strings.Contains(g.NextAction, "lattice prepare promise confirm") {
		t.Errorf("next = %q", g.NextAction)
	}
}

// A gap pair that is new against base blocks the merge. A gap that was already
// there does not.
// @verifies check:INV-3
func TestNewGapPairBlocksMerge(t *testing.T) {
	p, q := "bookshop:INV-1", "bookshop:INV-2"
	base := auditOf("base", good(p, 1), good(q, 1, noBreak(), unconfirmed()))
	head := auditOf("head", good(p, 1, noCoverage()), good(q, 1, noBreak(), unconfirmed()))
	im := impact.Report{ChangedSymbols: []impact.ChangedSymbol{guardEdited(p), guardEdited(q)}}
	rep := Compute(scenario(&base, head, im))
	if rep.Verdict.State != Unproven || rep.MergeAllowed || rep.Merge.Code != CodeNewGap {
		t.Fatalf("verdict %s merge %v rule %+v", rep.Verdict.State, rep.MergeAllowed, rep.Merge)
	}
	if g, _ := gapOf(rep, p, SigExercised); g.Delta != DeltaNew || !g.Blocks {
		t.Errorf("new gap = %+v", g)
	}
	if g, _ := gapOf(rep, q, SigConfirmed); g.Delta != DeltaSame || g.Blocks {
		t.Errorf("old gap = %+v", g)
	}

	// Only the old gap: UNPROVEN and allowed.
	head = auditOf("head", good(p, 1), good(q, 1, noBreak(), unconfirmed()))
	rep = Compute(scenario(&base, head, im))
	if rep.Verdict.State != Unproven || !rep.MergeAllowed {
		t.Fatalf("old gaps only: verdict %s merge %v rule %+v", rep.Verdict.State, rep.MergeAllowed, rep.Merge)
	}
}

// A promise untouched by the change, whose evidence got weaker, still counts
// as a new gap.
// @verifies check:INV-3
func TestUntouchedPromiseGapGrowthCounts(t *testing.T) {
	p, q := "bookshop:INV-1", "bookshop:INV-2"
	base := auditOf("base", good(p, 1), good(q, 1))
	head := auditOf("head", good(p, 1), good(q, 1, noCoverage()))
	rep := Compute(scenario(&base, head, impact.Report{ChangedSymbols: []impact.ChangedSymbol{guardEdited(p)}}))
	if rep.MergeAllowed || rep.Merge.Code != CodeNewGap {
		t.Fatalf("merge %v rule %+v", rep.MergeAllowed, rep.Merge)
	}
	found := false
	for _, r := range rep.Promises {
		found = found || (r.Promise == q && contains(r.Touched, "gap_grew"))
	}
	if !found {
		t.Errorf("the promise whose gap grew is not on the page: %+v", rep.Promises)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// A waiver added in the same change shows on the gap and never removes it.
// @verifies check:INV-3
func TestWaiverAddedInSameChangeStillCounts(t *testing.T) {
	p := "bookshop:INV-1"
	base := auditOf("base", good(p, 1))
	head := auditOf("head", good(p, 1, noCoverage()))
	in := scenario(&base, head, impact.Report{
		ChangedSymbols: []impact.ChangedSymbol{guardEdited(p)},
		ChangedFiles:   []impact.FileChange{{Status: "A", NewPath: "lattice/waivers.yaml"}},
	})
	in.Waivers = []Waiver{{Subject: p + "/exercised", Kind: "no_tool", Reason: "no coverage tool for this language"}}
	rep := Compute(in)
	g, ok := gapOf(rep, p, SigExercised)
	if !ok || !g.Waived || g.Delta != DeltaNew || !g.Blocks {
		t.Fatalf("waived gap = %+v", g)
	}
	if rep.MergeAllowed || rep.Verdict.State != Unproven {
		t.Fatalf("a waiver must not unblock: verdict %s merge %v", rep.Verdict.State, rep.MergeAllowed)
	}
	if len(rep.Waivers) != 1 || !rep.Waivers[0].InChange {
		t.Errorf("waivers = %+v", rep.Waivers)
	}
}

// Tier 3 needs ASSURED: an UNPROVEN change that touches one may not merge,
// even when no gap is new.
// @verifies check:INV-3
func TestTier3NeedsAssured(t *testing.T) {
	p := "bookshop:INV-1"
	base := auditOf("base", good(p, 3, noBreak()))
	head := auditOf("head", good(p, 3, noBreak()))
	rep := Compute(scenario(&base, head, impact.Report{ChangedSymbols: []impact.ChangedSymbol{guardEdited(p)}}))
	if rep.Verdict.State != Unproven || rep.MergeAllowed || rep.Merge.Code != CodeTier3 {
		t.Fatalf("verdict %s merge %v rule %+v", rep.Verdict.State, rep.MergeAllowed, rep.Merge)
	}
	if g, ok := gapOf(rep, p, SigBreakTested); !ok || g.Delta != DeltaSame {
		t.Errorf("tier 3 needs break_tested: %+v", rep.Gaps)
	}
}

// Tier defaults: tier 1 is satisfied by exercised, tier 2 needs break_tested.
// @verifies check:INV-2
func TestTierDefaults(t *testing.T) {
	p := "bookshop:INV-1"
	for tier, wantGap := range map[int]bool{1: false, 2: true} {
		base := auditOf("base", good(p, tier, noBreak()))
		head := auditOf("head", good(p, tier, noBreak()))
		rep := Compute(scenario(&base, head, impact.Report{ChangedSymbols: []impact.ChangedSymbol{guardEdited(p)}}))
		_, has := gapOf(rep, p, SigBreakTested)
		if has != wantGap {
			t.Errorf("tier %d: break_tested gap = %v, want %v (verdict %s)", tier, has, wantGap, rep.Verdict.State)
		}
		if !wantGap && rep.Verdict.State != Assured {
			t.Errorf("tier %d exercised: verdict %s, gaps %+v", tier, rep.Verdict.State, rep.Gaps)
		}
	}
}

// With no base audit nothing is compared with base: UNPROVEN, never BROKEN,
// and the merge rule cannot be met. A structural fall still reads BROKEN.
// @verifies check:INV-2
func TestNoBaseAudit(t *testing.T) {
	p := "bookshop:INV-1"
	rep := Compute(scenario(nil, auditOf("head", good(p, 1)), impact.Report{ChangedSymbols: []impact.ChangedSymbol{guardEdited(p)}}))
	if rep.Verdict.State != Unproven || rep.MergeAllowed || rep.Merge.Code != CodeNoBaseAudit {
		t.Fatalf("verdict %s merge %v rule %+v", rep.Verdict.State, rep.MergeAllowed, rep.Merge)
	}
	if rep.Evidence.BaseAudit.Present || !strings.Contains(rep.Evidence.BaseAudit.Text, "no base audit") {
		t.Errorf("base audit = %+v", rep.Evidence.BaseAudit)
	}
	if rep.Promises[0].Signals.NothingFell.State != Unknown {
		t.Errorf("nothing_fell = %+v", rep.Promises[0].Signals.NothingFell)
	}

	im := impact.Report{RemovedEnforcement: []impact.RemovedEnforcement{{Symbol: "bookshop.Guard", Invariant: p, Reason: impact.ReasonSymbolDeleted}}}
	if rep := Compute(scenario(nil, auditOf("head", good(p, 1)), im)); rep.Verdict.State != Broken {
		t.Errorf("structural removal with no base audit = %s, want BROKEN", rep.Verdict.State)
	}
}

// --- traced and the blind spot ---

// A touched line under a file tag only is untraced; an enforcer span, a
// checked reference or an entry point that reaches it traces it. Only new
// untraced lines fail the merge rule.
// @verifies check:INV-5
func TestFileTagOnlyIsUntraced(t *testing.T) {
	p := "bookshop:INV-1"
	base := auditOf("base", good(p, 1))
	head := auditOf("head", good(p, 1))
	tagOnly := impact.ChangedSymbol{Symbol: "bookshop.Format", Kind: "function", File: "bookshop/format.go", Lines: "5-16",
		Change: impact.ChangeModified, Feature: "bookshop"}
	reached := impact.ChangedSymbol{Symbol: "bookshop.Handler", Kind: "function", File: "bookshop/handler.go", Lines: "1-4",
		Change: impact.ChangeModified, Feature: "bookshop"}
	referenced := impact.ChangedSymbol{Symbol: "bookshop.Audit", Kind: "function", File: "bookshop/audit.go", Lines: "1-2",
		Change: impact.ChangeModified, Feature: "bookshop"}

	in := scenario(&base, head, impact.Report{ChangedSymbols: []impact.ChangedSymbol{tagOnly, reached, referenced, guardEdited(p)}})
	in.KG = schema.KnowledgeGraph{
		Symbols: []schema.GraphSymbol{
			{FQN: "bookshop.Format", File: "bookshop/format.go", Feature: "bookshop", Reach: schema.ReachUnreached},
			{FQN: "bookshop.Handler", File: "bookshop/handler.go", Feature: "bookshop", Reach: schema.ReachEntry},
		},
		EntryPoints: []schema.EntryPoint{{ID: "POST /books", Flow: []schema.FlowStep{{Feature: "bookshop"}}}},
		BRDs: []schema.BRD{{ID: "brd.bookshop", SuccessCriteria: []schema.BRDCriterion{
			{ID: "SC-9", DirectWire: true, EnforcedBy: []string{"bookshop.Audit"}}}}},
	}
	rep := Compute(in)
	bs := rep.BlindSpot
	if len(bs.Untraced) != 1 || bs.Untraced[0].Symbol != "bookshop.Format" || bs.Untraced[0].Count != 12 {
		t.Fatalf("untraced = %+v", bs.Untraced)
	}
	if !strings.Contains(bs.Untraced[0].Reason, "tag only") || bs.Params["untraced"] != 12 || bs.Params["of"] != 12+4+2+10 {
		t.Errorf("blind spot = %+v", bs)
	}
	if rep.Verdict.State != Unproven {
		t.Fatalf("untraced lines keep the verdict at UNPROVEN, got %s", rep.Verdict.State)
	}
	// The symbol existed before and the change did not drop its enforcement:
	// the untraced count did not rise.
	if !rep.MergeAllowed {
		t.Errorf("legacy untraced lines must not block: %+v", rep.Merge)
	}

	// The same lines in a new symbol do rise.
	tagOnly.Change = impact.ChangeAdded
	in.Impact.ChangedSymbols = []impact.ChangedSymbol{tagOnly, guardEdited(p)}
	rep = Compute(in)
	if rep.MergeAllowed || rep.Merge.Code != CodeUntracedRose {
		t.Fatalf("new untraced lines: merge %v rule %+v", rep.MergeAllowed, rep.Merge)
	}
}

// A change that touches a function an entry point reaches and no promise
// guards is traced but unguarded: UNPROVEN with a reason that names the
// lines, never ASSURED and never NOTHING_TOUCHED, and the merge rule is not
// changed. A change touching no production line still reads NOTHING_TOUCHED,
// and one touching only a promise's enforcer can still read ASSURED.
// @verifies check:INV-9
func TestReachedButUnguardedLinesAreUnproven(t *testing.T) {
	p := "orders:INV-1"
	base := auditOf("base", good(p, 1))
	head := auditOf("head", good(p, 1))
	kg := schema.KnowledgeGraph{
		Symbols: []schema.GraphSymbol{
			{FQN: "orders.Handle", File: "orders/handler.go", Feature: "orders", Reach: schema.ReachEntry},
			{FQN: "orders.Guard", File: "orders/guard.go", Feature: "orders", Reach: schema.ReachEntry},
		},
		EntryPoints: []schema.EntryPoint{{ID: "POST /orders", Flow: []schema.FlowStep{{Feature: "orders"}}}},
	}
	handler := impact.ChangedSymbol{Symbol: "orders.Handle", Kind: "function", File: "orders/handler.go", Lines: "3-22",
		Change: impact.ChangeModified, Feature: "orders"}
	md := func(r Report) string {
		var b bytes.Buffer
		WriteMarkdown(&b, r)
		return b.String()
	}

	in := scenario(&base, head, impact.Report{ChangedSymbols: []impact.ChangedSymbol{handler}})
	in.KG = kg
	rep := Compute(in)
	if rep.Verdict.State != Unproven || rep.Verdict.Code != CodeUnguardedLines {
		t.Fatalf("verdict %+v, want UNPROVEN %s", rep.Verdict, CodeUnguardedLines)
	}
	if !rep.MergeAllowed || rep.Merge.Code != CodeMergeOK {
		t.Errorf("unguarded lines are reported, not blocking: merge %v %+v", rep.MergeAllowed, rep.Merge)
	}
	if !strings.Contains(rep.Verdict.Text, "20 production lines that no promise guards") {
		t.Errorf("verdict text = %q", rep.Verdict.Text)
	}
	bs := rep.BlindSpot
	if bs.UnguardedLines != 20 || bs.GuardedLines != 0 || len(bs.Untraced) != 0 || len(bs.Unguarded) != 1 ||
		bs.Unguarded[0].Symbol != "orders.Handle" || bs.Unguarded[0].Count != 20 || !strings.Contains(bs.Unguarded[0].Reason, "entry point reaches it") {
		t.Errorf("blind spot = %+v", bs)
	}
	if bs.State != "info" || strings.Contains(bs.Text, "no production line") || strings.Contains(rep.Verdict.Text, "no production line") {
		t.Errorf("the text must not say no production line was touched: %q / %q", bs.Text, rep.Verdict.Text)
	}
	if m := md(rep); !strings.Contains(m, "not a refusal") || !strings.Contains(m, "reported, not blocking") ||
		!strings.Contains(m, "`orders/handler.go:3-22` `orders.Handle` (unguarded)") || strings.Contains(m, "✅") {
		t.Errorf("markdown:\n%s", m)
	}

	// A removed symbol that enforced nothing is unguarded too.
	removed := impact.ChangedSymbol{Symbol: "orders.Legacy", Kind: "function", File: "orders/legacy.go", OldLines: "1-5", Change: impact.ChangeRemoved, Feature: "orders"}
	in.Impact.ChangedSymbols = []impact.ChangedSymbol{removed}
	if rep := Compute(in); rep.Verdict.Code != CodeUnguardedLines || rep.BlindSpot.UnguardedLines != 5 || !rep.MergeAllowed {
		t.Errorf("removed unguarded symbol: %+v %+v", rep.Verdict, rep.BlindSpot)
	}

	// Only docs: no production line, ASSURED, NOTHING_TOUCHED.
	in.Impact.ChangedSymbols = nil
	in.Impact.ChangedFiles = []impact.FileChange{{Status: "M", OldPath: "docs/guide.md", NewPath: "docs/guide.md"}}
	rep = Compute(in)
	if rep.Verdict.State != Assured || rep.Verdict.Code != CodeNothingTouched || !rep.MergeAllowed ||
		rep.BlindSpot.Text != "the change touches no production line" || rep.BlindSpot.UnguardedLines != 0 {
		t.Errorf("docs only: %+v %+v", rep.Verdict, rep.BlindSpot)
	}

	// Only a promise's enforcer, every signal passing: ASSURED.
	in.Impact = impact.Report{ChangedSymbols: []impact.ChangedSymbol{guardEdited(p)}}
	rep = Compute(in)
	if rep.Verdict.State != Assured || rep.Verdict.Code != CodeAllSignals || rep.BlindSpot.GuardedLines != 10 || rep.BlindSpot.UnguardedLines != 0 {
		t.Errorf("enforcer only: %+v %+v", rep.Verdict, rep.BlindSpot)
	}

	// The enforcer and the handler: the handler keeps it at UNPROVEN.
	in.Impact.ChangedSymbols = append(in.Impact.ChangedSymbols, handler)
	rep = Compute(in)
	if rep.Verdict.Code != CodeUnguardedLines || rep.BlindSpot.GuardedLines != 10 || rep.BlindSpot.UnguardedLines != 20 || !rep.MergeAllowed {
		t.Errorf("enforcer and handler: %+v %+v", rep.Verdict, rep.BlindSpot)
	}

	// A checked reference guards a line: ASSURED, and not NOTHING_TOUCHED.
	in.KG.BRDs = []schema.BRD{{ID: "brd.orders", SuccessCriteria: []schema.BRDCriterion{
		{ID: "SC-1", DirectWire: true, EnforcedBy: []string{"orders.Handle"}}}}}
	in.Impact.ChangedSymbols = []impact.ChangedSymbol{handler}
	rep = Compute(in)
	if rep.Verdict.State != Assured || rep.Verdict.Code != CodeAllSignals || rep.BlindSpot.GuardedLines != 20 {
		t.Errorf("checked reference: %+v %+v", rep.Verdict, rep.BlindSpot)
	}
}

// --- evidence provenance ---

// Under --ci, evidence from a local run, or for another commit, is set aside
// and reads unknown.
// @verifies check:INV-4
func TestCIRefusesEvidenceNotTiedToHead(t *testing.T) {
	dir := t.TempDir()
	save := func(commit, run string) {
		set := results.Set{Commit: commit, CIRun: run, Results: map[string]results.Outcome{"bookshop.TestGuard": results.Pass}}
		if err := results.Save(dir, set); err != nil {
			t.Fatal(err)
		}
	}

	save("head", results.LocalRun)
	ev := Gather(dir, "head", true)
	if ev.Results.Counted || ev.Results.Code != CodeEvidenceNotCI || ev.Results.Tied {
		t.Fatalf("local run under --ci: %+v", ev.Results)
	}
	if ev.Suite.Present {
		t.Errorf("a suite from a local run must not read green: %+v", ev.Suite)
	}
	if ev := Gather(dir, "head", false); !ev.Results.Counted || ev.Results.Tied || !ev.Suite.Present {
		t.Errorf("a local run is advice without --ci: %+v", ev.Results)
	}

	save("other", "4812")
	if ev := Gather(dir, "head", true); ev.Results.Counted || ev.Results.Code != CodeEvidenceNotCI {
		t.Errorf("another commit under --ci: %+v", ev.Results)
	}

	save("head", "4812")
	ev = Gather(dir, "head", true)
	if !ev.Results.Counted || !ev.Results.Tied || !ev.Suite.Present {
		t.Fatalf("CI evidence of head: %+v", ev.Results)
	}

	// Setting evidence aside reads unknown, never a pass.
	save("head", results.LocalRun)
	ev = Gather(dir, "head", true)
	src := ev.Apply(audit.Sources{Results: results.Load(dir)})
	if len(src.Results.Results) != 0 {
		t.Errorf("set-aside results still reach the audit: %+v", src.Results)
	}
	p := "bookshop:INV-1"
	base := auditOf("base", good(p, 1))
	in := scenario(&base, auditOf("head", good(p, 1)), impact.Report{ChangedSymbols: []impact.ChangedSymbol{guardEdited(p)}})
	in.CI, in.Evidence = true, ev
	rep := Compute(in)
	g, ok := gapOf(rep, "*", SigSuiteGreen)
	if !ok || g.State != Unknown || g.Code != CodeEvidenceNotCI || rep.Verdict.State != Unproven {
		t.Fatalf("suite gap = %+v verdict %s", g, rep.Verdict.State)
	}
}

// In a local run too, evidence stamped with another commit is set aside: it
// reads unknown (EVIDENCE_STALE), the verdict is not ASSURED, and a stale red
// suite is not a fall. Evidence at head still counts. The snapshot side keeps
// its own rule.
// @verifies check:INV-8
func TestStaleEvidenceIsSetAsideLocally(t *testing.T) {
	dir := t.TempDir()
	saveAll := func(commit string, outcome results.Outcome) {
		t.Helper()
		set := results.Set{Commit: commit, CIRun: results.LocalRun, Results: map[string]results.Outcome{"bookshop.TestGuard": outcome}}
		if err := results.Save(dir, set); err != nil {
			t.Fatal(err)
		}
		if err := coverage.Save(dir, coverage.Set{Commit: commit, CIRun: results.LocalRun}); err != nil {
			t.Fatal(err)
		}
		bf := []byte(`{"commit":"` + commit + `","ci_run":"local"}`)
		if err := os.WriteFile(filepath.Join(dir, ".cache", "results", "breaks.json"), bf, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	saveAll("old", results.Pass)
	ev := Gather(dir, "head", false)
	for name, it := range map[string]EvidenceItem{"results": ev.Results, "coverage": ev.Coverage, "breaks": ev.Breaks} {
		if it.Counted || it.Code != CodeEvidenceStale || !strings.Contains(it.Text, "not HEAD head") {
			t.Errorf("%s at another commit, local run: %+v", name, it)
		}
	}
	if ev.Suite.Present {
		t.Errorf("a stale suite must not read green: %+v", ev.Suite)
	}
	if !strings.Contains(ev.Results.Text, "results are for old") || !strings.Contains(ev.Results.Text, "re-run the tests and ingest") {
		t.Errorf("results text = %q", ev.Results.Text)
	}
	src := ev.Apply(audit.Sources{Results: results.Load(dir)})
	if len(src.Results.Results) != 0 {
		t.Errorf("stale results still reach the audit: %+v", src.Results)
	}

	// The verdict: suite_green reads unknown (EVIDENCE_STALE), never ASSURED.
	p := "bookshop:INV-1"
	base := auditOf("base", good(p, 1))
	in := scenario(&base, auditOf("head", good(p, 1)), impact.Report{ChangedSymbols: []impact.ChangedSymbol{guardEdited(p)}})
	in.Evidence = ev
	rep := Compute(in)
	g, ok := gapOf(rep, "*", SigSuiteGreen)
	if !ok || g.State != Unknown || g.Code != CodeEvidenceStale || rep.Verdict.State == Assured {
		t.Fatalf("suite gap = %+v verdict %s", g, rep.Verdict.State)
	}

	// A stale red suite is a gap, never a fall.
	saveAll("old", results.Fail)
	in.Evidence = Gather(dir, "head", false)
	if rep := Compute(in); rep.Verdict.State == Broken || hasWeakening(rep, CodeSuiteRed) {
		t.Errorf("a stale red suite fell: %s %+v", rep.Verdict.State, rep.Weakenings)
	}

	// The snapshot side keeps its own rule: without --ci it counts the file.
	if sev := GatherSnapshot(dir, "head", false); !sev.Results.Counted || !sev.Suite.Present {
		t.Errorf("snapshot evidence: %+v", sev.Results)
	}

	// Evidence at head still counts in a local run.
	saveAll("head", results.Pass)
	ev = Gather(dir, "head", false)
	if !ev.Results.Counted || !ev.Coverage.Counted || !ev.Breaks.Counted || !ev.Suite.Present || ev.Results.Code != "" {
		t.Fatalf("evidence at head: %+v", ev)
	}
	in.Evidence = ev
	if rep := Compute(in); rep.Verdict.State != Assured {
		t.Errorf("evidence at head: verdict %s gaps %+v", rep.Verdict.State, rep.Gaps)
	}

	// An unstamped file cannot be tied to head either.
	saveAll("", results.Pass)
	if ev := Gather(dir, "head", false); ev.Results.Counted || ev.Results.Code != CodeEvidenceStale {
		t.Errorf("unstamped results: %+v", ev.Results)
	}
}

// --- snapshots and waivers files ---

func TestSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	rep := auditOf("abc123", good("bookshop:INV-1", 1))
	path, err := SaveSnapshot(dir, rep)
	if err != nil || path != SnapshotPath(dir, "abc123") {
		t.Fatalf("save: %q %v", path, err)
	}
	got, why := LoadSnapshot(dir, "abc123")
	if got == nil || len(got.Rows) != 1 {
		t.Fatalf("load: %v %q", got, why)
	}
	if got, why := LoadSnapshot(dir, "def456"); got != nil || !strings.Contains(why, "no base audit") {
		t.Errorf("missing snapshot: %v %q", got, why)
	}
	// A snapshot named for one commit but holding another is not used.
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(SnapshotPath(dir, "def456"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, why := LoadSnapshot(dir, "def456"); got != nil || !strings.Contains(why, "not def456") {
		t.Errorf("misnamed snapshot: %v %q", got, why)
	}
	// A dirty tree's audit is not the commit's.
	rep.Dirty = true
	if p, err := SaveSnapshot(dir, rep); p != "" || err != nil {
		t.Errorf("dirty save: %q %v", p, err)
	}
}

func TestLoadWaivers(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "waivers.yaml"), []byte("waivers:\n  - subject: bookshop:INV-1\n    kind: out_of_reach\n    reason: batch job\n  - kind: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := LoadWaivers(dir)
	if len(ws) != 1 || ws[0].Subject != "bookshop:INV-1" || ws[0].Kind != "out_of_reach" {
		t.Fatalf("waivers = %+v", ws)
	}
	// An out-of-reach waiver on a touched promise is listed in the blind spot.
	p := "bookshop:INV-1"
	base := auditOf("base", good(p, 1))
	in := scenario(&base, auditOf("head", good(p, 1)), impact.Report{ChangedSymbols: []impact.ChangedSymbol{guardEdited(p)}})
	in.Waivers = ws
	rep := Compute(in)
	if len(rep.BlindSpot.OutOfReach) != 1 || rep.Verdict.State != Unproven {
		t.Errorf("out of reach: %+v verdict %s", rep.BlindSpot.OutOfReach, rep.Verdict.State)
	}
}

// --- rendering ---

// UNPROVEN must not look green.
// @verifies check:INV-2
func TestMarkdownNeverGreenForUnproven(t *testing.T) {
	p := "bookshop:INV-1"
	base := auditOf("base", good(p, 1))
	unproven := Compute(scenario(&base, auditOf("head", good(p, 1, noCoverage())), impact.Report{ChangedSymbols: []impact.ChangedSymbol{guardEdited(p)}}))
	assured := Compute(scenario(&base, auditOf("head", good(p, 1)), impact.Report{ChangedSymbols: []impact.ChangedSymbol{guardEdited(p)}}))
	broken := Compute(scenario(&base, auditOf("head", good(p, 1, survived())), impact.Report{}))

	render := func(r Report) string {
		var b bytes.Buffer
		WriteMarkdown(&b, r)
		return b.String()
	}
	u, a, b := render(unproven), render(assured), render(broken)
	for name, md := range map[string]string{"unproven": u, "assured": a, "broken": b} {
		if !strings.HasPrefix(md, Marker+"\n") {
			t.Errorf("%s does not start with the marker: %q", name, md[:40])
		}
	}
	if strings.Contains(u, "✅") || !strings.Contains(u, "UNPROVEN is not a pass") || !strings.Contains(u, "UNPROVEN") {
		t.Errorf("UNPROVEN looks green:\n%s", u)
	}
	if !strings.Contains(a, "✅ ASSURED") || strings.Contains(b, "✅") || !strings.Contains(b, "BROKEN") {
		t.Errorf("badges wrong:\nASSURED:\n%s\nBROKEN:\n%s", a, b)
	}
	var text bytes.Buffer
	WriteText(&text, unproven)
	if !strings.Contains(text.String(), "UNPROVEN is not a pass") || strings.Contains(text.String(), "✅") {
		t.Errorf("text:\n%s", text.String())
	}
}

// --- the frozen contract ---

// The shape of lattice.check/1 is frozen by a golden file. A change that is
// intended updates it with LATTICE_UPDATE_GOLDEN=1 and bumps the contract
// version when a field is removed or renamed.
func TestCheckContractGolden(t *testing.T) {
	p, q, r := "bookshop:INV-1", "bookshop:INV-2", "bookshop:INV-3"
	base := auditOf("base", good(p, 2), good(q, 1))
	head := auditOf("head", good(p, 2), good(q, 1, noCoverage()), good(r, 1, unconfirmed()))
	im := impact.Report{
		ChangedSymbols: []impact.ChangedSymbol{guardEdited(p), guardEdited(q), guardEdited(r),
			{Symbol: "bookshop.Format", Kind: "function", File: "bookshop/format.go", Lines: "5-16", Change: impact.ChangeAdded, Feature: "bookshop"}},
		ManifestDiff: impact.ManifestDiff{Features: impact.KindDiff{Changed: []impact.ManifestChange{{
			ID: "bookshop", Fields: []impact.FieldChange{{Field: "invariants[INV-3]", Change: "added"}}}}}},
		ChangedFiles: []impact.FileChange{{Status: "M", OldPath: "lattice/waivers.yaml", NewPath: "lattice/waivers.yaml"}},
	}
	in := scenario(&base, head, im)
	in.Waivers = []Waiver{{Subject: q + "/exercised", Kind: "no_tool", Reason: "no coverage tool"}}
	rep := Compute(in)

	got, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	golden := filepath.Join("testdata", "check.golden.json")
	if os.Getenv("LATTICE_UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with LATTICE_UPDATE_GOLDEN=1 to write it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("lattice.check/1 changed shape; if intended, rerun with LATTICE_UPDATE_GOLDEN=1\n--- got\n%s", got)
	}
	var generic map[string]interface{}
	if err := json.Unmarshal(got, &generic); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"contract", "commit", "verdict", "merge_allowed", "promises", "gaps", "weakenings", "blind_spot", "base", "head"} {
		if _, ok := generic[k]; !ok {
			t.Errorf("contract field %q missing", k)
		}
	}
	if generic["contract"] != "lattice.check/1" {
		t.Errorf("contract = %v", generic["contract"])
	}
}

// An edit to a confirmed promise that carries its own decision file is
// re-confirmed by the change's approval: not BROKEN, the confirmed gap is
// pending and does not block the merge, tier 3 included.
// @verifies check:INV-6
func TestEditCarryingDecisionIsPending(t *testing.T) {
	p := "bookshop:INV-1"
	base := auditOf("base", good(p, 3))
	head := auditOf("head", good(p, 3, unconfirmed()))
	im := impact.Report{ManifestDiff: impact.ManifestDiff{Features: impact.KindDiff{Changed: []impact.ManifestChange{{
		ID: "bookshop", Fields: []impact.FieldChange{{Field: "invariants[INV-1].statement", Change: "changed"}}}}}}}
	in := scenario(&base, head, im)
	in.Reconfirm = map[string]bool{p: true}
	rep := Compute(in)
	if hasWeakening(rep, CodeConfirmationVoid) || rep.Verdict.State == Broken {
		t.Fatalf("an edit carrying its decision is not a weakening: %s %+v", rep.Verdict.State, rep.Weakenings)
	}
	g, ok := gapOf(rep, p, SigConfirmed)
	if !ok || g.Blocks || !strings.Contains(g.Text, "pending") {
		t.Errorf("confirmed gap = %+v", g)
	}
	if !rep.MergeAllowed {
		t.Errorf("merge refused: %+v", rep.Merge)
	}

	// Without the decision file the same edit voids the confirmation.
	in.Reconfirm = nil
	if rep := Compute(in); rep.Verdict.State != Broken || !hasWeakening(rep, CodeConfirmationVoid) {
		t.Errorf("without a decision: %s %+v", rep.Verdict.State, rep.Weakenings)
	}
}

// Reconfirmed reads only decision files under decisions/approvals: an approve
// at the BRD's head version covers its promises, one for another version
// covers nothing, and a promise confirm names its rows.
// @verifies check:INV-6
func TestReconfirmed(t *testing.T) {
	brds := []schema.BRD{{ID: "brd.bookshop", Version: 4, SuccessCriteria: []schema.BRDCriterion{
		{ID: "SC-1", MapsToInvariant: schema.InvariantRefs{"bookshop:INV-1", "bookshop:INV-2"}}}}}
	files := map[string]confirm.Decision{
		"lattice/decisions/approvals/brd.bookshop-v4.yaml":   {Action: confirm.ActionApprove, ID: "brd.bookshop", Version: 4},
		"lattice/decisions/approvals/brd.bookshop-v3.yaml":   {Action: confirm.ActionApprove, ID: "brd.bookshop", Version: 3},
		"lattice/decisions/approvals/brd.shelf-v1.yaml":      {Action: confirm.ActionPromiseConfirm, ID: "brd.shelf", Version: 1, Rows: []confirm.Row{{Promise: "shelf:INV-3"}}},
		"lattice/decisions/approvals/brd.bookshop-v4.r.yaml": {Action: confirm.ActionReturn, ID: "brd.bookshop", Version: 4},
		"lattice/features/bookshop.yaml":                     {Action: confirm.ActionApprove, ID: "brd.bookshop", Version: 4},
	}
	read := func(p string) ([]byte, error) { return files[p].Marshal() }
	var paths []string
	for p := range files {
		paths = append(paths, p)
	}
	got := Reconfirmed(paths, "lattice", read, brds)
	want := map[string]bool{"bookshop:INV-1": true, "bookshop:INV-2": true, "shelf:INV-3": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for p := range want {
		if !got[p] {
			t.Errorf("missing %s in %v", p, got)
		}
	}
}

// A promise with no result ingested at head is missing evidence, a gap, not
// a fall: the change reads UNPROVEN. A red result at head still falls.
// @verifies check:INV-7
func TestMissingResultsIsNotAFall(t *testing.T) {
	p := "bookshop:INV-1"
	base := auditOf("base", good(p, 1))
	noResults := func(r *audit.Row) { r.Class, r.Kind = audit.ClassNotDemonstrated, audit.KindNoResults }
	failing := func(r *audit.Row) { r.Class, r.Kind = audit.ClassNotDemonstrated, audit.KindFailing }

	rep := Compute(scenario(&base, auditOf("head", good(p, 1, noResults)), impact.Report{}))
	if rep.Verdict.State == Broken || hasWeakening(rep, CodeLevelDropped) {
		t.Errorf("no results at head is not a fall: %s %+v", rep.Verdict.State, rep.Weakenings)
	}

	rep = Compute(scenario(&base, auditOf("head", good(p, 1, failing)), impact.Report{}))
	if rep.Verdict.State != Broken || !hasWeakening(rep, CodeLevelDropped) {
		t.Errorf("a red result at head falls: %s %+v", rep.Verdict.State, rep.Weakenings)
	}
}
