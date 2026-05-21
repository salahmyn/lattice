package analyze

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// FixtureResult is the outcome of evaluating one calibration fixture.
type FixtureResult struct {
	Name     string   `json:"name"`
	Expected []string `json:"expected"`
	Produced []string `json:"produced"`
	TruePos  int      `json:"true_positives"`
	FalsePos int      `json:"false_positives"`
	FalseNeg int      `json:"false_negatives"`
}

// EvalResult aggregates a calibration run.
type EvalResult struct {
	Fixtures  []FixtureResult `json:"fixtures"`
	Precision float64         `json:"precision"`
	Recall    float64         `json:"recall"`
}

// Eval runs the calibration harness over a baseline directory. Each immediate
// subdirectory is a fixture containing proposal.yaml and expected.json (a JSON
// array of expected finding codes).
func (a *Analyzer) Eval(ctx context.Context, baselineDir string) (EvalResult, error) {
	entries, err := os.ReadDir(baselineDir)
	if err != nil {
		return EvalResult{}, err
	}

	var result EvalResult
	var totalTP, totalFP, totalFN int

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(baselineDir, e.Name())
		expected, err := readExpected(filepath.Join(dir, "expected.json"))
		if err != nil {
			continue
		}
		report, err := a.AnalyzeProposal(ctx, filepath.Join(dir, "proposal.yaml"))
		if err != nil {
			continue
		}

		produced := findingCodes(report)
		fr := FixtureResult{Name: e.Name(), Expected: expected, Produced: produced}
		expSet := toSet(expected)
		prodSet := toSet(produced)
		for code := range prodSet {
			if expSet[code] {
				fr.TruePos++
			} else {
				fr.FalsePos++
			}
		}
		for code := range expSet {
			if !prodSet[code] {
				fr.FalseNeg++
			}
		}
		totalTP += fr.TruePos
		totalFP += fr.FalsePos
		totalFN += fr.FalseNeg
		result.Fixtures = append(result.Fixtures, fr)
	}

	if totalTP+totalFP > 0 {
		result.Precision = round2(float64(totalTP) / float64(totalTP+totalFP))
	}
	if totalTP+totalFN > 0 {
		result.Recall = round2(float64(totalTP) / float64(totalTP+totalFN))
	}
	return result, nil
}

// findingCodes returns the distinct non-OK finding codes of a report.
func findingCodes(r ImpactReport) []string {
	set := map[string]bool{}
	for _, f := range append(r.DeterministicFindings, r.SemanticFindings...) {
		if f.Level != LevelOK {
			set[f.Code] = true
		}
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

func readExpected(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var codes []string
	if err := json.Unmarshal(data, &codes); err != nil {
		return nil, err
	}
	sort.Strings(codes)
	return codes, nil
}

func toSet(s []string) map[string]bool {
	m := make(map[string]bool, len(s))
	for _, x := range s {
		m[x] = true
	}
	return m
}
