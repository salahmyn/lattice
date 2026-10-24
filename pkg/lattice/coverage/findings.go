package coverage

// @module-feature coverage

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// unit is one invariant, or one direct-wired criterion, with its enforcers
// and verifiers.
type unit struct {
	ref       string // feature:INV-N or brd.<id>:SC-N
	feature   string
	invariant string
	enforcers []string
	verifiers []string
	loc       *schema.Location
}

func units(kg schema.KnowledgeGraph) []unit {
	byRef := map[string]*unit{}
	get := func(ref string) *unit {
		if u, ok := byRef[ref]; ok {
			return u
		}
		feature, inv := splitRef(ref, "")
		u := &unit{ref: ref, feature: feature, invariant: inv}
		byRef[ref] = u
		return u
	}
	for _, f := range kg.Features {
		for _, inv := range f.Invariants {
			get(f.ID + ":" + inv.ID)
		}
	}
	for _, s := range kg.Symbols {
		for _, ref := range s.EnforcesInvariants {
			f, inv := splitRef(ref, s.Feature)
			u := get(f + ":" + inv)
			u.enforcers = appendUnique(u.enforcers, s.FQN)
			if u.loc == nil {
				u.loc = &schema.Location{File: s.File, Line: s.Line}
			}
		}
	}
	for _, t := range kg.Tests {
		for _, ref := range t.Verifies {
			f, inv := splitRef(ref, t.Feature)
			u := get(f + ":" + inv)
			u.verifiers = appendUnique(u.verifiers, t.FQN)
		}
	}
	for _, b := range kg.BRDs {
		for _, sc := range b.SuccessCriteria {
			if !sc.DirectWire {
				continue
			}
			u := get(b.ID + ":" + sc.ID)
			u.enforcers, u.verifiers = sc.EnforcedBy, sc.VerifiedBy
			u.loc = &schema.Location{File: b.SourcePath}
		}
	}
	out := make([]unit, 0, len(byRef))
	for _, u := range byRef {
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ref < out[j].ref })
	return out
}

func splitRef(ref, fallback string) (string, string) {
	if i := strings.LastIndex(ref, ":"); i > 0 {
		return ref[:i], ref[i+1:]
	}
	return fallback, ref
}

func appendUnique(list []string, s string) []string {
	for _, have := range list {
		if have == s {
			return list
		}
	}
	return append(list, s)
}

// basisRank orders evidence bases, strongest first.
var basisRank = map[string]int{BasisPerTest: 3, BasisPerFile: 2, BasisSuite: 1}

// Findings raises the coverage findings for an ingested set. With no set
// (ix nil) it raises none: no data is never a pass, and never a finding.
// Severities are the codes' severities once enabled; validate's policy
// turns them off unless the workspace has a coverage: block.
// @enforces coverage:INV-8
func Findings(kg schema.KnowledgeGraph, ix *Index, cfg config.Coverage) []schema.Violation {
	if ix == nil {
		return nil
	}
	var v []schema.Violation
	v = append(v, stale(kg, ix)...)
	us := units(kg)
	for _, u := range us {
		if len(u.enforcers) == 0 || len(u.verifiers) == 0 {
			continue // UNENFORCED_INVARIANT / UNVERIFIED_INVARIANT own these
		}
		v = append(v, missesEnforcer(u, ix)...)
		v = append(v, unproven(u, ix)...)
	}
	v = append(v, floors(kg, ix, cfg.Floors, us)...)
	return v
}

// missesEnforcer is VERIFIER_MISSES_ENFORCER: every verifier has evidence
// of its own run (per-test, per-file) or the suite shows no enforcer ran at
// all, and none executed an enforcer. A verifier with no evidence makes the
// answer unknown, which is never a pass and never this finding.
// @enforces coverage:INV-5
func missesEnforcer(u unit, ix *Index) []schema.Violation {
	suiteKnown, suiteRan := true, false
	for _, e := range u.enforcers {
		ran, known := ix.SuiteRan(e)
		suiteKnown = suiteKnown && known
		suiteRan = suiteRan || ran
	}
	weakest := BasisPerTest
	for _, t := range u.verifiers {
		basis := "none"
		for _, e := range u.enforcers {
			ran, b := ix.TestRan(t, e)
			if ran {
				return nil // this verifier ran an enforcer
			}
			basis = b
		}
		if basis == "none" {
			if !suiteKnown || suiteRan {
				return nil // no evidence for this verifier: unknown
			}
			basis = BasisSuite
		}
		if basisRank[basis] < basisRank[weakest] {
			weakest = basis
		}
	}
	what := map[string]string{
		BasisPerTest: "each verifier's own run",
		BasisPerFile: "a run of each verifier's test file",
		BasisSuite:   "the whole suite, in which no test ran it",
	}[weakest]
	return []schema.Violation{{
		Code: schema.CodeVerifierMissesEnforcer, Severity: schema.SeverityWarning,
		Subject: u.ref, FeatureID: u.feature, InvariantID: u.invariant,
		Message: fmt.Sprintf("none of the %d verifier(s) of %s executed its enforcer %s (evidence: %s, from %s): the test can pass without the guard running",
			len(u.verifiers), u.ref, strings.Join(u.enforcers, ", "), weakest, what),
		Location: u.loc,
		NextAction: &schema.NextAction{Kind: "add_verification",
			Detail: "call the enforcer from the verifier (no stub in its place), or tag the test that does"},
	}}
}

// unproven is VERIFIER_UNPROVEN: once any per-test coverage was ingested, a
// verifier with none of its own is named, so the gap is visible.
// @enforces coverage:INV-5
func unproven(u unit, ix *Index) []schema.Violation {
	if !ix.HasPerTest() {
		return nil
	}
	lacking := ix.lackingPerTest(u.verifiers)
	if len(lacking) == 0 {
		return nil
	}
	basis := "none"
	for _, t := range lacking {
		for _, e := range u.enforcers {
			if _, b := ix.TestRan(t, e); b == BasisPerFile {
				basis = BasisPerFile
			}
		}
	}
	if basis == "none" {
		for _, e := range u.enforcers {
			if _, known := ix.SuiteRan(e); known {
				basis = BasisSuite
			}
		}
	}
	return []schema.Violation{{
		Code: schema.CodeVerifierUnproven, Severity: schema.SeverityInfo,
		Subject: u.ref, FeatureID: u.feature, InvariantID: u.invariant,
		Message: fmt.Sprintf("%s: %d of %d verifier(s) have no per-test coverage (%s), so whether they execute the enforcer is unknown (evidence: %s at best)",
			u.ref, len(lacking), len(u.verifiers), strings.Join(lacking, ", "), basis),
		Location: u.loc,
		NextAction: &schema.NextAction{Kind: "run_command",
			Command: []string{"lattice", "results", "tests", "--verifiers"},
			Detail:  "collect a coverage report per verifier test and ingest it with `lattice results ingest --per-test <dir>`"},
	}}
}

// stale is COVERAGE_STALE: the coverage is of another commit than the graph,
// or the tree had uncommitted changes when it was ingested.
// @enforces coverage:INV-6
func stale(kg schema.KnowledgeGraph, ix *Index) []schema.Violation {
	set := ix.Set
	var why []string
	if set.Commit != kg.GeneratedFromCommit {
		why = append(why, fmt.Sprintf("it covers commit %s, the graph is at %s", short(set.Commit), short(kg.GeneratedFromCommit)))
	}
	if set.Dirty {
		d := "the working tree had uncommitted changes when it was ingested"
		if len(set.DirtyFiles) > 0 {
			d += " (" + strings.Join(set.DirtyFiles, ", ") + ")"
		}
		why = append(why, d)
	}
	if len(why) == 0 {
		return nil
	}
	return []schema.Violation{{
		Code: schema.CodeCoverageStale, Severity: schema.SeverityWarning,
		Subject:  "coverage",
		Message:  "the ingested coverage is stale: " + strings.Join(why, "; ") + " (evidence: ingest stamp)",
		Location: &schema.Location{File: cacheFile},
		NextAction: &schema.NextAction{Kind: "run_command",
			Command: []string{"lattice", "results", "ingest"},
			Detail:  "rerun the tests on a clean checkout of this commit and ingest their reports"},
	}}
}

func short(c string) string {
	switch {
	case c == "":
		return "(none)"
	case len(c) > 8:
		return c[:8]
	}
	return c
}

// runtimeKinds are the symbol kinds that hold runtime code.
var runtimeKinds = map[string]bool{"function": true, "method": true, "class": true, "const": true, "trait": true}

// floors is COVERAGE_BELOW_FLOOR for each floor the workspace declares.
// @enforces coverage:INV-7
func floors(kg schema.KnowledgeGraph, ix *Index, f config.CoverageFloors, us []unit) []schema.Violation {
	var v []schema.Violation
	below := func(subject, feature, inv, msg string, loc *schema.Location) {
		v = append(v, schema.Violation{
			Code: schema.CodeCoverageBelowFloor, Severity: schema.SeverityWarning,
			Subject: subject, FeatureID: feature, InvariantID: inv, Message: msg, Location: loc,
			NextAction: &schema.NextAction{Kind: "add_verification",
				Detail: "add tests that run the uncovered lines, or lower the floor in config.yaml coverage.floors"},
		})
	}

	if f.Feature > 0 && len(ix.Set.Symbols) > 0 {
		encloses := map[string]bool{}
		for _, s := range kg.Symbols {
			if s.EnclosingFQN != "" {
				encloses[s.EnclosingFQN] = true
			}
		}
		type tally struct{ lines, hit int }
		byFeature := map[string]*tally{}
		for _, s := range kg.Symbols {
			if s.Feature == "" || !runtimeKinds[s.Kind] || encloses[s.FQN] {
				continue
			}
			c, ok := ix.Set.Symbols[s.FQN]
			if !ok {
				continue
			}
			t := byFeature[s.Feature]
			if t == nil {
				t = &tally{}
				byFeature[s.Feature] = t
			}
			t.lines += c.Lines
			t.hit += c.Hit
		}
		loc := map[string]*schema.Location{}
		for _, m := range kg.Features {
			loc[m.ID] = &schema.Location{File: m.SourcePath}
		}
		ids := make([]string, 0, len(byFeature))
		for id := range byFeature {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			t := byFeature[id]
			if t.lines == 0 {
				continue
			}
			if p := rtm.Percent(t.hit, t.lines); p < f.Feature {
				below(id, id, "", fmt.Sprintf("feature %s: the suite ran %.1f%% of its body lines (%d/%d), below the feature floor of %s%% (evidence: suite)",
					id, p, t.hit, t.lines, trimFloat(f.Feature)), loc[id])
			}
		}
	}

	// Invariant coverage, for the invariant floor and the tier floors.
	type measured struct {
		pct       float64
		lines     int
		hit       int
		overTests bool
	}
	cov := map[string]measured{}
	for _, u := range us {
		if len(u.enforcers) == 0 {
			continue
		}
		lines, hit, overTests, ok := ix.EnforcerLines(u.enforcers, u.verifiers)
		if !ok {
			continue
		}
		m := measured{rtm.Percent(hit, lines), lines, hit, overTests}
		cov[u.ref] = m
		if f.Invariant > 0 && m.pct < f.Invariant && !strings.HasPrefix(u.ref, "brd.") {
			below(u.ref, u.feature, u.invariant, fmt.Sprintf("%s: %.1f%% of its enforcer lines ran (%d/%d) over %s, below the invariant floor of %s%% (evidence: %s)",
				u.ref, m.pct, hit, lines, overWhat(overTests), trimFloat(f.Invariant), basisOf(overTests)), u.loc)
		}
	}

	if len(f.Tier) > 0 {
		for _, b := range kg.BRDs {
			for _, sc := range b.SuccessCriteria {
				tier := sc.EffectiveTier()
				floor := f.Tier[tier]
				if floor <= 0 {
					continue
				}
				refs := []string(sc.MapsToInvariant)
				if sc.DirectWire {
					refs = []string{b.ID + ":" + sc.ID}
				}
				worst, worstRef, found := measured{pct: 101}, "", false
				for _, ref := range refs {
					if m, ok := cov[ref]; ok && m.pct < worst.pct {
						worst, worstRef, found = m, ref, true
					}
				}
				if !found || worst.pct >= floor {
					continue
				}
				below(b.ID+":"+sc.ID, "", "", fmt.Sprintf("BRD %q %s (tier %d): %.1f%% of the enforcer lines of %s, its weakest invariant, ran (%d/%d) over %s, below the tier %d floor of %s%% (evidence: %s)",
					b.ID, sc.ID, tier, worst.pct, worstRef, worst.hit, worst.lines, overWhat(worst.overTests), tier, trimFloat(floor), basisOf(worst.overTests)),
					&schema.Location{File: b.SourcePath})
			}
		}
	}
	return v
}

func overWhat(overTests bool) string {
	if overTests {
		return "its verifiers' per-test runs"
	}
	return "the whole suite (a verifier has no per-test coverage)"
}

func basisOf(overTests bool) string {
	if overTests {
		return BasisPerTest
	}
	return BasisSuite
}

func trimFloat(f float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.1f", f), "0"), ".")
}
