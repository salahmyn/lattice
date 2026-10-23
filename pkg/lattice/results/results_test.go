package results

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleJUnit = `<?xml version="1.0" encoding="UTF-8"?>
<testsuites>
  <testsuite name="journey">
    <testcase classname="test.journey.us1" name="lists in insertion order"></testcase>
    <testcase classname="AddTodoHandler" name="execute appends"></testcase>
    <testcase classname="CompleteTodoHandler" name="idempotent">
      <failure message="expected done"></failure>
    </testcase>
    <testcase classname="ListTodos" name="skipped case">
      <skipped></skipped>
    </testcase>
  </testsuite>
</testsuites>`

func TestParseJUnitOutcomes(t *testing.T) {
	set, err := ParseJUnit([]byte(sampleJUnit))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cases := []struct {
		ref  string
		want Outcome
	}{
		{"test.journey.us1.lists in insertion order", Pass},
		{"AddTodoHandler.execute appends", Pass},
		{"CompleteTodoHandler.idempotent", Fail},
		{"ListTodos.skipped case", Skip},
	}
	for _, c := range cases {
		got, ok := set.Lookup(c.ref)
		if !ok {
			t.Errorf("Lookup(%q): not found", c.ref)
			continue
		}
		if got != c.want {
			t.Errorf("Lookup(%q) = %s, want %s", c.ref, got, c.want)
		}
	}
}

func TestLookupTrailingSegment(t *testing.T) {
	set, _ := ParseJUnit([]byte(sampleJUnit))
	// A lattice FQN that differs in prefix but shares the trailing method
	// should still resolve.
	if o, ok := set.Lookup("src.features.complete.CompleteTodoHandler.idempotent"); !ok || o != Fail {
		t.Errorf("trailing-segment lookup = (%s,%v), want (fail,true)", o, ok)
	}
}

func TestLookupMissing(t *testing.T) {
	set, _ := ParseJUnit([]byte(sampleJUnit))
	if _, ok := set.Lookup("nothing.here"); ok {
		t.Error("expected miss for unknown test")
	}
	var empty Set
	if _, ok := empty.Lookup("anything"); ok {
		t.Error("empty set should never match")
	}
}

func TestParseBareTestsuite(t *testing.T) {
	bare := `<testsuite name="x"><testcase classname="A" name="b"></testcase></testsuite>`
	set, err := ParseJUnit([]byte(bare))
	if err != nil {
		t.Fatalf("parse bare: %v", err)
	}
	if o, ok := set.Lookup("A.b"); !ok || o != Pass {
		t.Errorf("bare suite lookup = (%s,%v), want (pass,true)", o, ok)
	}
}

// TestLookupVitestItBlock joins a vitest report (classname = file path, name =
// "describe > it") to the FQN the TypeScript adapter gives an it() block.
func TestLookupVitestItBlock(t *testing.T) {
	const report = `<testsuites><testsuite name="src/cart/total.test.ts">
<testcase classname="src/cart/total.test.ts" name="cart total &gt; adds line items"/>
<testcase classname="src/cart/total.test.ts" name="cart total &gt; applies the customer's discount: a discount never changes another customer's total"><failure/></testcase>
</testsuite></testsuites>`
	set, err := ParseJUnit([]byte(report))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		ref  string
		want Outcome
	}{
		{"web.src.cart.total.test.test.adds-line-items", Pass},
		{"web.src.cart.total.test.test.applies-the-customer-s-discount-a-discount-never-changes-another-customer-s-total", Fail},
	}
	for _, c := range cases {
		if got, ok := set.Lookup(c.ref); !ok || got != c.want {
			t.Errorf("Lookup(%q) = (%s,%v), want (%s,true)", c.ref, got, ok, c.want)
		}
	}
}

// Playwright's JUnit reporter names a case by its title path joined with
// " › ", sets classname to the file path relative to the test directory, and
// repeats every case once per project (browser).
const playwrightReport = `<testsuites id="" name="" tests="5" failures="1" skipped="0" errors="0" time="4.2">
<testsuite name="auth/signin.spec.ts" hostname="chromium" tests="2" failures="0" skipped="0" time="2.0" errors="0">
<testcase name="Sign in › signs in with a code" classname="auth/signin.spec.ts" time="1.1"></testcase>
<testcase name="Sign in › rejects an expired code" classname="auth/signin.spec.ts" time="0.9"></testcase>
</testsuite>
<testsuite name="auth/signin.spec.ts" hostname="webkit" tests="2" failures="1" skipped="0" time="2.0" errors="0">
<testcase name="Sign in › signs in with a code" classname="auth/signin.spec.ts" time="1.0"></testcase>
<testcase name="Sign in › rejects an expired code" classname="auth/signin.spec.ts" time="1.0"><failure message="expected 401" type="FAILURE">expected 401</failure></testcase>
</testsuite>
<testsuite name="home.spec.cjs" hostname="chromium" tests="1" failures="0" skipped="0" time="0.2" errors="0">
<testcase name="loads the home screen" classname="home.spec.cjs" time="0.2"></testcase>
</testsuite>
</testsuites>`

func TestLookupPlaywrightTitlePath(t *testing.T) {
	set, err := ParseJUnit([]byte(playwrightReport))
	if err != nil {
		t.Fatal(err)
	}
	// FQNs as the TypeScript adapter gives them for e2e/auth/signin.spec.ts
	// and e2e/home.spec.cjs, with e2e/ as Playwright's testDir.
	cases := []struct {
		ref  string
		want Outcome
	}{
		{"e2e.auth.signin.spec.test.signs-in-with-a-code", Pass},
		// passes on chromium, fails on webkit: the failure wins.
		{"e2e.auth.signin.spec.test.rejects-an-expired-code", Fail},
		{"e2e.home.spec.test.loads-the-home-screen", Pass},
	}
	for _, c := range cases {
		if got, ok := set.Lookup(c.ref); !ok || got != c.want {
			t.Errorf("Lookup(%q) = (%s,%v), want (%s,true)", c.ref, got, ok, c.want)
		}
	}
}

// Two files with the same test title: the file path decides, not map order.
func TestLookupPrefersMostSpecificMatch(t *testing.T) {
	const report = `<testsuites>
<testsuite name="a"><testcase classname="cart/total.test.ts" name="rounds once"/></testsuite>
<testsuite name="b"><testcase classname="tax/total.test.ts" name="rounds once"><failure/></testcase></testsuite>
</testsuites>`
	set, err := ParseJUnit([]byte(report))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ { // map order varies run to run
		if got, ok := set.Lookup("web.cart.total.test.test.rounds-once"); !ok || got != Pass {
			t.Fatalf("cart case: got (%s,%v), want (pass,true)", got, ok)
		}
		if got, ok := set.Lookup("web.tax.total.test.test.rounds-once"); !ok || got != Fail {
			t.Fatalf("tax case: got (%s,%v), want (fail,true)", got, ok)
		}
	}
}

// One run, two reports: unit and e2e results land in one set, and a failure
// in either report wins over a pass for the same case.
func TestIngestMergesReports(t *testing.T) {
	dir := t.TempDir()
	unit := filepath.Join(dir, "unit.xml")
	e2e := filepath.Join(dir, "e2e.xml")
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(unit, `<testsuite name="u"><testcase classname="tests.credit" name="test_never_negative"/>
<testcase classname="tests.shared" name="test_flaky"/></testsuite>`)
	write(e2e, playwrightReport)
	write(filepath.Join(dir, "shared.xml"), `<testsuite name="s"><testcase classname="tests.shared" name="test_flaky"><failure/></testcase></testsuite>`)

	set, err := Ingest(dir, "abc123", unit, e2e, filepath.Join(dir, "shared.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if set.Commit != "abc123" {
		t.Errorf("commit = %q", set.Commit)
	}
	for ref, want := range map[string]Outcome{
		"tests.credit.test_never_negative":               Pass,
		"e2e.auth.signin.spec.test.signs-in-with-a-code": Pass,
		"tests.shared.test_flaky":                        Fail,
	} {
		if got, ok := Load(dir).Lookup(ref); !ok || got != want {
			t.Errorf("Lookup(%q) = (%s,%v), want (%s,true)", ref, got, ok, want)
		}
	}
	if _, err := Ingest(dir, "", filepath.Join(dir, "missing.xml")); err == nil {
		t.Error("ingesting a missing report should fail")
	}
}
