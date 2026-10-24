package coverage

// @module-feature coverage

import (
	"sort"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Index is an ingested set bound to the graph: it answers "did this test's
// own run execute this symbol, and on what evidence?" for the RTM and the
// coverage findings. It implements rtm.CoverageEvidence.
type Index struct {
	Set      Set
	perTest  map[string]map[string]bool
	perFile  map[string]map[string]bool
	testFile map[string]string // test FQN -> its file
}

// NewIndex binds a set to the graph's tests.
func NewIndex(set Set, kg schema.KnowledgeGraph) *Index {
	ix := &Index{Set: set, perTest: map[string]map[string]bool{},
		perFile: map[string]map[string]bool{}, testFile: map[string]string{}}
	for t, syms := range set.PerTest {
		m := map[string]bool{}
		for _, s := range syms {
			m[s] = true
		}
		ix.perTest[t] = m
	}
	for f, syms := range set.PerFile {
		m := map[string]bool{}
		for _, s := range syms {
			m[s] = true
		}
		ix.perFile[f] = m
	}
	for _, t := range kg.Tests {
		ix.testFile[t.FQN] = t.File
	}
	return ix
}

// TestRan reports whether the test's own coverage executed the symbol, and
// the basis: per_test when the test has its own report, per_file when only
// a run of its test file does, none otherwise.
func (ix *Index) TestRan(test, symbol string) (bool, string) {
	if m, ok := ix.perTest[test]; ok {
		return m[symbol], BasisPerTest
	}
	if m, ok := ix.perFile[ix.testFile[test]]; ok {
		return m[symbol], BasisPerFile
	}
	return false, "none"
}

// SuiteRan reports whether the whole suite executed the symbol; known is
// false when no suite report covers its file.
func (ix *Index) SuiteRan(symbol string) (bool, bool) {
	c, ok := ix.Set.Symbols[symbol]
	return c.Executed, ok
}

// HasPerTest reports whether any per-test coverage was ingested.
func (ix *Index) HasPerTest() bool { return len(ix.perTest) > 0 }

// EnforcerLines rolls up the enforcers' body lines over the union of the
// tests' per-test runs when every test has per-test line data for them,
// else over the suite. ok is false when neither has data.
func (ix *Index) EnforcerLines(enforcers, tests []string) (lines, hit int, overTests, ok bool) {
	if len(tests) > 0 && ix.allPerTest(tests) {
		for _, e := range enforcers {
			body, ran := map[int]bool{}, map[int]bool{}
			for _, t := range tests {
				ls, has := ix.Set.EnforcerLines[t][e]
				if !has {
					continue
				}
				for _, l := range ls.Body {
					body[l] = true
				}
				for _, l := range ls.Hit {
					ran[l] = true
				}
			}
			if len(body) == 0 {
				// No verifier's run loaded the enforcer's file, so none of its
				// lines ran; the suite says how many there are.
				if c, has := ix.Set.Symbols[e]; has {
					lines += c.Lines
				}
				continue
			}
			lines += len(body)
			hit += len(ran)
		}
		if lines > 0 {
			return lines, hit, true, true
		}
	}
	lines, hit = 0, 0
	for _, e := range enforcers {
		if c, has := ix.Set.Symbols[e]; has {
			lines += c.Lines
			hit += c.Hit
			ok = true
		}
	}
	return lines, hit, false, ok && lines > 0
}

func (ix *Index) allPerTest(tests []string) bool {
	for _, t := range tests {
		if _, ok := ix.perTest[t]; !ok {
			return false
		}
	}
	return true
}

// lackingPerTest returns the tests with no per-test evidence, sorted.
func (ix *Index) lackingPerTest(tests []string) []string {
	var out []string
	for _, t := range tests {
		if _, ok := ix.perTest[t]; !ok {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}
