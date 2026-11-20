package impact

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/extract"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
)

// totalTS is a production symbol of fees that no promise guards.
const totalTS = `// @module-feature fees

export function total(amount: number): number {
  return amount + 1;
}
`

// journeyTest exercises total() without verifying any promise.
const journeyTest = `// @module-feature fees
import { total } from './total';

test('shows the total', () => {
  total(1);
});
`

// unguardedRepo is runnersRepo plus an unguarded production file and a
// journey test that calls it, then a change to that file only.
func unguardedRepo(t *testing.T, test string) (*repo, string) {
	r, _ := runnersRepo(t, runnersConfig)
	r.git("checkout", "-q", "main")
	r.write("apps/web/src/total.ts", totalTS)
	if test != "" {
		r.write(test, journeyTest)
	}
	base := r.commit("add total")
	r.git("checkout", "-q", "-B", "change")
	r.write("apps/web/src/total.ts", strings.Replace(totalTS, "amount + 1", "amount + 2", 1))
	return r, base
}

const totalSym = "apps.web.src.total.total"

// homeSpec is an end-to-end spec that drives the built app: it imports
// nothing from the source tree and names none of its symbols.
const homeSpec = `import { test, expect } from '@playwright/test';

test('opens the home page', async ({ page }) => {
  await page.goto('/home');
  await expect(page.getByRole('heading')).toBeVisible();
});
`

// apartRepo is unguardedRepo with the test file committed on its own,
// before total.ts, so the two never changed in the same commit.
func apartRepo(t *testing.T, test, body string) (*repo, string) {
	r, _ := runnersRepo(t, runnersConfig)
	r.git("checkout", "-q", "main")
	r.write(test, body)
	r.commit("add " + test)
	r.write("apps/web/src/total.ts", totalTS)
	base := r.commit("add total")
	r.git("checkout", "-q", "-B", "change")
	r.write("apps/web/src/total.ts", strings.Replace(totalTS, "amount + 1", "amount + 2", 1))
	return r, base
}

// fakeCoverage says which tests' own runs executed which symbols.
type fakeCoverage map[string][]string

func (f fakeCoverage) TestRan(test, symbol string) (bool, string) {
	syms, ok := f[test]
	if !ok {
		return false, "none"
	}
	for _, s := range syms {
		if s == symbol {
			return true, basisPerTest
		}
	}
	return false, basisPerTest
}

// planWith is planOf with the coverage and, when calls is set, the call
// graph built from the working tree.
func planWith(t *testing.T, r *repo, base string, cov TestCoverage, calls bool) TestPlan {
	t.Helper()
	in := r.input(base)
	cfg, err := config.Load(in.Workspace.LatticeDir)
	if err != nil {
		t.Fatal(err)
	}
	in.Break = &cfg.Break
	if cov != nil {
		in.Coverage = cov
	}
	if calls {
		res, err := extract.Extract(context.Background(), in.Workspace, in.Registry, extract.Options{})
		if err != nil {
			t.Fatal(err)
		}
		in.Calls = reach.Build(res.Modules, reach.Options{})
	}
	rep := r.report(in)
	if rep.Tests == nil {
		t.Fatal("no test plan")
	}
	return *rep.Tests
}

func runnerNamed(p TestPlan, name string) (TestRunner, bool) {
	for _, g := range p.Runners {
		if g.Name == name {
			return g, true
		}
	}
	return TestRunner{}, false
}

// testFQN finds the graph FQN of a test by its file and title.
func testFQN(t *testing.T, r *repo, file, title string) string {
	t.Helper()
	for _, s := range r.input("HEAD").Graph.Tests {
		if s.File == file && s.Name == title {
			return s.FQN
		}
	}
	t.Fatalf("no test %q in %s", title, file)
	return ""
}

// A touched symbol no promise guards selects the tests whose own coverage
// run executed it, labelled coverage, even with no call or file link.
// @verifies impact:INV-7
func TestTestPlanUnguardedByCoverage(t *testing.T) {
	r, base := unguardedRepo(t, "e2e/journey.spec.ts")
	journey := testFQN(t, r, "e2e/journey.spec.ts", "shows the total")
	plan := planWith(t, r, base, fakeCoverage{journey: {totalSym}}, false)

	if len(plan.Runners) != 0 || len(plan.Uncovered) != 0 || len(plan.Unmatched) != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	u := plan.Unmatched[0]
	if u.File != "e2e/journey.spec.ts" || !reflect.DeepEqual(u.Tests, []string{journey}) || len(u.Promises) != 0 ||
		!reflect.DeepEqual(u.Why[journey], []string{"unguarded " + totalSym + " via coverage"}) {
		t.Errorf("unmatched = %+v", u)
	}
}

// Without coverage, the static call graph selects the test that reaches the
// symbol, labelled call_graph, under the runner whose glob matches.
// @verifies impact:INV-7
func TestTestPlanUnguardedByCallGraph(t *testing.T) {
	r, base := unguardedRepo(t, "apps/web/src/checkout.test.ts")
	journey := testFQN(t, r, "apps/web/src/checkout.test.ts", "shows the total")
	plan := planWith(t, r, base, nil, true)

	v, ok := runnerNamed(plan, "vitest")
	if !ok || len(plan.Runners) != 1 || len(plan.Uncovered) != 0 || len(plan.Unmatched) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
	if !reflect.DeepEqual(v.Files, []string{"src/checkout.test.ts"}) || !reflect.DeepEqual(v.Tests, []string{journey}) ||
		len(v.Promises) != 0 || !reflect.DeepEqual(v.Why[journey], []string{"unguarded " + totalSym + " via call_graph"}) {
		t.Errorf("vitest = %+v", v)
	}
	var text bytes.Buffer
	WriteTests(&text, plan)
	if !strings.Contains(text.String(), "  # vitest: unguarded "+totalSym+" via call_graph\n") {
		t.Errorf("text:\n%s", text.String())
	}
}

// Without coverage or a call graph, a test file next to the symbol's file
// with the same base name is selected, labelled heuristic.
// @verifies impact:INV-7
func TestTestPlanUnguardedByNeighbour(t *testing.T) {
	r, base := unguardedRepo(t, "apps/web/src/total.test.ts")
	journey := testFQN(t, r, "apps/web/src/total.test.ts", "shows the total")
	plan := planWith(t, r, base, nil, false)
	v, ok := runnerNamed(plan, "vitest")
	if !ok || !reflect.DeepEqual(v.Why[journey], []string{"unguarded " + totalSym + " via heuristic"}) || len(plan.Uncovered) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
}

// With no evidence at all (the spec never changed with total.ts and never
// names it), the symbol is listed under uncovered with the reason, and the text ends by asking for the full suite; it is never
// silently dropped.
// @verifies impact:INV-7
func TestTestPlanUnguardedUncovered(t *testing.T) {
	r, base := apartRepo(t, "e2e/journey.spec.ts", homeSpec)
	plan := planWith(t, r, base, nil, false)
	if len(plan.Runners) != 0 || len(plan.Unmatched) != 0 ||
		!reflect.DeepEqual(plan.Uncovered, []UncoveredSymbol{{Symbol: totalSym, File: "apps/web/src/total.ts", Lines: "3-5", Reason: ReasonUncovered}}) {
		t.Fatalf("plan = %+v", plan)
	}
	var text bytes.Buffer
	WriteTests(&text, plan)
	want := "# uncovered: touched lines no promise guards and no known test exercises\n" +
		"#   apps/web/src/total.ts:3-5  " + totalSym + "\n" +
		"# run the full suite before merge\n"
	if text.String() != want {
		t.Errorf("text:\n%s", text.String())
	}
}

// A guarded symbol selects only its promise's verifiers, with every
// evidence source present: a test the call graph or coverage links to it is
// not added, and nothing is uncovered.
// @verifies impact:INV-6
// @verifies impact:INV-7
func TestTestPlanGuardedUnchanged(t *testing.T) {
	r, base := unguardedRepo(t, "apps/web/src/checkout.test.ts")
	r.write("apps/web/src/total.ts", totalTS) // undo: only the guarded cap changes
	r.write("apps/web/src/checkout.test.ts", journeyTest+"import { cap } from './fees';\ntest('caps in the journey', () => { cap(1); });\n")
	r.commit("journey calls cap")
	base = r.git("rev-parse", "HEAD")
	r.write("apps/web/src/fees.ts", strings.Replace(feesTS, "Math.min(fee, 100)", "Math.min(fee, 99)", 1))
	journey := testFQN(t, r, "apps/web/src/checkout.test.ts", "caps in the journey")

	plan := planWith(t, r, base, fakeCoverage{journey: {"apps.web.src.fees.cap"}}, true)
	v, ok := runnerNamed(plan, "vitest")
	if !ok || len(plan.Runners) != 2 || len(plan.Unmatched) != 1 || len(plan.Uncovered) != 0 ||
		!reflect.DeepEqual(v.Files, []string{"src/fees.test.ts"}) || !reflect.DeepEqual(v.Promises, []string{"fees:INV-1"}) {
		t.Fatalf("plan = %+v", plan)
	}
	for test, why := range v.Why {
		if !reflect.DeepEqual(why, []string{"promise fees:INV-1"}) {
			t.Errorf("why[%s] = %v", test, why)
		}
	}
}
