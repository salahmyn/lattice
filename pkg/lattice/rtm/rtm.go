// Package rtm builds the Requirements Traceability Matrix: the walk
// from a BRD's success_criteria down to the invariants, enforcers,
// verifiers, and mutation scores that back them.
//
// The matrix is computed live from the KnowledgeGraph — no schema
// changes, no extra files on disk. Status per criterion is a pure
// function of the graph, so the same answer is reachable from CLI,
// UI, MCP, and validation passes.
package rtm

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
	// generated commit (v0.8 γ). The top of the ladder.
	StatusDemonstrated Status = "demonstrated"
)

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
	}
	return 0
}

// Row is one walked SC: BRD → SC → invariant → enforcers + verifiers.
// One row per success_criterion. UnmappedReason is filled when the
// criterion has no maps_to_invariant; PhantomRef holds the bad ref
// when the lookup misses.
type Row struct {
	BRDID         string  `json:"brd_id"`
	BRDTitle      string  `json:"brd_title,omitempty"`
	CriterionID   string  `json:"criterion_id"`
	Statement     string  `json:"statement"`
	MapsTo        string  `json:"maps_to,omitempty"`
	FeatureID     string  `json:"feature_id,omitempty"`
	InvariantID   string  `json:"invariant_id,omitempty"`
	Invariant     string  `json:"invariant,omitempty"`
	Enforcers     []string `json:"enforcers,omitempty"`  // symbol FQNs
	Verifiers     []string `json:"verifiers,omitempty"`  // test FQNs
	MutationScore float64 `json:"mutation_score,omitempty"`
	HasMutation   bool    `json:"has_mutation,omitempty"`
	Status        Status  `json:"status"`
	StatusReason  string  `json:"status_reason,omitempty"`

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
}

// BRDSummary rolls up every Row for one BRD into a verification ratio.
type BRDSummary struct {
	BRDID            string  `json:"brd_id"`
	BRDTitle         string  `json:"brd_title,omitempty"`
	Total            int     `json:"total"`
	Demonstrated     int     `json:"demonstrated"`
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
	for _, ep := range kg.EntryPoints {
		epIDs[ep.ID] = true
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
				MapsTo:      sc.MapsToInvariant,
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
			applyOverlays(&sr.Status, &sr.StatusReason, &sr.Flagged, &sr.Flags,
				b.ID+"/"+us.ID, opts)
			scenarios = append(scenarios, sr)
			sum.ScenarioTotal++
			if sr.Status == StatusDemonstrated {
				sum.ScenarioDemonstrated++
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
		}
		return
	}

	if strings.TrimSpace(sc.MapsToInvariant) == "" {
		row.Status = StatusUnmapped
		row.StatusReason = "success criterion has no `maps_to_invariant`"
		return
	}

	feature, inv, ok := splitInvariantRef(sc.MapsToInvariant)
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
		return
	}
	row.Status = StatusVerified
}

// applyOverlays folds the v0.8.1 cross-cutting state onto a computed
// status: the lite-profile ceiling (demonstrated caps to verified —
// lite's honest top is "wired") and open meaning flags (attached
// alongside the status, never replacing it).
func applyOverlays(status *Status, reason *string, flagged *bool, flagList *[]string, unit string, opts Options) {
	if opts.ProfileLite && *status == StatusDemonstrated {
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

// ComputeCoverage rolls the whole matrix into one number. Both VERIFIED
// (declared) and DEMONSTRATED criteria count as covered, so the v0.6
// headline never regresses when v0.8 splits the rung.
func ComputeCoverage(m Matrix) Coverage {
	c := Coverage{TotalCriteria: len(m.Rows)}
	for _, r := range m.Rows {
		if r.Status == StatusVerified || r.Status == StatusDemonstrated {
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
			if s.Status == StatusDemonstrated {
				jc.Demonstrated++
			}
		}
	}
	if jc.TotalScenarios > 0 {
		jc.Ratio = round4(float64(jc.ReachedScenarios) / float64(jc.TotalScenarios))
	}
	return jc
}
