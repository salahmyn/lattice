// Package rtm builds the Requirements Traceability Matrix: the walk
// from a BRD's success_criteria down to the invariants, enforcers,
// verifiers, and mutation scores that back them.
//
// The matrix is computed live from the KnowledgeGraph — no schema
// changes, no extra files on disk. Status per criterion is a pure
// function of the graph, so the same answer is reachable from CLI,
// UI, MCP, and validation passes.
package rtm

// @module-feature rtm

import (
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Status is the verification state of one BRD success_criterion.
//
// The ordering is by severity (worst first); roll-ups pick the worst
// status across a set of rows.
type Status string

const (
	// StatusPhantom — `maps_to_invariant` names an invariant that
	// doesn't exist on the manifest. The criterion is broken on
	// disk; fix the reference before anything else.
	StatusPhantom Status = "phantom"

	// StatusUnenforced — invariant exists but no symbol declares
	// `enforces_invariant`. Equivalent to the v0.1 UNENFORCED rule.
	StatusUnenforced Status = "unenforced"

	// StatusUnverified — invariant exists but no test declares
	// `verifies`. Equivalent to v0.1 UNVERIFIED.
	StatusUnverified Status = "unverified"

	// StatusFailing — a verifier exists AND an ingested test result for
	// it is red (v0.8 γ). The most actionable bad state: not "no test"
	// but "the test we have is failing on this commit."
	StatusFailing Status = "failing"

	// StatusUnmapped — SC has no `maps_to_invariant` at all. Common
	// during BRD drafting; info-level, not a failure.
	StatusUnmapped Status = "unmapped"

	// StatusPartial — enforcer + verifier both present, but mutation
	// score is below the per-invariant or default threshold.
	StatusPartial Status = "partial"

	// StatusVerified — invariant exists, enforcer + verifier both
	// present, mutation OK (or unknown). This is the v0.8 DECLARED
	// rung: the verifier *exists*, but its pass/fail has not been
	// ingested for the current commit.
	StatusVerified Status = "verified"

	// StatusDemonstrated — everything StatusVerified requires, plus an
	// ingested test result showing the verifier *passed* on the
	// generated commit (v0.8 γ).
	StatusDemonstrated Status = "demonstrated"

	// StatusExercised — demonstrated, and for every invariant behind the
	// unit at least one verifier's own per-test coverage executed at least
	// one enforcer (rnd/0009). The top of the ladder. Only per-test
	// coverage can promote to it; it never changes what demonstrated means.
	StatusExercised Status = "exercised"
)

// IsDemonstrated reports whether a status is demonstrated or above: an
// exercised unit is demonstrated too, so counts of demonstrated units do
// not drop when coverage is ingested.
func (s Status) IsDemonstrated() bool {
	return s == StatusDemonstrated || s == StatusExercised
}

// Evidence bases a coverage claim rests on, strongest first (rnd/0009).
// Only EvidencePerTest can promote a unit to exercised.
const (
	EvidencePerTest = "per_test" // the verifier's own run
	EvidencePerFile = "per_file" // a run of the verifier's whole test file
	EvidenceSuite   = "suite"    // one run of the whole suite
	EvidenceNone    = "none"     // no coverage covers the enforcers
)

// CoverageEvidence is ingested test coverage, wired by the caller to the
// coverage package so rtm keeps importing only schema. nil means no
// coverage was ingested: no row is exercised and no evidence is reported.
type CoverageEvidence interface {
	// TestRan reports whether a test's own coverage executed a symbol, and
	// the basis: EvidencePerTest when the test has a report of its own,
	// EvidencePerFile when only a run of its test file does, EvidenceNone
	// when neither exists (executed is then false and means nothing).
	TestRan(test, symbol string) (executed bool, basis string)
	// SuiteRan reports whether the whole suite executed a symbol. known is
	// false when no suite report covers the symbol's file.
	SuiteRan(symbol string) (executed, known bool)
	// EnforcerLines rolls up the body lines of the enforcers: over what the
	// tests ran when each has per-test line data (overTests), else over the
	// suite. ok is false when neither has data for them.
	EnforcerLines(enforcers, tests []string) (lines, hit int, overTests, ok bool)
	// HasPerTest reports whether any per-test coverage was ingested.
	HasPerTest() bool
}

// EnforcerCoverage is the share of an invariant's enforcer body lines that
// ran, and what it was measured over.
type EnforcerCoverage struct {
	Lines   int     `json:"lines"`
	Hit     int     `json:"hit"`
	Percent float64 `json:"percent"`
	// Over is "verifiers" (the union of the verifiers' per-test runs) or
	// "suite" (the whole run, when a verifier has no per-test data).
	Over string `json:"over"`
}

// Severity returns 0 for the worst status and increases for healthier
// states. Used to roll up multiple rows to one summary status.
func (s Status) Severity() int {
	switch s {
	case StatusPhantom:
		return 0
	case StatusUnenforced:
		return 1
	case StatusFailing:
		return 2
	case StatusUnverified:
		return 3
	case StatusUnmapped:
		return 4
	case StatusPartial:
		return 5
	case StatusVerified:
		return 6
	case StatusDemonstrated:
		return 7
	case StatusExercised:
		return 8
	}
	return 0
}

// Row is one walked SC: BRD → SC → invariant → enforcers + verifiers.
// One row per success_criterion. UnmappedReason is filled when the
// criterion has no maps_to_invariant; PhantomRef holds the bad ref
// when the lookup misses.
type Row struct {
	BRDID         string   `json:"brd_id"`
	BRDTitle      string   `json:"brd_title,omitempty"`
	CriterionID   string   `json:"criterion_id"`
	Statement     string   `json:"statement"`
	MapsTo        string   `json:"maps_to,omitempty"`
	FeatureID     string   `json:"feature_id,omitempty"`
	InvariantID   string   `json:"invariant_id,omitempty"`
	Invariant     string   `json:"invariant,omitempty"`
	Enforcers     []string `json:"enforcers,omitempty"` // symbol FQNs
	Verifiers     []string `json:"verifiers,omitempty"` // test FQNs
	MutationScore float64  `json:"mutation_score,omitempty"`
	HasMutation   bool     `json:"has_mutation,omitempty"`
	Status        Status   `json:"status"`
	StatusReason  string   `json:"status_reason,omitempty"`

	// Tier is the pinned criticality of the criterion (v0.8.1):
	// 1 default, 2 auth/permissions/data-loss, 3 money/regulatory.
	Tier int `json:"tier,omitempty"`
	// DirectWire marks a criterion wired directly to enforcers and
	// verifiers without a restatement invariant.
	DirectWire bool `json:"direct_wire,omitempty"`
	// Flagged + Flags carry open meaning flags. A flag rides alongside
	// the computed status — it is never hidden behind a green row.
	Flagged bool     `json:"flagged,omitempty"`
	Flags   []string `json:"flags,omitempty"`

	// Coverage (rnd/0009), set only when coverage was ingested. Evidence is
	// the strongest basis behind the deciding invariant's verifiers
	// (per_test, per_file, suite or none); ExercisedBy names the verifiers
	// whose per-test run executed an enforcer; CoverageReason says why a
	// demonstrated row is not exercised.
	Evidence         string            `json:"evidence,omitempty"`
	EnforcerCoverage *EnforcerCoverage `json:"enforcer_coverage,omitempty"`
	ExercisedBy      []string          `json:"exercised_by,omitempty"`
	CoverageReason   string            `json:"coverage_reason,omitempty"`
}

// Ref names the invariant that decided the row's status: the only one
// for a criterion mapped to one, the worst of several otherwise. It falls
// back to MapsTo when no ref parsed.
func (r Row) Ref() string {
	switch {
	case r.FeatureID != "" && r.InvariantID != "":
		return r.FeatureID + ":" + r.InvariantID
	case r.FeatureID != "":
		return r.FeatureID
	}
	return r.MapsTo
}

// BRDSummary rolls up every Row for one BRD into a verification ratio.
type BRDSummary struct {
	BRDID    string `json:"brd_id"`
	BRDTitle string `json:"brd_title,omitempty"`
	Total    int    `json:"total"`
	// Demonstrated counts rows at demonstrated or above (exercised rows
	// too); Exercised counts those that are also exercised.
	Demonstrated     int     `json:"demonstrated"`
	Exercised        int     `json:"exercised"`
	Verified         int     `json:"verified"`
	Partial          int     `json:"partial"`
	Failing          int     `json:"failing"`
	Unmapped         int     `json:"unmapped"`
	Unverified       int     `json:"unverified"`
	Unenforced       int     `json:"unenforced"`
	Phantom          int     `json:"phantom"`
	VerificationRate float64 `json:"verification_rate"` // (verified+demonstrated) / total
	WorstStatus      Status  `json:"worst_status"`
	// Flagged counts rows with open meaning flags (v0.8.1) — kept as
	// its own number because a flag overlays any status.
	Flagged int `json:"flagged,omitempty"`

	// Scenario roll-up (v0.8 α): user_scenarios are walked alongside
	// success_criteria so a BRD's headline counts what a user *does*,
	// not just what an invariant asserts.
	ScenarioTotal        int `json:"scenario_total"`
	ScenarioDemonstrated int `json:"scenario_demonstrated"`
	ScenarioExercised    int `json:"scenario_exercised"`
}

// ScenarioRow is one walked BRD user_scenario (v0.8 α). A scenario is a
// verifiable unit peer to a success_criterion: it is DEMONSTRATED when a
// tagged verifier test passed, VERIFIED when such a test exists,
// UNVERIFIED when its verified_by resolves to nothing, and UNMAPPED when
// it claims no verifier at all.
type ScenarioRow struct {
	BRDID             string   `json:"brd_id"`
	BRDTitle          string   `json:"brd_title,omitempty"`
	ScenarioID        string   `json:"scenario_id"`
	Actor             string   `json:"actor,omitempty"`
	Verifiers         []string `json:"verifiers,omitempty"`    // resolved test FQNs
	EntryPoints       []string `json:"entry_points,omitempty"` // resolved EP ids
	TouchesEntryPoint bool     `json:"touches_entry_point"`
	Status            Status   `json:"status"`
	StatusReason      string   `json:"status_reason,omitempty"`

	// Flagged + Flags — open meaning flags on the scenario (v0.8.1),
	// reported alongside the computed status.
	Flagged bool     `json:"flagged,omitempty"`
	Flags   []string `json:"flags,omitempty"`

	// Coverage (rnd/0009): a scenario is exercised when it is demonstrated
	// and a verifier's per-test run executed the handler of an entry point
	// it names. Set only when coverage was ingested.
	Evidence       string   `json:"evidence,omitempty"`
	ExercisedBy    []string `json:"exercised_by,omitempty"`
	CoverageReason string   `json:"coverage_reason,omitempty"`
}

// Matrix is the full traceability output: every row + per-BRD
// summaries. Ordering is deterministic so re-runs are byte-stable.
type Matrix struct {
	Rows      []Row         `json:"rows"`
	Scenarios []ScenarioRow `json:"scenarios"`
	Summaries []BRDSummary  `json:"summaries"`
}

// Options tunes computation. MutationThreshold below sets the cutoff
// for PARTIAL vs VERIFIED — passed in by the caller so the same
// configured threshold drives validation and RTM display.
type Options struct {
	MutationThreshold float64

	// ResultOf reports the ingested pass/fail of a verifier test, keyed
	// by its FQN (v0.8 γ). known is false when no result was ingested
	// for that test — the RTM then behaves exactly as v0.7 (verifier
	// existence ⇒ StatusVerified). Wired by the caller to
	// results.Set.Lookup so rtm stays dependency-light (it imports only
	// schema). nil ResultOf means "no results ingested."
	ResultOf func(testFQN string) (passed bool, known bool)

	// FlagsOf returns the open meaning-flag reasons for a unit
	// ("brd.id/SC-1", "brd.id/US-1") — v0.8.1. A flagged row keeps its
	// computed status AND carries the flag: demonstrated-but-flagged
	// reports both, never one hidden behind the other. Wired by the
	// caller to the flags store; nil means no flag store.
	FlagsOf func(unit string) []string

	// ProfileLite caps the ladder at verified — the lite profile's
	// honest ceiling is "wired"; a lite workspace never reports
	// demonstrated until the full profile (architect pass) is adopted.
	ProfileLite bool

	// Coverage is the ingested coverage evidence (rnd/0009); nil means none
	// was ingested and nothing is exercised.
	Coverage CoverageEvidence
}

// invKey identifies an invariant uniquely across the graph by its
// (feature_id, invariant_id) pair. Hoisted to package level so the
// classify helper can take it as a parameter without redeclaring an
// inline struct that wouldn't be type-identical.
type invKey struct{ feature, inv string }

// Build walks the graph and returns the matrix. Empty graphs return
// an empty (but non-nil) matrix — never nil slices, so JSON consumers
// see [].
func Build(kg schema.KnowledgeGraph, opts Options) Matrix {
	// Index everything we need to look up by ref.
	featByID := map[string]*schema.Manifest{}
	for i := range kg.Features {
		featByID[kg.Features[i].ID] = &kg.Features[i]
	}

	// Per-invariant: enforcers, verifiers, mutation score.
	enforcers := map[invKey][]string{}
	verifiers := map[invKey][]string{}
	mutation := map[invKey]float64{}
	mutationOK := map[invKey]bool{}

	for _, sym := range kg.Symbols {
		for _, ref := range sym.EnforcesInvariants {
			feature, inv := resolveRef(ref, sym.Feature)
			k := invKey{feature, inv}
			enforcers[k] = append(enforcers[k], sym.FQN)
		}
	}
	// testFQNs lets a scenario's explicit verified_by entry be classified
	// as a test reference (vs an entry-point reference).
	testFQNs := map[string]bool{}
	for _, t := range kg.Tests {
		testFQNs[t.FQN] = true
		for _, ref := range t.Verifies {
			feature, inv := resolveRef(ref, t.Feature)
			k := invKey{feature, inv}
			verifiers[k] = append(verifiers[k], t.FQN)
		}
	}
	// epIDs lets a scenario's verified_by entry be classified as a
	// declared entry point — the journey-coverage signal (v0.8 β).
	epIDs := map[string]bool{}
	handlers := map[string]string{} // entry point id -> handler symbol
	for _, ep := range kg.EntryPoints {
		epIDs[ep.ID] = true
		handlers[ep.ID] = ep.Handler.Symbol
	}
	// Mutation scores live on the Manifest as a map keyed by
	// invariant id; lift to invKey for uniform lookup.
	for _, f := range kg.Features {
		for invID, score := range f.MutationScores {
			k := invKey{f.ID, invID}
			mutation[k] = score
			mutationOK[k] = true
		}
	}

	var rows []Row
	var scenarios []ScenarioRow
	summaries := map[string]*BRDSummary{}

	for _, b := range kg.BRDs {
		sum := &BRDSummary{BRDID: b.ID, BRDTitle: b.Title}
		summaries[b.ID] = sum

		for _, sc := range b.SuccessCriteria {
			row := Row{
				BRDID:       b.ID,
				BRDTitle:    b.Title,
				CriterionID: sc.ID,
				Statement:   sc.Statement,
				MapsTo:      sc.MapsToInvariant.String(),
				Tier:        sc.EffectiveTier(),
			}
			classify(&row, sc, featByID, enforcers, verifiers, mutation, mutationOK, opts)
			applyOverlays(&row.Status, &row.StatusReason, &row.Flagged, &row.Flags,
				b.ID+"/"+sc.ID, opts)
			rows = append(rows, row)

			if row.Flagged {
				sum.Flagged++
			}
			sum.Total++
			switch row.Status {
			case StatusExercised:
				sum.Demonstrated++
				sum.Exercised++
			case StatusDemonstrated:
				sum.Demonstrated++
			case StatusVerified:
				sum.Verified++
			case StatusPartial:
				sum.Partial++
			case StatusFailing:
				sum.Failing++
			case StatusUnmapped:
				sum.Unmapped++
			case StatusUnverified:
				sum.Unverified++
			case StatusUnenforced:
				sum.Unenforced++
			case StatusPhantom:
				sum.Phantom++
			}
		}

		// Scenario walk (v0.8 α): user_scenarios are verifiable units too.
		for _, us := range b.UserScenarios {
			sr := classifyScenario(b, us, verifiers, testFQNs, epIDs, opts)
			exerciseScenario(&sr, testFQNs, handlers, opts)
			applyOverlays(&sr.Status, &sr.StatusReason, &sr.Flagged, &sr.Flags,
				b.ID+"/"+us.ID, opts)
			scenarios = append(scenarios, sr)
			sum.ScenarioTotal++
			if sr.Status.IsDemonstrated() {
				sum.ScenarioDemonstrated++
			}
			if sr.Status == StatusExercised {
				sum.ScenarioExercised++
			}
		}
	}

	// Finalize summaries: ratio + worst-status roll-up.
	summaryList := make([]BRDSummary, 0, len(summaries))
	for _, s := range summaries {
		if s.Total > 0 {
			s.VerificationRate = round4(float64(s.Verified+s.Demonstrated) / float64(s.Total))
		}
		s.WorstStatus = worstFor(s)
		summaryList = append(summaryList, *s)
	}

	// Deterministic ordering.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].BRDID != rows[j].BRDID {
			return rows[i].BRDID < rows[j].BRDID
		}
		return rows[i].CriterionID < rows[j].CriterionID
	})
	sort.Slice(summaryList, func(i, j int) bool {
		return summaryList[i].BRDID < summaryList[j].BRDID
	})
	sort.Slice(scenarios, func(i, j int) bool {
		if scenarios[i].BRDID != scenarios[j].BRDID {
			return scenarios[i].BRDID < scenarios[j].BRDID
		}
		return scenarios[i].ScenarioID < scenarios[j].ScenarioID
	})

	if rows == nil {
		rows = []Row{}
	}
	if scenarios == nil {
		scenarios = []ScenarioRow{}
	}
	return Matrix{Rows: rows, Scenarios: scenarios, Summaries: summaryList}
}

// classifyScenario computes a scenario's truth-level (v0.8 α). Verifiers
// resolve two ways: explicit verified_by test FQNs, and tests tagged
// `@verifies brd.<id>:US-N` (which already land in the shared verifiers
// map under invKey{brd.id, scenario.id}). An entry-point id in verified_by
// flags the scenario as reachable through a real trigger — the
// journey-coverage signal.
func classifyScenario(
	b schema.BRD,
	us schema.UserScenario,
	verifiers map[invKey][]string,
	testFQNs, epIDs map[string]bool,
	opts Options,
) ScenarioRow {
	sr := ScenarioRow{
		BRDID:      b.ID,
		BRDTitle:   b.Title,
		ScenarioID: us.ID,
		Actor:      us.Actor,
	}

	// Reverse verifiers: tests that tagged @verifies brd.<id>:US-N.
	seen := map[string]bool{}
	for _, fqn := range verifiers[invKey{b.ID, us.ID}] {
		if !seen[fqn] {
			seen[fqn] = true
			sr.Verifiers = append(sr.Verifiers, fqn)
		}
	}
	// Explicit verified_by entries: test FQN → verifier; EP id → reach.
	for _, ref := range us.VerifiedBy {
		switch {
		case epIDs[ref]:
			sr.EntryPoints = append(sr.EntryPoints, ref)
			sr.TouchesEntryPoint = true
		case testFQNs[ref]:
			if !seen[ref] {
				seen[ref] = true
				sr.Verifiers = append(sr.Verifiers, ref)
			}
		default:
			// Unresolved entry — recorded as a verifier candidate so the
			// status falls to unverified rather than silently vanishing.
			if !seen[ref] {
				seen[ref] = true
				sr.Verifiers = append(sr.Verifiers, ref)
			}
		}
	}

	if len(us.VerifiedBy) == 0 && len(sr.Verifiers) == 0 {
		sr.Status = StatusUnmapped
		sr.StatusReason = "scenario declares no verified_by"
		return sr
	}

	// Resolve pass/fail across the verifier tests.
	anyKnown, anyFail, allKnownPass := false, false, true
	resolvable := false
	for _, fqn := range sr.Verifiers {
		if !testFQNs[fqn] {
			continue // an unresolved ref or an EP — not a runnable test
		}
		resolvable = true
		if opts.ResultOf != nil {
			if passed, known := opts.ResultOf(fqn); known {
				anyKnown = true
				if !passed {
					anyFail = true
				}
			} else {
				allKnownPass = false
			}
		} else {
			allKnownPass = false
		}
	}

	if !resolvable {
		sr.Status = StatusUnverified
		sr.StatusReason = "verified_by names no test on the graph"
		return sr
	}
	switch {
	case anyFail:
		sr.Status = StatusFailing
		sr.StatusReason = "an ingested result for a verifier is red"
	case anyKnown && allKnownPass:
		sr.Status = StatusDemonstrated
		sr.StatusReason = "verifier passed on the generated commit"
	default:
		sr.Status = StatusVerified
		sr.StatusReason = "verifier exists; pass/fail not ingested"
	}
	return sr
}

// classify computes the row's Status + supporting fields. Pulled out
// so the rules read top-to-bottom in worst-first order.
// @enforces rtm:INV-1, rtm:INV-2, rtm:INV-4
func classify(
	row *Row,
	sc schema.BRDCriterion,
	featByID map[string]*schema.Manifest,
	enforcers, verifiers map[invKey][]string,
	mutation map[invKey]float64,
	mutationOK map[invKey]bool,
	opts Options,
) {
	// direct_wire (v0.8.1): the criterion IS its own invariant — its
	// enforcers/verifiers attach directly, no restatement invariant
	// minted. Same ladder from here: unenforced → unverified →
	// verified → demonstrated.
	if sc.DirectWire {
		row.DirectWire = true
		row.Enforcers = sc.EnforcedBy
		row.Verifiers = sc.VerifiedBy
		switch {
		case len(row.Enforcers) == 0:
			row.Status = StatusUnenforced
			row.StatusReason = "direct-wired criterion lists no enforced_by"
		case len(row.Verifiers) == 0:
			row.Status = StatusUnverified
			row.StatusReason = "direct-wired criterion lists no verified_by"
		default:
			if status, reason, decided := resultStatus(row.Verifiers, opts.ResultOf); decided {
				row.Status = status
				row.StatusReason = reason
			} else {
				row.Status = StatusVerified
			}
			exercise(row, row.Enforcers, row.Verifiers, opts)
		}
		return
	}

	if len(sc.MapsToInvariant) == 0 {
		row.Status = StatusUnmapped
		row.StatusReason = "success criterion has no `maps_to_invariant`"
		return
	}

	// A criterion mapped to several invariants holds only when all of
	// them do: each ref is walked on its own and the worst one decides.
	// Enforcers and verifiers are the union, for display.
	var statements []string
	decided := false
	for _, ref := range sc.MapsToInvariant {
		var r Row
		classifyRef(&r, ref, featByID, enforcers, verifiers, mutation, mutationOK, opts)
		if r.Invariant != "" {
			statements = append(statements, r.Invariant)
		}
		row.Enforcers = appendNew(row.Enforcers, r.Enforcers)
		row.Verifiers = appendNew(row.Verifiers, r.Verifiers)
		if decided && r.Status.Severity() >= row.Status.Severity() {
			continue
		}
		decided = true
		row.FeatureID, row.InvariantID = r.FeatureID, r.InvariantID
		row.MutationScore, row.HasMutation = r.MutationScore, r.HasMutation
		row.Status, row.StatusReason = r.Status, r.StatusReason
		row.Evidence, row.EnforcerCoverage = r.Evidence, r.EnforcerCoverage
		row.ExercisedBy, row.CoverageReason = r.ExercisedBy, r.CoverageReason
		if len(sc.MapsToInvariant) > 1 && r.CoverageReason != "" {
			row.CoverageReason = ref + ": " + r.CoverageReason
		}
		if len(sc.MapsToInvariant) > 1 && r.StatusReason != "" {
			row.StatusReason = ref + ": " + r.StatusReason
		}
	}
	row.Invariant = strings.Join(statements, " ")
}

// classifyRef walks one `<feature.id>:<INV-N>` ref: the invariant, its
// enforcers and verifiers, mutation evidence and ingested results.
func classifyRef(
	row *Row,
	ref string,
	featByID map[string]*schema.Manifest,
	enforcers, verifiers map[invKey][]string,
	mutation map[invKey]float64,
	mutationOK map[invKey]bool,
	opts Options,
) {
	feature, inv, ok := splitInvariantRef(ref)
	row.FeatureID = feature
	row.InvariantID = inv
	if !ok {
		row.Status = StatusPhantom
		row.StatusReason = "maps_to_invariant must be `<feature.id>:<INV-N>`"
		return
	}

	f := featByID[feature]
	if f == nil {
		row.Status = StatusPhantom
		row.StatusReason = "feature " + feature + " not found"
		return
	}
	var matched *schema.Invariant
	for i := range f.Invariants {
		if f.Invariants[i].ID == inv {
			matched = &f.Invariants[i]
			break
		}
	}
	if matched == nil {
		row.Status = StatusPhantom
		row.StatusReason = "invariant " + inv + " not declared on " + feature
		return
	}
	row.Invariant = matched.Statement

	k := invKey{feature, inv}
	row.Enforcers = enforcers[k]
	row.Verifiers = verifiers[k]

	switch {
	case len(row.Enforcers) == 0:
		row.Status = StatusUnenforced
		row.StatusReason = "no symbol enforces " + inv
		return
	case len(row.Verifiers) == 0 && !hasStructuralOrMutationOnly(matched):
		row.Status = StatusUnverified
		row.StatusReason = "no test verifies " + inv
		return
	}

	if mutationOK[k] {
		row.MutationScore = mutation[k]
		row.HasMutation = true
		if opts.MutationThreshold > 0 && mutation[k] < opts.MutationThreshold {
			row.Status = StatusPartial
			row.StatusReason = "mutation score below threshold"
			return
		}
	}

	// v0.8 γ — fold in ingested test results. A red verifier downgrades to
	// StatusFailing; an all-green (and at least one known) verifier set
	// upgrades to StatusDemonstrated. With no results ingested
	// (ResultOf nil or unknown) the status stays StatusVerified — exactly
	// the v0.7 "declared" rung.
	if status, reason, decided := resultStatus(row.Verifiers, opts.ResultOf); decided {
		row.Status = status
		row.StatusReason = reason
	} else {
		row.Status = StatusVerified
	}
	exercise(row, row.Enforcers, row.Verifiers, opts)
}

// exercise folds ingested coverage into a wired row (rnd/0009): it records
// the evidence basis and the enforcer line coverage, and lifts a
// demonstrated row to exercised when a verifier's own per-test run executed
// an enforcer. per_file and suite evidence are reported but never promote.
// @enforces rtm:INV-8
func exercise(row *Row, enforcers, verifiers []string, opts Options) {
	cov := opts.Coverage
	if cov == nil || len(enforcers) == 0 || len(verifiers) == 0 {
		return
	}
	best := EvidenceNone
	var missing, perFileOnly []string
	for _, v := range verifiers {
		basis, ran := EvidenceNone, false
		for _, e := range enforcers {
			executed, b := cov.TestRan(v, e)
			if stronger(b, basis) {
				basis = b
			}
			if executed && b == EvidencePerTest {
				ran = true
			}
		}
		if ran {
			row.ExercisedBy = append(row.ExercisedBy, v)
		}
		switch basis {
		case EvidencePerFile:
			perFileOnly = append(perFileOnly, v)
		case EvidenceNone:
			missing = append(missing, v)
		}
		if stronger(basis, best) {
			best = basis
		}
	}
	if best == EvidenceNone {
		for _, e := range enforcers {
			if _, known := cov.SuiteRan(e); known {
				best = EvidenceSuite
				break
			}
		}
	}
	row.Evidence = best
	if lines, hit, overTests, ok := cov.EnforcerLines(enforcers, verifiers); ok {
		ec := &EnforcerCoverage{Lines: lines, Hit: hit, Percent: Percent(hit, lines), Over: "suite"}
		if overTests {
			ec.Over = "verifiers"
		}
		row.EnforcerCoverage = ec
	}

	if len(row.ExercisedBy) > 0 {
		if row.Status == StatusDemonstrated {
			row.Status = StatusExercised
			row.StatusReason = "verifier passed, and its per-test coverage executed an enforcer"
		}
		return
	}
	switch {
	case !cov.HasPerTest():
		row.CoverageReason = "no per-test coverage ingested"
	case len(missing)+len(perFileOnly) == 0:
		row.CoverageReason = "no verifier's per-test coverage executed an enforcer"
	case len(missing) > 0:
		row.CoverageReason = "no per-test coverage for " + strings.Join(missing, ", ")
		if len(perFileOnly) > 0 {
			row.CoverageReason += "; per_file only for " + strings.Join(perFileOnly, ", ") + ", which cannot promote"
		}
	default:
		row.CoverageReason = "per_file coverage only for " + strings.Join(perFileOnly, ", ") + ", which cannot promote"
	}
}

// exerciseScenario lifts a demonstrated scenario to exercised when a
// verifier's per-test run executed the handler of an entry point the
// scenario names, turning the declared TouchesEntryPoint into a checked fact.
func exerciseScenario(sr *ScenarioRow, testFQNs map[string]bool, handlers map[string]string, opts Options) {
	cov := opts.Coverage
	if cov == nil || len(sr.Verifiers) == 0 {
		return
	}
	best, ran := EvidenceNone, false
	for _, v := range sr.Verifiers {
		if !testFQNs[v] {
			continue
		}
		for _, ep := range sr.EntryPoints {
			h := handlers[ep]
			if h == "" {
				continue
			}
			executed, b := cov.TestRan(v, h)
			if stronger(b, best) {
				best = b
			}
			if executed && b == EvidencePerTest && !ran {
				ran = true
				sr.ExercisedBy = append(sr.ExercisedBy, v)
			}
		}
	}
	sr.Evidence = best
	switch {
	case ran:
		if sr.Status == StatusDemonstrated {
			sr.Status = StatusExercised
			sr.StatusReason = "verifier passed, and its per-test coverage executed the entry point's handler"
		}
	case len(sr.EntryPoints) == 0:
		sr.CoverageReason = "names no entry point whose handler a verifier could execute"
	case !cov.HasPerTest():
		sr.CoverageReason = "no per-test coverage ingested"
	case best == EvidencePerTest:
		sr.CoverageReason = "no verifier's per-test coverage executed the entry point's handler"
	default:
		sr.CoverageReason = "no per-test coverage for its verifiers"
	}
}

// stronger reports whether evidence basis a is stronger than b.
func stronger(a, b string) bool {
	rank := map[string]int{EvidencePerTest: 3, EvidencePerFile: 2, EvidenceSuite: 1, EvidenceNone: 0}
	return rank[a] > rank[b]
}

// Percent is hit/lines as a percentage rounded to one decimal; 100 for no lines.
func Percent(hit, lines int) float64 {
	if lines == 0 {
		return 100
	}
	return float64(int(1000*float64(hit)/float64(lines)+0.5)) / 10
}

// appendNew appends the entries of add not already in list.
func appendNew(list, add []string) []string {
	for _, s := range add {
		dup := false
		for _, have := range list {
			if have == s {
				dup = true
				break
			}
		}
		if !dup {
			list = append(list, s)
		}
	}
	return list
}

// applyOverlays folds the v0.8.1 cross-cutting state onto a computed
// status: the lite-profile ceiling (demonstrated caps to verified —
// lite's honest top is "wired") and open meaning flags (attached
// alongside the status, never replacing it).
// @enforces rtm:INV-5
func applyOverlays(status *Status, reason *string, flagged *bool, flagList *[]string, unit string, opts Options) {
	if opts.ProfileLite && status.IsDemonstrated() {
		*status = StatusVerified
		*reason = "lite profile: ceiling is wired — adopt the full profile to claim demonstrated"
	}
	if opts.FlagsOf != nil {
		if fl := opts.FlagsOf(unit); len(fl) > 0 {
			*flagged = true
			*flagList = fl
		}
	}
}

// resultStatus folds ingested results over a verifier set. decided is
// false when no result moves the needle (keep StatusVerified).
// @enforces rtm:INV-3
func resultStatus(verifiers []string, resultOf func(string) (bool, bool)) (Status, string, bool) {
	if resultOf == nil {
		return "", "", false
	}
	anyKnown, anyFail, allPass := false, false, true
	for _, fqn := range verifiers {
		passed, known := resultOf(fqn)
		if !known {
			allPass = false
			continue
		}
		anyKnown = true
		if !passed {
			anyFail = true
		}
	}
	switch {
	case anyFail:
		return StatusFailing, "an ingested result for a verifier is red", true
	case anyKnown && allPass:
		return StatusDemonstrated, "verifier passed on the generated commit", true
	default:
		return "", "", false
	}
}

// hasStructuralOrMutationOnly reports whether the invariant declares
// verifiable_by that does NOT include "test". In that case, the
// absence of a verifying test isn't unverified — the invariant is
// verified by structural check / mutation alone.
// @enforces rtm:INV-7
func hasStructuralOrMutationOnly(inv *schema.Invariant) bool {
	methods := inv.EffectiveVerifiableBy()
	for _, m := range methods {
		if m == schema.VerifiableByTest {
			return false
		}
	}
	return true
}

// worstFor returns the worst (lowest-severity) status across a BRD's
// rows. An empty BRD (no SCs) is treated as Unmapped.
// @enforces rtm:INV-6
func worstFor(s *BRDSummary) Status {
	if s.Total == 0 {
		return StatusUnmapped
	}
	if s.Phantom > 0 {
		return StatusPhantom
	}
	if s.Unenforced > 0 {
		return StatusUnenforced
	}
	if s.Failing > 0 {
		return StatusFailing
	}
	if s.Unverified > 0 {
		return StatusUnverified
	}
	if s.Partial > 0 {
		return StatusPartial
	}
	if s.Unmapped > 0 && s.Verified == 0 && s.Demonstrated == 0 {
		return StatusUnmapped
	}
	if s.Verified > 0 {
		return StatusVerified
	}
	if s.Exercised > 0 && s.Exercised == s.Demonstrated {
		return StatusExercised
	}
	return StatusDemonstrated
}

// splitInvariantRef parses `<feature.id>:<INV-N>`. Returns (feature,
// inv, true) on success; (input, "", false) on a bare ref or
// malformed string. The "INV-N" segment is liberal — anything after
// the colon counts as the invariant id.
func splitInvariantRef(ref string) (feature, inv string, ok bool) {
	i := strings.LastIndex(ref, ":")
	if i <= 0 || i == len(ref)-1 {
		return ref, "", false
	}
	return ref[:i], ref[i+1:], true
}

// resolveRef is the same disambiguation used in validate.resolveRef.
// A qualified ref (`feature:item`) splits cleanly; a bare ref is
// scoped to the fallback feature provided by the caller (the
// enforcer's own feature, the test's own feature).
func resolveRef(ref, fallback string) (feature, item string) {
	if i := strings.LastIndex(ref, ":"); i > 0 {
		return ref[:i], ref[i+1:]
	}
	return fallback, ref
}

// round4 trims to four decimals so JSON emission is byte-stable
// across runs — same contract as importer.ratio().
func round4(v float64) float64 {
	return float64(int(v*10000+0.5)) / 10000
}

// Coverage is the BRD-goal coverage ratio surfaced on /coverage as
// the v0.6 5th card. Computed once over the whole matrix.
type Coverage struct {
	TotalCriteria    int     `json:"total_criteria"`
	VerifiedCriteria int     `json:"verified_criteria"`
	Ratio            float64 `json:"ratio"`
}

// ComputeCoverage rolls the whole matrix into one number. VERIFIED
// (declared), DEMONSTRATED and EXERCISED criteria count as covered, so the
// v0.6 headline never regresses when a rung splits.
func ComputeCoverage(m Matrix) Coverage {
	c := Coverage{TotalCriteria: len(m.Rows)}
	for _, r := range m.Rows {
		if r.Status == StatusVerified || r.Status.IsDemonstrated() {
			c.VerifiedCriteria++
		}
	}
	if c.TotalCriteria > 0 {
		c.Ratio = round4(float64(c.VerifiedCriteria) / float64(c.TotalCriteria))
	}
	return c
}

// JourneyCoverage is the v0.8 β 6th Coverage card: the fraction of BRD
// user_scenarios whose verifier exercises a declared entry point. It is a
// far more honest headline than criterion coverage — it counts what a
// user can actually reach, not what an invariant asserts.
type JourneyCoverage struct {
	TotalScenarios   int     `json:"total_scenarios"`
	ReachedScenarios int     `json:"reached_scenarios"` // touch a declared entry point
	Demonstrated     int     `json:"demonstrated"`      // reached AND verifier passed
	Ratio            float64 `json:"ratio"`             // reached / total
}

// ComputeJourneyCoverage rolls the scenario rows into the journey number.
func ComputeJourneyCoverage(m Matrix) JourneyCoverage {
	jc := JourneyCoverage{TotalScenarios: len(m.Scenarios)}
	for _, s := range m.Scenarios {
		if s.TouchesEntryPoint {
			jc.ReachedScenarios++
			if s.Status.IsDemonstrated() {
				jc.Demonstrated++
			}
		}
	}
	if jc.TotalScenarios > 0 {
		jc.Ratio = round4(float64(jc.ReachedScenarios) / float64(jc.TotalScenarios))
	}
	return jc
}
