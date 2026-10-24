package reach

// @module-feature reachability

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// testDirs are the directory names that mark a top-level test suite when
// coverage.suites is not set.
var testDirs = map[string]bool{
	"tests": true, "test": true, "__tests__": true, "e2e": true, "__e2e__": true,
	"spec": true, "specs": true, "cypress": true, "playwright": true,
}

// Suites groups the graph's test files into suites: coverage.suites when
// set (name -> globs over graph file paths), else one suite per top-level
// test directory (tests/, e2e/, cypress/, ...). Tests outside every suite
// belong to none. Suite names are sorted.
func Suites(kg schema.KnowledgeGraph, cfg config.Coverage) map[string][]schema.GraphSymbol {
	out := map[string][]schema.GraphSymbol{}
	for _, t := range kg.Tests {
		if len(cfg.Suites) > 0 {
			for name, globs := range cfg.Suites {
				for _, g := range globs {
					if ok, _ := doublestar.Match(g, t.File); ok {
						out[name] = append(out[name], t)
						break
					}
				}
			}
			continue
		}
		if i := strings.IndexByte(t.File, '/'); i > 0 && testDirs[t.File[:i]] {
			out[t.File[:i]] = append(out[t.File[:i]], t)
		}
	}
	return out
}

// suiteTargets is what a suite's tests claim to verify: the @verifies
// targets of its tests (feature:INV-N, brd.<id>:SC-N, brd.<id>:US-N), and
// the entry points named in verified_by of the scenarios its tests verify.
func suiteTargets(kg schema.KnowledgeGraph, tests []schema.GraphSymbol) map[string]bool {
	eps := map[string]bool{}
	for _, ep := range kg.EntryPoints {
		eps[ep.ID] = true
	}
	inSuite := map[string]bool{}
	out := map[string]bool{}
	for _, t := range tests {
		inSuite[t.FQN] = true
		for _, ref := range t.Verifies {
			if !strings.Contains(ref, ":") && t.Feature != "" {
				ref = t.Feature + ":" + ref
			}
			out[ref] = true
		}
	}
	for _, b := range kg.BRDs {
		for _, sc := range b.UserScenarios {
			mine := false
			for _, e := range sc.VerifiedBy {
				if inSuite[e] {
					mine = true
				}
			}
			if !mine {
				continue
			}
			for _, e := range sc.VerifiedBy {
				if eps[e] {
					out["ep:"+e] = true
				}
			}
		}
	}
	return out
}

// SuiteOverlap is SUITE_OVERLAP: two suites whose verified targets have a
// Jaccard index at or above coverage.overlap (default 0.8). The basis is
// static: the tags and scenario links the suites declare. Per-test
// end-to-end coverage would compare what the suites run instead; it is
// not read yet.
// @enforces reachability:INV-8
func SuiteOverlap(kg schema.KnowledgeGraph, cfg config.Coverage) []schema.Violation {
	suites := Suites(kg, cfg)
	names := make([]string, 0, len(suites))
	sets := map[string]map[string]bool{}
	for name, tests := range suites {
		if s := suiteTargets(kg, tests); len(s) > 0 {
			names = append(names, name)
			sets[name] = s
		}
	}
	sort.Strings(names)
	threshold := cfg.OverlapOrDefault()
	var v []schema.Violation
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			a, b := sets[names[i]], sets[names[j]]
			shared := 0
			for k := range a {
				if b[k] {
					shared++
				}
			}
			union := len(a) + len(b) - shared
			jac := float64(shared) / float64(union)
			if jac < threshold {
				continue
			}
			v = append(v, schema.Violation{
				Code: schema.CodeSuiteOverlap, Severity: schema.SeverityInfo,
				Subject: names[i] + "~" + names[j],
				Message: fmt.Sprintf("test suites %s and %s overlap: %d of the %d targets they verify are shared (Jaccard %.2f, threshold %.2f) (evidence: static @verifies tags and scenario entry points; per-test end-to-end coverage would compare what they run)",
					names[i], names[j], shared, union, jac, threshold),
				Location: &schema.Location{File: "config.yaml"},
				NextAction: &schema.NextAction{Kind: "review_tests",
					Detail: "merge the suites or split what each one verifies; set coverage.suites or coverage.overlap to tune the check"},
			})
		}
	}
	return v
}
