// Package sweep implements the forbidden-move sweep (v0.8.1): the
// check that a verifier which existed at the last baseline has not
// silently disappeared. Deleting or skipping a test is legal ONLY
// against a retirement item from an approved change request — that is
// what makes legitimate descoping distinguishable from gaming the
// suite.
package sweep

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Baseline is the recorded verifier inventory at a known-good point.
type Baseline struct {
	Commit    string   `json:"commit,omitempty"`
	Verifiers []string `json:"verifiers"`
}

const file = ".cache/sweep/baseline.json"

// Inventory lists every verifier test FQN on the graph: tests carrying
// a @verifies edge, plus tests named by a BRD scenario's verified_by.
func Inventory(kg schema.KnowledgeGraph) []string {
	set := map[string]bool{}
	for _, t := range kg.Tests {
		if len(t.Verifies) > 0 {
			set[t.FQN] = true
		}
	}
	testFQNs := map[string]bool{}
	for _, t := range kg.Tests {
		testFQNs[t.FQN] = true
	}
	for _, b := range kg.BRDs {
		for _, us := range b.UserScenarios {
			for _, ref := range us.VerifiedBy {
				if testFQNs[ref] {
					set[ref] = true
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for fqn := range set {
		out = append(out, fqn)
	}
	sort.Strings(out)
	return out
}

// Load reads the recorded baseline. ok is false when none exists yet.
func Load(latticeDir string) (Baseline, bool) {
	data, err := os.ReadFile(filepath.Join(latticeDir, file))
	if err != nil {
		return Baseline{}, false
	}
	var b Baseline
	if json.Unmarshal(data, &b) != nil {
		return Baseline{}, false
	}
	return b, true
}

// Save records the baseline.
func Save(latticeDir string, b Baseline) error {
	p := filepath.Join(latticeDir, file)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	sort.Strings(b.Verifiers)
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

// Disappeared returns baseline verifiers absent from the current
// inventory — the candidates for TEST_RETIRED_ILLEGALLY.
func Disappeared(baseline Baseline, current []string) []string {
	have := map[string]bool{}
	for _, fqn := range current {
		have[fqn] = true
	}
	var gone []string
	for _, fqn := range baseline.Verifiers {
		if !have[fqn] {
			gone = append(gone, fqn)
		}
	}
	return gone
}
