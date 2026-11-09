// Package audit answers one question about every promise (invariant) in a
// workspace: is there evidence it is kept, or does it only look done?
//
// It computes nothing new about tests. The rungs come from rtm.Build (run
// once over one synthetic criterion per invariant), per-test coverage from
// the coverage index, reach from GraphSymbol.Reach, approvals from the BRD
// fields, and break outcomes from lattice/.cache/results/breaks.json, read as
// a file. The audit joins them into one row per invariant, labels each signal
// fact, upper_bound or proxy, and classifies the row.
//
// Unknown is never a pass and never a fail: "no tool" (no per-test coverage,
// no break run) is kept apart from "fake" (the evidence shows the verifier
// does not guard the promise).
package audit

// @module-feature audit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Schema is the version tag of the JSON report.
const Schema = "lattice.audit/1"

// Classes, in the order the audit reads worst first. Only break_tested and
// exercised are positive; exercised is the weaker of the two.
const (
	ClassFake             = "fake"
	ClassUnenforced       = "unenforced"
	ClassUnverified       = "unverified"
	ClassNotDemonstrated  = "not_demonstrated"
	ClassUnknown          = "unknown"
	ClassDemonstratedOnly = "demonstrated_only"
	ClassExercised        = "exercised"
	ClassBreakTested      = "break_tested"
)

// Classes lists every class, worst first.
var Classes = []string{
	ClassFake, ClassUnenforced, ClassUnverified, ClassNotDemonstrated,
	ClassUnknown, ClassDemonstratedOnly, ClassExercised, ClassBreakTested,
}

// Kinds narrow a class.
const (
	// fake
	KindNeverRunsEnforcer = "never_runs_enforcer"
	KindPassesWhenBroken  = "passes_when_broken"
	// unknown: a break survived, but a verifier of the promise did not run
	// (no runner, or an excluded journey), so it says nothing about that one.
	KindPartialRun = "partial_run"
	// demonstrated_only, unknown: the evidence that would decide is absent,
	// which is not a verdict on the verifier.
	KindNoTool          = "no_tool"          // no per-test coverage ingested
	KindPartialCoverage = "partial_coverage" // some verifiers have none
	KindStructuralOnly  = "structural_only"  // verifiable by a structural check, no test to audit
	// not_demonstrated
	KindNoResults = "no_results" // no passing result ingested for the verifiers
	KindFailing   = "failing"    // a verifier's ingested result is red
)

// Honesty labels (plan section 2).
const (
	Fact       = "fact"
	UpperBound = "upper_bound"
	Proxy      = "proxy"
)

// Signal is one measurement with its honesty label.
type Signal struct {
	Value   string   `json:"value"`
	Honesty string   `json:"honesty"`
	Detail  string   `json:"detail,omitempty"`
	Items   []string `json:"items,omitempty"`
	Method  string   `json:"method,omitempty"`
}

// Signals are the eight signals of one promise.
type Signals struct {
	Declared     Signal `json:"declared"`
	Enforced     Signal `json:"enforced"`
	Verified     Signal `json:"verified"`
	Demonstrated Signal `json:"demonstrated"`
	Exercised    Signal `json:"exercised"`
	Live         Signal `json:"live"`
	BreakTested  Signal `json:"break_tested"`
	Confirmed    Signal `json:"confirmed"`
}

// EnforcerEvidence is one enforcing symbol and its reach class.
type EnforcerEvidence struct {
	FQN   string `json:"fqn"`
	File  string `json:"file,omitempty"`
	Line  int    `json:"line,omitempty"`
	Reach string `json:"reach,omitempty"`
}

// VerifierEvidence is one verifying test and what ran.
type VerifierEvidence struct {
	FQN  string `json:"fqn"`
	File string `json:"file,omitempty"`
	// Result is the ingested outcome: pass, fail, skip or none.
	Result string `json:"result"`
	// Coverage is the basis of the test's coverage of the enforcers
	// (per_test, per_file, none; empty when no coverage was ingested), and
	// RanEnforcer says whether that run executed an enforcer. RanEnforcer is
	// only meaningful when Coverage is per_test.
	Coverage    string `json:"coverage,omitempty"`
	RanEnforcer bool   `json:"ran_enforcer"`
}

// Approval is the approval record of one BRD whose criterion maps to the
// promise: a fact about the record, not about the intent.
type Approval struct {
	BRD             string `json:"brd"`
	Criterion       string `json:"criterion"`
	Tier            int    `json:"tier"`
	State           string `json:"state"` // human | agent | draft | drifted
	Status          string `json:"status"`
	ApprovedBy      string `json:"approved_by,omitempty"`
	Version         int    `json:"version"`
	ApprovedVersion int    `json:"approved_version,omitempty"`
	Detail          string `json:"detail,omitempty"`
}

// Evidence is what stands behind a row's class.
type Evidence struct {
	Enforcers []EnforcerEvidence `json:"enforcers,omitempty"`
	Verifiers []VerifierEvidence `json:"verifiers,omitempty"`
	Breaks    []BreakRun         `json:"breaks,omitempty"`
	Approvals []Approval         `json:"approvals,omitempty"`
}

// Row is one promise.
type Row struct {
	Promise   string `json:"promise"` // <feature>:INV-n
	Feature   string `json:"feature"`
	Invariant string `json:"invariant"`
	Statement string `json:"statement"`
	// Tier is the highest tier among the criteria that map to the promise;
	// 1 when none does.
	Tier   int    `json:"tier"`
	Class  string `json:"class"`
	Kind   string `json:"kind,omitempty"`
	Reason string `json:"reason"`

	Signals  Signals  `json:"signals"`
	Evidence Evidence `json:"evidence"`
	// Notes are second readers' notes. They are shown and never change Class.
	Notes []Note `json:"notes,omitempty"`
}

// Summary counts the rows.
type Summary struct {
	Total   int            `json:"total"`
	ByClass map[string]int `json:"by_class"`
	// ByKind counts "<class>:<kind>" for rows that have a kind.
	ByKind map[string]int `json:"by_kind,omitempty"`
	Fakes  int            `json:"fakes"`
}

// Report is the audit: lattice.audit/1.
type Report struct {
	Schema  string  `json:"schema"`
	Commit  string  `json:"commit,omitempty"`
	Dirty   bool    `json:"dirty"`
	Inputs  Inputs  `json:"inputs"`
	Summary Summary `json:"summary"`
	// Note states what the confirmed signal is and is not.
	Note string `json:"note"`
	Rows []Row  `json:"rows"`
}

// confirmedNote is carried on every report: a human approval is a fact about
// the record, not proof the intent is right.
const confirmedNote = "confirmed is a fact about the approval record (who signed which version), not proof the promise is the right one. Only break_tested and exercised are positive; unknown is never a pass."

// Evidence files the audit read, with their commit stamps.
type Inputs struct {
	Results  ResultsInput  `json:"results"`
	Coverage CoverageInput `json:"coverage"`
	Reach    ReachInput    `json:"reach"`
	Breaks   BreaksInput   `json:"breaks"`
	Notes    NotesInput    `json:"notes"`
}

// ResultsInput describes lattice/.cache/results/results.json.
type ResultsInput struct {
	Present     bool   `json:"present"`
	Commit      string `json:"commit,omitempty"`
	MatchesHead bool   `json:"matches_head"`
}

// CoverageInput describes lattice/.cache/results/coverage.json.
type CoverageInput struct {
	Present     bool   `json:"present"`
	Commit      string `json:"commit,omitempty"`
	Dirty       bool   `json:"dirty,omitempty"`
	PerTest     int    `json:"per_test"`
	MatchesHead bool   `json:"matches_head"`
}

// ReachInput says whether any symbol carries a reach class.
type ReachInput struct {
	Present bool `json:"present"`
}

// BreaksInput describes lattice/.cache/results/breaks.json.
type BreaksInput struct {
	Present     bool   `json:"present"`
	Commit      string `json:"commit,omitempty"`
	Dirty       bool   `json:"dirty,omitempty"`
	GeneratedAt string `json:"generated_at,omitempty"`
	Runs        int    `json:"runs"`
	// Ignored is why the file was not used (stale commit, unknown schema).
	Ignored string `json:"ignored,omitempty"`
}

// NotesInput describes lattice/audit-notes.yaml.
type NotesInput struct {
	Present bool     `json:"present"`
	Count   int      `json:"count"`
	Errors  []string `json:"errors,omitempty"`
}

// Sources is everything Compute joins. Collect fills it from a workspace;
// tests build it by hand.
type Sources struct {
	KG      schema.KnowledgeGraph
	Results results.Set
	// Coverage is nil when no coverage was ingested.
	Coverage rtm.CoverageEvidence
	Breaks   *BreaksFile
	Notes    []Note
	// Stamps describe the evidence files; Collect sets them.
	Stamps Inputs
	Dirty  bool
}

// Compute builds the report. It is a pure function of its sources.
func Compute(src Sources) Report {
	kg := src.KG
	head := kg.GeneratedFromCommit

	rep := Report{Schema: Schema, Commit: head, Dirty: src.Dirty, Inputs: src.Stamps, Note: confirmedNote}
	rep.Inputs.Results.MatchesHead = stampMatches(src.Stamps.Results.Commit, head)
	rep.Inputs.Coverage.MatchesHead = stampMatches(src.Stamps.Coverage.Commit, head)
	for _, s := range kg.Symbols {
		if s.Reach != "" {
			rep.Inputs.Reach.Present = true
			break
		}
	}

	// One criterion per invariant lets rtm.Build do the walk once, so the
	// audit and `lattice rtm` can never disagree about a rung.
	var refs []string
	statements := map[string]schema.Invariant{}
	featureOf := map[string]string{}
	for _, f := range kg.Features {
		for _, inv := range f.Invariants {
			ref := f.ID + ":" + inv.ID
			refs = append(refs, ref)
			statements[ref] = inv
			featureOf[ref] = f.ID
		}
	}
	sort.Strings(refs)
	rtmRows := ladder(kg, refs, src)

	breaks, breaksInput := usableBreaks(src.Breaks, head)
	rep.Inputs.Breaks = mergeBreaksInput(src.Stamps.Breaks, breaksInput)

	symbols := map[string]schema.GraphSymbol{}
	for _, s := range kg.Symbols {
		symbols[s.FQN] = s
	}
	testFile := map[string]string{}
	for _, t := range kg.Tests {
		testFile[t.FQN] = t.File
	}
	brdMap := criteriaByPromise(kg)
	notesBy := map[string][]Note{}
	for _, n := range src.Notes {
		notesBy[n.Promise] = append(notesBy[n.Promise], n)
	}

	rep.Rows = make([]Row, 0, len(refs))
	for _, ref := range refs {
		inv := statements[ref]
		r := buildRow(ref, featureOf[ref], inv, rtmRows[ref], src, symbols, testFile, breaks, brdMap[ref])
		r.Notes = notesBy[ref]
		rep.Rows = append(rep.Rows, r)
	}
	sortRows(rep.Rows)
	rep.Summary = summarize(rep.Rows)
	return rep
}

func stampMatches(stamp, head string) bool {
	return stamp == "" || head == "" || stamp == head
}

// ladder runs rtm.Build over one synthetic criterion per promise and returns
// the row of each. The mutation threshold stays 0: a mutation score is not an
// audit signal, and a partial status would hide the coverage evidence.
func ladder(kg schema.KnowledgeGraph, refs []string, src Sources) map[string]rtm.Row {
	b := schema.BRD{ID: "audit"}
	for _, ref := range refs {
		b.SuccessCriteria = append(b.SuccessCriteria, schema.BRDCriterion{
			ID: ref, Statement: ref, MapsToInvariant: schema.InvariantRefs{ref},
		})
	}
	synth := kg
	synth.BRDs = []schema.BRD{b}
	m := rtm.Build(synth, rtm.Options{ResultOf: resultOf(src.Results), Coverage: src.Coverage})
	out := make(map[string]rtm.Row, len(m.Rows))
	for _, r := range m.Rows {
		out[r.CriterionID] = r
	}
	return out
}

// resultOf is the pass/fail lookup `lattice rtm` wires: a skip is no result.
func resultOf(set results.Set) func(string) (bool, bool) {
	return func(fqn string) (bool, bool) {
		o, ok := set.Lookup(fqn)
		if !ok || o == results.Skip {
			return false, false
		}
		return o == results.Pass, true
	}
}

// buildRow computes one row's signals and class.
func buildRow(ref, feature string, inv schema.Invariant, rr rtm.Row, src Sources,
	symbols map[string]schema.GraphSymbol, testFile map[string]string,
	breaks map[string][]BreakRun, crit []criterionRef) Row {

	r := Row{Promise: ref, Feature: feature, Invariant: inv.ID, Statement: inv.Statement, Tier: 1}
	for _, c := range crit {
		if c.tier > r.Tier {
			r.Tier = c.tier
		}
	}

	enf, ver := rr.Enforcers, rr.Verifiers
	r.Signals.Declared = Signal{Value: "yes", Honesty: Fact}
	r.Signals.Enforced = linkSignal(enf, "enforcer")
	r.Signals.Verified = linkSignal(ver, "verifier")

	for _, fqn := range enf {
		s := symbols[fqn]
		r.Evidence.Enforcers = append(r.Evidence.Enforcers,
			EnforcerEvidence{FQN: fqn, File: s.File, Line: s.Line, Reach: s.Reach})
	}

	cov := exercise(src.Coverage, ver, enf)
	for _, fqn := range ver {
		ve := VerifierEvidence{FQN: fqn, File: testFile[fqn], Result: "none"}
		if o, ok := src.Results.Lookup(fqn); ok {
			ve.Result = string(o)
		}
		ve.Coverage, ve.RanEnforcer = cov.perVerifier[fqn].basis, cov.perVerifier[fqn].ran
		r.Evidence.Verifiers = append(r.Evidence.Verifiers, ve)
	}

	r.Signals.Demonstrated = demonstrated(rr, len(ver) > 0)
	r.Signals.Exercised = cov.signal
	r.Signals.Live = live(r.Evidence.Enforcers)
	var bt Signal
	bt, r.Evidence.Breaks = breakSignal(breaks[ref])
	r.Signals.BreakTested = bt
	r.Signals.Confirmed, r.Evidence.Approvals = confirmed(crit)

	classify(&r, rr, cov, len(ver) > 0)
	return r
}

// linkSignal reports wired enforcers or anchored verifiers.
func linkSignal(items []string, what string) Signal {
	if len(items) == 0 {
		return Signal{Value: "no", Honesty: Fact, Detail: "no " + what + " is anchored"}
	}
	return Signal{Value: "yes", Honesty: Fact, Items: append([]string(nil), items...)}
}

// demonstrated reads the rtm rung: a verifier passed in the ingested results.
func demonstrated(rr rtm.Row, hasVerifiers bool) Signal {
	s := Signal{Honesty: Fact}
	switch {
	case !hasVerifiers:
		s.Value, s.Detail = "no", "no verifier to run"
	case rr.Status == rtm.StatusFailing:
		s.Value, s.Detail = "failing", "an ingested result for a verifier is red"
	case rr.Status.IsDemonstrated():
		s.Value, s.Detail = "yes", "every verifier with a result passed, and at least one has one"
	default:
		s.Value, s.Detail = "no", "no passing result ingested for the verifiers"
	}
	return s
}

// coverageVerdict is what per-test coverage says about a promise's verifiers.
type coverageVerdict struct {
	signal      Signal
	perVerifier map[string]verifierRun
	// none is true when every verifier has per-test coverage and none
	// executed an enforcer: the only state that reads as "never runs".
	none bool
	// ranAny is true when some verifier's own run executed an enforcer.
	ranAny bool
	// noCoverage is true when no per-test coverage was ingested at all.
	noCoverage bool
}

type verifierRun struct {
	basis string
	ran   bool
}

// exercise reads per-test coverage through the same rtm.CoverageEvidence the
// ladder uses. "no" needs per-test data for every verifier; anything less is
// unknown, never a verdict.
func exercise(cov rtm.CoverageEvidence, verifiers, enforcers []string) coverageVerdict {
	v := coverageVerdict{perVerifier: map[string]verifierRun{}}
	s := Signal{Honesty: Proxy}
	switch {
	case cov == nil || !cov.HasPerTest():
		v.noCoverage = true
		s.Value, s.Detail = "unknown", "no per-test coverage ingested (lattice results ingest --per-test)"
		v.signal = s
		return v
	case len(enforcers) == 0 || len(verifiers) == 0:
		s.Value, s.Detail = "unknown", "needs an enforcer and a verifier"
		v.signal = s
		return v
	}
	var ran, missing []string
	allPerTest := true
	for _, t := range verifiers {
		basis, executed := rtm.EvidenceNone, false
		for _, e := range enforcers {
			ex, b := cov.TestRan(t, e)
			if b == rtm.EvidencePerTest {
				basis = b
			} else if basis != rtm.EvidencePerTest && b == rtm.EvidencePerFile {
				basis = b
			}
			if ex && b == rtm.EvidencePerTest {
				executed = true
			}
		}
		v.perVerifier[t] = verifierRun{basis: basis, ran: executed}
		if executed {
			ran = append(ran, t)
		}
		if basis != rtm.EvidencePerTest {
			allPerTest = false
			missing = append(missing, t)
		}
	}
	switch {
	case len(ran) > 0:
		v.ranAny = true
		s.Value, s.Items = "yes", ran
		s.Detail = "a verifier's own run executed an enforcer; that the test checks it is not shown"
	case allPerTest:
		v.none = true
		s.Value = "no"
		s.Detail = "per-test coverage exists for every verifier and none executed an enforcer"
	default:
		s.Value = "unknown"
		s.Detail = "no per-test coverage for " + strings.Join(missing, ", ")
	}
	v.signal = s
	return v
}

// live folds the reach classes of the enforcers: entry when any is reached
// from a root, unknown when the call graph could not decide, test_only when
// only tests reach one, unreached when none is reached. It is an upper bound:
// a path the code allows, not one that runs.
func live(enf []EnforcerEvidence) Signal {
	s := Signal{Honesty: UpperBound}
	if len(enf) == 0 {
		s.Value, s.Detail = schema.ReachUnknown, "no enforcer to reach"
		return s
	}
	counts := map[string]int{}
	for _, e := range enf {
		c := e.Reach
		switch c {
		case schema.ReachEntry, schema.ReachTestOnly, schema.ReachUnreached:
		default:
			c = schema.ReachUnknown
		}
		counts[c]++
	}
	switch {
	case counts[schema.ReachEntry] > 0:
		s.Value = schema.ReachEntry
	case counts[schema.ReachUnknown] > 0:
		s.Value, s.Detail = schema.ReachUnknown, "reach is unknown for at least one enforcer (no roots, no reference data, or an unresolved call)"
	case counts[schema.ReachTestOnly] > 0:
		s.Value = schema.ReachTestOnly
	default:
		s.Value = schema.ReachUnreached
	}
	return s
}

// classify sets Class, Kind and Reason in the audit's precedence: fake, then
// break_tested, unknown (partial_run), exercised, demonstrated_only, a gap,
// and last unknown. A survived break is fake only when every verifier ran.
// @enforces audit:INV-1, audit:INV-2
func classify(r *Row, rr rtm.Row, cov coverageVerdict, hasVerifiers bool) {
	wired := len(rr.Enforcers) > 0 && hasVerifiers
	bt := r.Signals.BreakTested
	failing := rr.Status == rtm.StatusFailing

	switch {
	case wired && cov.none:
		r.Class, r.Kind = ClassFake, KindNeverRunsEnforcer
		r.Reason = "per-test coverage shows no verifier's own run executed an enforcer (" + strings.Join(rr.Verifiers, ", ") + ")"
		if bt.Value == "survived" {
			r.Reason += "; the break test agrees: it survived"
		}
		return
	case wired && bt.Value == "survived":
		r.Class, r.Kind = ClassFake, KindPassesWhenBroken
		r.Reason = "the verifiers passed with " + strings.Join(bt.Items, ", ") + " broken (" + bt.Method + "): they cannot tell the code is broken"
		return
	case wired && bt.Value == "killed" && !failing:
		r.Class = ClassBreakTested
		r.Reason = "breaking " + strings.Join(bt.Items, ", ") + " (" + bt.Method + ") made the verifiers fail"
		return
	case wired && bt.Value == "partial_run" && !failing:
		r.Class, r.Kind = ClassUnknown, KindPartialRun
		r.Reason = "breaking " + strings.Join(bt.Items, ", ") + " (" + bt.Method + ") left the verifiers green, but some could not run (" +
			strings.Join(notRun(r.Evidence.Breaks), ", ") + "): the break says nothing about them (not a fake)"
		return
	case rr.Status == rtm.StatusExercised:
		r.Class = ClassExercised
		r.Reason = "a verifier passed and its own run executed an enforcer; not shown to fail when the code is broken" + unknownBreak(bt)
		return
	case rr.Status == rtm.StatusDemonstrated:
		r.Class = ClassDemonstratedOnly
		switch {
		case cov.noCoverage:
			r.Kind = KindNoTool
			r.Reason = "a verifier passed; no per-test coverage is ingested, so whether it runs an enforcer is unknown (not a fake)"
		default:
			r.Kind = KindPartialCoverage
			r.Reason = "a verifier passed; " + r.Signals.Exercised.Detail + ", so whether it runs an enforcer is unknown (not a fake)"
		}
		r.Reason += unknownBreak(bt)
		return
	case rr.Status == rtm.StatusUnenforced:
		r.Class = ClassUnenforced
		r.Reason = "no symbol is anchored as an enforcer of this invariant"
		return
	case rr.Status == rtm.StatusUnverified:
		r.Class = ClassUnverified
		r.Reason = "an enforcer exists but no test is anchored as a verifier of this invariant"
		return
	case failing:
		r.Class, r.Kind = ClassNotDemonstrated, KindFailing
		r.Reason = "an ingested result for a verifier is red"
		return
	case rr.Status == rtm.StatusVerified && hasVerifiers:
		r.Class, r.Kind = ClassNotDemonstrated, KindNoResults
		r.Reason = "a verifier exists but no passing result is ingested for it (lattice results ingest)"
		return
	}
	r.Class = ClassUnknown
	switch {
	case rr.Status == rtm.StatusVerified && !hasVerifiers:
		r.Kind = KindStructuralOnly
		r.Reason = "verifiable by a structural check or mutation, not by a test, so there is no verifier to audit"
	default:
		r.Reason = "the ladder reads " + string(rr.Status) + ": " + rr.StatusReason
	}
}

// unknownBreak appends why the break signal could not strengthen the class.
func unknownBreak(bt Signal) string {
	if bt.Value == "unknown" && bt.Detail != "" {
		return " (break test unknown: " + bt.Detail + ")"
	}
	return ""
}

// summarize counts rows by class and kind.
func summarize(rows []Row) Summary {
	s := Summary{Total: len(rows), ByClass: map[string]int{}, ByKind: map[string]int{}}
	for _, c := range Classes {
		s.ByClass[c] = 0
	}
	for _, r := range rows {
		s.ByClass[r.Class]++
		if r.Kind != "" {
			s.ByKind[r.Class+":"+r.Kind]++
		}
	}
	s.Fakes = s.ByClass[ClassFake]
	if len(s.ByKind) == 0 {
		s.ByKind = nil
	}
	return s
}

// sortRows puts the worst class first, then by promise.
func sortRows(rows []Row) {
	rank := map[string]int{}
	for i, c := range Classes {
		rank[c] = i
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rank[rows[i].Class] != rank[rows[j].Class] {
			return rank[rows[i].Class] < rank[rows[j].Class]
		}
		return rows[i].Promise < rows[j].Promise
	})
}

// Positive reports whether a class may be shown in the positive colour:
// only break_tested and exercised.
// @enforces audit:INV-2
func Positive(class string) bool {
	return class == ClassBreakTested || class == ClassExercised
}

// Describe is a one-line label of a row for lists.
func (r Row) Describe() string {
	if r.Kind != "" {
		return fmt.Sprintf("%s (%s)", r.Class, r.Kind)
	}
	return r.Class
}
