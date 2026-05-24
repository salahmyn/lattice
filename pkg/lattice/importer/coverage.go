package importer

import (
	"math"
	"sort"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Coverage reports how much of the codebase the import has adopted — the
// brownfield analogue of test coverage, but for meaning.
//
// Stage 1 produces only the discovery ratio; the documentation and
// verification ratios are filled in by later stages.
type Coverage struct {
	Discovery DiscoveryCoverage `json:"discovery"`
}

// DiscoveryCoverage is the share of production symbols that Stage 1 placed
// into a feature candidate: how much code Lattice has even looked at.
type DiscoveryCoverage struct {
	TotalSymbols     int               `json:"total_symbols"`
	ClusteredSymbols int               `json:"clustered_symbols"`
	Ratio            float64           `json:"ratio"`
	ByPackage        []PackageCoverage `json:"by_package"`
}

// DocumentationCoverage is the share of production symbols that belong to a
// candidate the reviewer has accepted: how much code a real manifest now
// describes. It changes as the review loop progresses, so it is computed
// live rather than stored in candidates.json.
type DocumentationCoverage struct {
	TotalSymbols      int     `json:"total_symbols"`
	DocumentedSymbols int     `json:"documented_symbols"`
	Ratio             float64 `json:"ratio"`
}

// ComputeDocumentation measures documentation coverage from the candidate set
// and the session's accept/reject decisions.
func ComputeDocumentation(cf CandidatesFile, decisions map[string]string) DocumentationCoverage {
	total := cf.Coverage.Discovery.TotalSymbols
	documented := 0
	for _, c := range cf.Candidates {
		if decisions[c.ID] == DecisionAccepted {
			documented += len(c.Symbols)
		}
	}
	return DocumentationCoverage{
		TotalSymbols:      total,
		DocumentedSymbols: documented,
		Ratio:             ratio(documented, total),
	}
}

// VerificationCoverage is the share of declared invariants that have both an
// enforcer and a verifier: how much of the recorded meaning is fact-checked.
// It is v0.1's UNENFORCED/UNVERIFIED accounting surfaced as a ratio.
type VerificationCoverage struct {
	TotalInvariants    int     `json:"total_invariants"`
	VerifiedInvariants int     `json:"verified_invariants"`
	Ratio              float64 `json:"ratio"`
}

// ComputeVerification measures verification coverage from the feature
// manifests and the violations the validation engine reported. An invariant
// counts as verified when no UNENFORCED/UNVERIFIED violation names it.
func ComputeVerification(features []schema.Manifest, violations []schema.Violation) VerificationCoverage {
	total := 0
	for _, f := range features {
		total += len(f.Invariants)
	}
	flagged := map[string]bool{}
	for _, v := range violations {
		if v.Code != schema.CodeUnenforcedInvariant && v.Code != schema.CodeUnverifiedInvariant {
			continue
		}
		if v.InvariantID != "" {
			flagged[v.FeatureID+":"+v.InvariantID] = true
		}
	}
	verified := total - len(flagged)
	if verified < 0 {
		verified = 0
	}
	return VerificationCoverage{
		TotalInvariants:    total,
		VerifiedInvariants: verified,
		Ratio:              ratio(verified, total),
	}
}

// BRDCoverage is the share of features that point at an *approved* BRD
// — the v0.5.0 business-intent ratio. Features with no BRD count as
// uncovered; features attached to a draft/proposed BRD also count as
// uncovered, because the adoption story is "business has signed off".
type BRDCoverage struct {
	TotalFeatures    int     `json:"total_features"`
	CoveredFeatures  int     `json:"covered_features"`
	Ratio            float64 `json:"ratio"`
	TotalBRDs        int     `json:"total_brds"`
	ApprovedBRDs     int     `json:"approved_brds"`
}

// ComputeBRD measures BRD coverage from features and BRDs. A feature
// counts as covered when it either declares `implements_brd: <id>`
// pointing at an approved BRD, or appears in an approved BRD's
// `implements_via` list.
func ComputeBRD(features []schema.Manifest, brds []schema.BRD) BRDCoverage {
	approvedByID := map[string]bool{}
	approvedCount := 0
	for _, b := range brds {
		if b.Status == schema.BRDApproved {
			approvedByID[b.ID] = true
			approvedCount++
		}
	}
	// Build the reverse map: every feature id mentioned in an approved
	// BRD's implements_via list counts as covered, even without the
	// feature-side back-link.
	implicitCovered := map[string]bool{}
	for _, b := range brds {
		if !approvedByID[b.ID] {
			continue
		}
		for _, fid := range b.ImplementsVia {
			implicitCovered[fid] = true
		}
	}

	total := len(features)
	covered := 0
	for _, f := range features {
		if approvedByID[f.ImplementsBRD] || implicitCovered[f.ID] {
			covered++
		}
	}
	return BRDCoverage{
		TotalFeatures:   total,
		CoveredFeatures: covered,
		Ratio:           ratio(covered, total),
		TotalBRDs:       len(brds),
		ApprovedBRDs:    approvedCount,
	}
}

// PackageCoverage is the discovery coverage of one source directory.
type PackageCoverage struct {
	Package          string  `json:"package"`
	TotalSymbols     int     `json:"total_symbols"`
	ClusteredSymbols int     `json:"clustered_symbols"`
	Ratio            float64 `json:"ratio"`
}

// computeCoverage builds the discovery coverage from per-directory symbol
// totals and the set of directories that became candidates.
func computeCoverage(pkgTotals map[string]int, clustered map[string]bool) Coverage {
	dirs := make([]string, 0, len(pkgTotals))
	for d := range pkgTotals {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	total, clusteredTotal := 0, 0
	byPkg := make([]PackageCoverage, 0, len(dirs))
	for _, d := range dirs {
		n := pkgTotals[d]
		c := 0
		if clustered[d] {
			c = n
		}
		total += n
		clusteredTotal += c
		byPkg = append(byPkg, PackageCoverage{
			Package:          d,
			TotalSymbols:     n,
			ClusteredSymbols: c,
			Ratio:            ratio(c, n),
		})
	}

	return Coverage{Discovery: DiscoveryCoverage{
		TotalSymbols:     total,
		ClusteredSymbols: clusteredTotal,
		Ratio:            ratio(clusteredTotal, total),
		ByPackage:        byPkg,
	}}
}

// ratio divides num by den, rounded to four decimals for byte-stable output.
// An empty denominator yields 0.
func ratio(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return math.Round(float64(num)/float64(den)*10000) / 10000
}
