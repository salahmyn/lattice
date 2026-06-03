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

	// StatusUnmapped — SC has no `maps_to_invariant` at all. Common
	// during BRD drafting; info-level, not a failure.
	StatusUnmapped Status = "unmapped"

	// StatusPartial — enforcer + verifier both present, but mutation
	// score is below the per-invariant or default threshold.
	StatusPartial Status = "partial"

	// StatusVerified — invariant exists, enforcer + verifier both
	// present, mutation OK (or unknown). The happy path.
	StatusVerified Status = "verified"
)

// Severity returns 0 for the worst status and increases for healthier
// states. Used to roll up multiple rows to one summary status.
func (s Status) Severity() int {
	switch s {
	case StatusPhantom:
		return 0
	case StatusUnenforced:
		return 1
	case StatusUnverified:
		return 2
	case StatusUnmapped:
		return 3
	case StatusPartial:
		return 4
	case StatusVerified:
		return 5
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
}

// BRDSummary rolls up every Row for one BRD into a verification ratio.
type BRDSummary struct {
	BRDID            string  `json:"brd_id"`
	BRDTitle         string  `json:"brd_title,omitempty"`
	Total            int     `json:"total"`
	Verified         int     `json:"verified"`
	Partial          int     `json:"partial"`
	Unmapped         int     `json:"unmapped"`
	Unverified       int     `json:"unverified"`
	Unenforced       int     `json:"unenforced"`
	Phantom          int     `json:"phantom"`
	VerificationRate float64 `json:"verification_rate"` // verified / total
	WorstStatus      Status  `json:"worst_status"`
}

// Matrix is the full traceability output: every row + per-BRD
// summaries. Ordering is deterministic so re-runs are byte-stable.
type Matrix struct {
	Rows      []Row        `json:"rows"`
	Summaries []BRDSummary `json:"summaries"`
}

// Options tunes computation. MutationThreshold below sets the cutoff
// for PARTIAL vs VERIFIED — passed in by the caller so the same
// configured threshold drives validation and RTM display.
type Options struct {
	MutationThreshold float64
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
	for _, t := range kg.Tests {
		for _, ref := range t.Verifies {
			feature, inv := resolveRef(ref, t.Feature)
			k := invKey{feature, inv}
			verifiers[k] = append(verifiers[k], t.FQN)
		}
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
			}
			classify(&row, sc, featByID, enforcers, verifiers, mutation, mutationOK, opts)
			rows = append(rows, row)

			sum.Total++
			switch row.Status {
			case StatusVerified:
				sum.Verified++
			case StatusPartial:
				sum.Partial++
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
	}

	// Finalize summaries: ratio + worst-status roll-up.
	summaryList := make([]BRDSummary, 0, len(summaries))
	for _, s := range summaries {
		if s.Total > 0 {
			s.VerificationRate = round4(float64(s.Verified) / float64(s.Total))
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

	if rows == nil {
		rows = []Row{}
	}
	return Matrix{Rows: rows, Summaries: summaryList}
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
	row.Status = StatusVerified
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
	if s.Unverified > 0 {
		return StatusUnverified
	}
	if s.Partial > 0 {
		return StatusPartial
	}
	if s.Unmapped > 0 && s.Verified == 0 {
		return StatusUnmapped
	}
	return StatusVerified
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

// ComputeCoverage rolls the whole matrix into one number.
func ComputeCoverage(m Matrix) Coverage {
	c := Coverage{TotalCriteria: len(m.Rows)}
	for _, r := range m.Rows {
		if r.Status == StatusVerified {
			c.VerifiedCriteria++
		}
	}
	if c.TotalCriteria > 0 {
		c.Ratio = round4(float64(c.VerifiedCriteria) / float64(c.TotalCriteria))
	}
	return c
}
