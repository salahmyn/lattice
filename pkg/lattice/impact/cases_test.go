package impact

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// helperTest is a test file that holds, beside its one test, a class with a
// method, a builder and a plain helper, each of which reaches total().
const helperTest = `// @module-feature fees
import { total } from './total';

class Box {
  constructor(private n: number) {}
  open(): number {
    return total(this.n);
  }
}

function boxOf(n: number): Box {
  return new Box(n);
}

function iso(n: number): number {
  return total(n);
}

test('shows the total', () => {
  iso(1);
  boxOf(1).open();
});
`

// testsOf lists every test a plan names: under runners, unmatched files and
// hinted symbols, and as keys of each why.
func testsOf(p TestPlan) []string {
	var out []string
	add := func(tests []string, why map[string][]string) {
		out = appendNew(out, tests...)
		for t := range why {
			out = appendNew(out, t)
		}
	}
	for _, r := range p.Runners {
		add(r.Tests, r.Why)
	}
	for _, u := range p.Unmatched {
		add(u.Tests, u.Why)
	}
	for _, h := range p.Hinted {
		add(h.Tests, nil)
	}
	sort.Strings(out)
	return out
}

// The helpers of a test file are never listed as tests, whichever evidence
// finds the file: the call graph walks through them to the test, and the
// neighbour heuristic takes only the file's tests.
// @verifies impact:INV-9
func TestTestPlanListsNoHelpers(t *testing.T) {
	for _, tc := range []struct {
		file  string
		calls bool
		via   string
	}{
		{"apps/web/src/checkout.test.ts", true, ViaCallGraph},
		{"apps/web/src/total.test.ts", false, ViaHeuristic},
	} {
		r, base := unguardedRepo(t, "")
		r.write(tc.file, helperTest)
		r.commit("add the test with its helpers")
		base = r.git("rev-parse", "HEAD")
		r.write("apps/web/src/total.ts", strings.Replace(totalTS, "amount + 1", "amount + 3", 1))
		journey := testFQN(t, r, tc.file, "shows the total")

		plan := planWith(t, r, base, nil, tc.calls)
		if got := testsOf(plan); !reflect.DeepEqual(got, []string{journey}) {
			t.Errorf("%s: tests = %v, want only %s", tc.via, got, journey)
		}
		v, ok := runnerNamed(plan, "vitest")
		if !ok || !reflect.DeepEqual(v.Why[journey], []string{"unguarded " + totalSym + " via " + tc.via}) {
			t.Errorf("%s: plan = %+v", tc.via, plan)
		}
	}
}

// A hint lists the tests of a spec, never its helpers, and a support file
// that holds only helpers is no test file: edited in the same change, it is
// not hinted. Each hinted symbol lists its tests' files once, in the JSON
// field "files".
// @verifies impact:INV-9
func TestTestPlanHintGroupsFilesWithoutHelpers(t *testing.T) {
	r, base := apartRepo(t, checkoutSpecFile, checkoutSpec("paid"))
	r.write("e2e/support/fake.ts", "export class FakeGateway {\n  handle(n: number): number {\n    return n;\n  }\n}\n")
	r.commit("add the fake gateway")
	base = r.git("rev-parse", "HEAD")
	spec := checkoutSpec("paid in full") + `
function boxOf(n: number): number {
  return n;
}

test('pays twice', async ({ page }) => {
  await page.goto('/pay?n=' + boxOf(2));
});
`
	r.write(checkoutSpecFile, spec)
	r.write("e2e/support/fake.ts", "export class FakeGateway {\n  handle(n: number): number {\n    return n + 1;\n  }\n}\n")
	r.write("apps/web/src/total.ts", strings.Replace(totalTS, "amount + 1", "amount + 3", 1))

	plan := planWith(t, r, base, nil, false)
	pays, twice := specFQN(t, r), testFQN(t, r, checkoutSpecFile, "pays twice")
	want := sortedPair(pays, twice)
	if got := testsOf(plan); !reflect.DeepEqual(got, want) {
		t.Fatalf("tests = %v, want %v", got, want)
	}
	if len(plan.Unmatched) != 1 || plan.Unmatched[0].File != checkoutSpecFile || len(plan.Hinted) != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	raw, err := json.Marshal(plan.Hinted[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"files":["`+checkoutSpecFile+`"]`) {
		t.Errorf("hinted = %s, want its one test file under \"files\"", raw)
	}
}

// testCases tells the tests a runner runs from the helpers of test files,
// per language.
// @verifies impact:INV-9
func TestTestCases(t *testing.T) {
	sym := func(lang, kind, file, fqn, name, enclosing string, verifies ...string) schema.GraphSymbol {
		return schema.GraphSymbol{Language: lang, Kind: kind, File: file, FQN: fqn, Name: name, EnclosingFQN: enclosing,
			IsTest: true, Verifies: verifies}
	}
	tests := []schema.GraphSymbol{
		sym("go", "function", "fees/fees_test.go", "fees.TestCap", "TestCap", ""),
		sym("go", "function", "fees/fees_test.go", "fees.TestCap.over_the_cap", "over the cap", "fees.TestCap"),
		sym("go", "function", "fees/fees_test.go", "fees.Testify", "Testify", ""),
		sym("go", "function", "fees/fees_test.go", "fees.newWallet", "newWallet", ""),
		sym("go", "class", "fees/fees_test.go", "fees.fakeLedger", "fakeLedger", ""),
		sym("go", "method", "fees/fees_test.go", "fees.fakeLedger.Post", "Post", "fees.fakeLedger"),
		sym("go", "function", "fees/fees_test.go", "fees.newWallet.inner", "inner", "fees.newWallet"),
		sym("go", "function", "fees/fees_test.go", "fees.checkCap", "checkCap", "", "fees:INV-1"),
		sym("typescript", "function", "e2e/orders.spec.ts", "e2e.orders.spec.test.places-an-order", "places an order", ""),
		sym("typescript", "function", "e2e/orders.spec.ts", "e2e.orders.spec.boxOf", "boxOf", ""),
		sym("typescript", "function", "test/orders.ts", "test.orders.iso", "iso", ""),
		sym("python", "function", "tests/test_wallet.py", "tests.test_wallet.test_debit", "test_debit", ""),
		sym("python", "function", "tests/test_wallet.py", "tests.test_wallet.make_wallet", "make_wallet", ""),
		sym("php", "method", "tests/WalletTest.php", "Tests.WalletTest::testDebit", "testDebit", "Tests.WalletTest"),
		sym("php", "method", "tests/WalletTest.php", "Tests.WalletTest::seed", "seed", "Tests.WalletTest"),
	}
	got := testCases(tests)
	var names []string
	for fqn := range got {
		names = append(names, fqn)
	}
	sort.Strings(names)
	want := []string{"Tests.WalletTest::testDebit", "e2e.orders.spec.test.places-an-order", "fees.TestCap", "fees.TestCap.over_the_cap",
		"fees.checkCap", "tests.test_wallet.test_debit"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("test cases = %v, want %v", names, want)
	}
}
