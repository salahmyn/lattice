package audit

// @module-feature audit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Case coverage, computed per acceptance case of an invariant (rnd/0010 §B).
// Only covered is positive. Unknown evidence is never covered.
const (
	// CaseCovered: a verifier tagged with the case passed on ingested
	// results, and breaking the case's enforcer made that verifier fail.
	CaseCovered = "covered"
	// CasePartial: a tagged verifier passed, but there is no break evidence
	// for it, or the break left it green.
	CasePartial = "partial"
	// CaseFailing: a tagged verifier's ingested result is red.
	CaseFailing = "failing"
	// CaseUncovered: no verifier is tagged with the case, or none has a result.
	CaseUncovered = "uncovered"
)

// CaseRow is one acceptance case of a promise and its computed coverage.
type CaseRow struct {
	ID string `json:"id"`
	// Label is how the case is displayed: "1a" for case a of INV-1.
	Label     string `json:"label"`
	Kind      string `json:"kind"`
	Statement string `json:"statement"`
	// Enforcer is the case's own enforcer, when it names one. Its coverage
	// reads the breaks of that symbol; without it, the invariant's.
	Enforcer string `json:"enforcer,omitempty"`
	Retired  bool   `json:"retired,omitempty"`
	Coverage string `json:"coverage"`
	Reason   string `json:"reason"`
	// Verifiers are the tests tagged @verifies <promise>#<id>.
	Verifiers []string `json:"verifiers"`
}

// Line is the case on one line: label, coverage, kind and statement, and
// with reason, why a case that is not covered reads as it does.
func (c CaseRow) Line(reason bool) string {
	s := fmt.Sprintf("%-4s %-9s %-8s %s", c.Label, c.Coverage, c.Kind, c.Statement)
	if c.Retired {
		s += " (retired)"
	}
	if reason && c.Coverage != CaseCovered && c.Reason != "" {
		s += " [" + c.Reason + "]"
	}
	return s
}

// caseVerifiers indexes the case-tagged verifiers of the graph:
// promise -> case id -> test FQNs, sorted. A bare ref resolves to the
// test's own feature, as every other verifier ref does.
func caseVerifiers(kg schema.KnowledgeGraph) map[string]map[string][]string {
	out := map[string]map[string][]string{}
	for _, t := range kg.Tests {
		for _, ref := range t.VerifiesCases {
			base, id, _ := schema.SplitCaseRef(ref)
			if !strings.Contains(base, ":") {
				base = t.Feature + ":" + base
			}
			if out[base] == nil {
				out[base] = map[string][]string{}
			}
			out[base][id] = appendOnce(out[base][id], t.FQN)
		}
	}
	for _, byCase := range out {
		for id := range byCase {
			sort.Strings(byCase[id])
		}
	}
	return out
}

func appendOnce(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// caseRows computes the coverage of every case of one invariant from the
// ingested results and the promise's usable break runs. enforcers are the
// invariant's enforcers, used for a case that names none of its own.
// @enforces audit:INV-8
func caseRows(promise string, inv schema.Invariant, tagged map[string][]string, set results.Set,
	enforcers []string, runs []BreakRun) []CaseRow {
	if len(inv.Cases) == 0 {
		return nil
	}
	out := make([]CaseRow, 0, len(inv.Cases))
	for _, cs := range inv.Cases {
		r := CaseRow{ID: cs.ID, Label: schema.CaseLabel(inv.ID, cs.ID), Kind: string(cs.Kind), Statement: cs.Statement,
			Enforcer: cs.Enforcer, Retired: cs.Retired, Verifiers: append([]string{}, tagged[cs.ID]...)}
		targets := enforcers
		if cs.Enforcer != "" {
			targets = []string{cs.Enforcer}
		}
		r.Coverage, r.Reason = caseCoverage(promise+"#"+cs.ID, r.Verifiers, set, targets, runs)
		out = append(out, r)
	}
	return out
}

// caseCoverage reads one case's state, worst first: failing, uncovered,
// then covered only on a kill of a passed tagged verifier, else partial.
func caseCoverage(ref string, verifiers []string, set results.Set, targets []string, runs []BreakRun) (string, string) {
	if len(verifiers) == 0 {
		return CaseUncovered, "no test is tagged @verifies " + ref
	}
	var passed, failed []string
	for _, v := range verifiers {
		switch o, ok := set.Lookup(v); {
		case ok && o == results.Fail:
			failed = append(failed, v)
		case ok && o == results.Pass:
			passed = append(passed, v)
		}
	}
	switch {
	case len(failed) > 0:
		return CaseFailing, "a tagged verifier's ingested result is red: " + strings.Join(failed, ", ")
	case len(passed) == 0:
		return CaseUncovered, "no tagged verifier has a result (none ran): " + strings.Join(verifiers, ", ")
	case len(targets) == 0:
		return CasePartial, "a tagged verifier passed; there is no enforcer to break"
	}

	target := map[string]bool{}
	for _, e := range targets {
		target[e] = true
	}
	var killed, green, unattributed, other []string
	for _, run := range runs {
		if !target[run.Enforcer] {
			continue
		}
		switch run.Outcome {
		case "killed":
			failedSet := results.FailedSet(run.FailedTests)
			hit := false
			for _, v := range passed {
				if o, ok := failedSet.Lookup(v); ok && o == results.Fail {
					killed = append(killed, run.Enforcer+methodNote(run)+" made "+v+" fail")
					hit = true
				}
			}
			if !hit && len(run.FailedTests) == 0 {
				unattributed = append(unattributed, run.Enforcer)
			} else if !hit {
				other = append(other, run.Enforcer)
			}
		case "survived":
			for _, v := range passed {
				if ranIn(run, v) {
					green = append(green, run.Enforcer+methodNote(run)+" left "+v+" green")
				}
			}
		}
	}
	switch {
	case len(killed) > 0:
		return CaseCovered, "a tagged verifier passed, and breaking " + strings.Join(killed, "; breaking ")
	case len(green) > 0:
		return CasePartial, "a tagged verifier passed, but breaking " + strings.Join(green, "; breaking ")
	case len(other) > 0:
		return CasePartial, "a tagged verifier passed; breaking " + strings.Join(other, ", ") + " failed other tests, not the tagged ones"
	case len(unattributed) > 0:
		return CasePartial, "a tagged verifier passed; breaking " + strings.Join(unattributed, ", ") + " was killed, but the run recorded no failed test names, so the kill is not shown to be the tagged verifier's"
	}
	return CasePartial, "a tagged verifier passed; there is no break run of " + strings.Join(targets, ", ") + " (lattice break)"
}

// ranIn reports whether a break run executed a verifier. A run written
// before ran_verifiers existed reads as if every verifier it lists ran.
func ranIn(run BreakRun, v string) bool {
	for _, x := range run.NotRunVerifiers {
		if x == v {
			return false
		}
	}
	if len(run.RanVerifiers) == 0 {
		return true
	}
	for _, x := range run.RanVerifiers {
		if x == v {
			return true
		}
	}
	return false
}
