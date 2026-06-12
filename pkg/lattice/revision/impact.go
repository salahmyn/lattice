package revision

import (
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/lease"
	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// ComputeImpact walks the forward blast radius of a CR's targets:
// criterion → invariants → enforcer symbols → verifier tests →
// scenarios → entry points. The decision gate prices the change before
// committing to it — this report IS the scope, computed from the
// graph, never guessed.
//
// Targets are BRD ids ("brd.x.y" — every criterion) or criterion refs
// ("brd.x.y/SC-1").
func ComputeImpact(m rtm.Matrix, kg schema.KnowledgeGraph, leases []lease.Lease, targets []string) schema.RevisionImpact {
	wantBRD := map[string]bool{}
	wantSC := map[string]bool{}
	for _, t := range targets {
		if i := strings.Index(t, "/"); i > 0 {
			wantSC[t] = true
		} else {
			wantBRD[t] = true
		}
	}
	hit := func(brdID, scID string) bool {
		return wantBRD[brdID] || wantSC[brdID+"/"+scID]
	}

	imp := schema.RevisionImpact{}
	features := map[string]bool{}
	addAll := func(dst *[]string, src []string) {
		*dst = append(*dst, src...)
	}

	for _, r := range m.Rows {
		if !hit(r.BRDID, r.CriterionID) {
			continue
		}
		if r.InvariantID != "" {
			imp.AffectedInvariants = append(imp.AffectedInvariants, r.FeatureID+":"+r.InvariantID)
		}
		addAll(&imp.AffectedSymbols, r.Enforcers)
		addAll(&imp.AffectedTests, r.Verifiers)
		if r.FeatureID != "" {
			features[r.FeatureID] = true
		}
		if t := r.Tier; t > imp.MaxTier {
			imp.MaxTier = t
		}
	}

	// Scenarios of a targeted BRD share its intent surface.
	for _, s := range m.Scenarios {
		if wantBRD[s.BRDID] || anyPrefixed(wantSC, s.BRDID+"/") {
			imp.AffectedScenarios = append(imp.AffectedScenarios, s.BRDID+"/"+s.ScenarioID)
			addAll(&imp.AffectedTests, s.Verifiers)
		}
	}

	// Entry points whose flow reaches an affected feature.
	for _, ep := range kg.EntryPoints {
		for _, step := range ep.Flow {
			if features[step.Feature] {
				imp.AffectedEntryPoints = append(imp.AffectedEntryPoints, ep.ID)
				break
			}
		}
	}

	// In-flight conflicts: an active lease on an affected feature or
	// targeted BRD means the radius intersects work being built right
	// now — the decision must hold the CR or attach it to that work.
	for _, l := range leases {
		unit := l.Unit
		conflict := features[unit] || wantBRD[unit]
		if !conflict {
			for f := range features {
				if strings.HasPrefix(unit, f) || strings.HasPrefix(f, unit) {
					conflict = true
					break
				}
			}
		}
		if conflict {
			imp.InFlightConflicts = append(imp.InFlightConflicts,
				unit+" (leased by "+l.Actor+")")
		}
	}

	dedupeSorted(&imp.AffectedInvariants)
	dedupeSorted(&imp.AffectedSymbols)
	dedupeSorted(&imp.AffectedTests)
	dedupeSorted(&imp.AffectedScenarios)
	dedupeSorted(&imp.AffectedEntryPoints)
	dedupeSorted(&imp.InFlightConflicts)
	return imp
}

// SpawnItems derives the CR-4 follow-ups from a priced impact. A
// widening (or wording) change spawns work items — re-verify each
// demoted unit against the revised text. A narrowing spawns retirement
// items — the exact tests and symbols that may legally be removed,
// each traceable back to this CR.
func SpawnItems(class schema.RevisionClass, imp schema.RevisionImpact, demotions []string) (work, retirement []string) {
	switch class {
	case schema.RevisionNarrowing:
		retirement = append(retirement, imp.AffectedTests...)
		retirement = append(retirement, imp.AffectedSymbols...)
	default:
		for _, d := range demotions {
			work = append(work, "re-verify "+d+" against the revised text")
		}
	}
	return work, retirement
}

func anyPrefixed(set map[string]bool, prefix string) bool {
	for k := range set {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

func dedupeSorted(s *[]string) {
	seen := map[string]bool{}
	out := (*s)[:0]
	for _, x := range *s {
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	*s = out
}
