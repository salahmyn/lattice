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

// A table-driven test (it.each) is demonstrated only when every row passed.
func TestLookupEachRows(t *testing.T) {
	const report = `<testsuites>
<testsuite name="src/parent/data.test.ts">
<testcase classname="src/parent/data.test.ts" name="library &gt; fails as a whole when the kid_profiles read fails"/>
<testcase classname="src/parent/data.test.ts" name="library &gt; fails as a whole when the feeds read fails"/>
<testcase classname="src/parent/data.test.ts" name="library &gt; 1 plus 2"/>
</testsuite>
<testsuite name="src/other/data.test.ts">
<testcase classname="src/other/data.test.ts" name="fails as a whole when the videos read fails"><failure/></testcase>
</testsuite>
</testsuites>`
	set, err := ParseJUnit([]byte(report))
	if err != nil {
		t.Fatal(err)
	}
	ref := "web.src.parent.data.test.test.fails-as-a-whole-when-the-_-read-fails"
	for i := 0; i < 30; i++ {
		if got, ok := set.Lookup(ref); !ok || got != Pass {
			t.Fatalf("Lookup(%q) = (%s,%v), want pass: the failing row is in another file", ref, got, ok)
		}
	}
	if got, ok := set.Lookup("web.src.parent.data.test.test._-plus-_"); !ok || got != Pass {
		t.Errorf("$a plus $b: got (%s,%v)", got, ok)
	}

	red, _ := ParseJUnit([]byte(`<testsuite name="s">
<testcase classname="src/parent/data.test.ts" name="fails as a whole when the feeds read fails"/>
<testcase classname="src/parent/data.test.ts" name="fails as a whole when the devices read fails"><failure/></testcase>
</testsuite>`))
	if got, _ := red.Lookup(ref); got != Fail {
		t.Errorf("one red row must fail the table test, got %s", got)
	}
	// A Python name with underscores is not a template.
	py, _ := ParseJUnit([]byte(`<testsuite name="p"><testcase classname="tests.test_cart" name="test_valid"/></testsuite>`))
	if got, ok := py.Lookup("tests.test_cart.test_valid"); !ok || got != Pass {
		t.Errorf("python lookup: (%s,%v)", got, ok)
	}
}

// A ref that only shares its title with cases from different files is
// ambiguous: the lookup refuses rather than guess, and Ambiguous names them.
// One case seen through several of its own keys is not ambiguous.
func TestLookupRefusesAmbiguous(t *testing.T) {
	const report = `<testsuites>
<testsuite name="a"><testcase classname="cart.LimitTest" name="test_valid"/></testsuite>
<testsuite name="b"><testcase classname="pay.CardTest" name="test_valid"><failure/></testcase></testsuite>
<testsuite name="c"><testcase classname="tv.PairTest" name="test_pairs"/></testsuite>
</testsuites>`
	set, err := ParseJUnit([]byte(report))
	if err != nil {
		t.Fatal(err)
	}
	if o, ok := set.Lookup("src.wallet.WalletTest.test_valid"); ok {
		t.Errorf("ambiguous ref resolved to %s", o)
	}
	if got := set.Ambiguous("src.wallet.WalletTest.test_valid"); len(got) != 2 {
		t.Errorf("Ambiguous = %v, want the two cases", got)
	}
	// A path that singles one out still resolves.
	if o, ok := set.Lookup("tests.pay.CardTest.test_valid"); !ok || o != Fail {
		t.Errorf("qualified ref: (%s,%v)", o, ok)
	}
	// One case only, matched through its bare-name and qualified keys.
	if o, ok := set.Lookup("src.tv.other.test_pairs"); !ok || o != Pass || set.Ambiguous("src.tv.other.test_pairs") != nil {
		t.Errorf("single case: (%s,%v) ambiguous=%v", o, ok, set.Ambiguous("src.tv.other.test_pairs"))
	}
}

// pg_prove --verbose output: each file's TAP follows a "<file> .." header,
// and an assertion joins its postgres-adapter FQN by file and description.
func TestParseTAP(t *testing.T) {
	tap := `supabase/tests/refunds_test.sql ..
1..4
ok 1 - a refund is recorded once
not ok 2 - a second refund is rejected
# Failed test 2: "a second refund is rejected"
ok 3 - pending refunds expire # SKIP needs pg_cron
ok 4 # nothing to join
ok
supabase/tests/feeds_test.sql ..
1..1
ok 1 - a parent reads only own feeds
ok
All tests successful.`
	set, err := ParseTAP([]byte(tap))
	if err != nil {
		t.Fatal(err)
	}
	for fqn, want := range map[string]Outcome{
		"supabase.tests.refunds_test.test.a-refund-is-recorded-once":   Pass,
		"supabase.tests.refunds_test.test.a-second-refund-is-rejected": Fail,
		"supabase.tests.refunds_test.test.pending-refunds-expire":      Skip,
		"supabase.tests.feeds_test.test.a-parent-reads-only-own-feeds": Pass,
	} {
		if got, ok := set.Lookup(fqn); !ok || got != want {
			t.Errorf("Lookup(%s) = %q, %v; want %q", fqn, got, ok, want)
		}
	}
}

// A bare TAP stream (psql -f) has no file header: cases still join by the
// description slug, and the same description in two files would be refused
// as ambiguous by the usual rule.
func TestParseTAPWithoutFile(t *testing.T) {
	set, err := ParseTAP([]byte("1..1\nok 1 - a refund is recorded once\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := set.Lookup("db.tests.refunds.test.a-refund-is-recorded-once"); !ok || got != Pass {
		t.Errorf("Lookup = %q, %v; want pass", got, ok)
	}
	if _, err := ParseTAP([]byte("1..0\n")); err == nil {
		t.Error("a stream with no described test lines is an error")
	}
}

// Ingest reads TAP and JUnit reports of one run together.
func TestIngestTAPAndJUnit(t *testing.T) {
	dir := t.TempDir()
	tapPath := filepath.Join(dir, "db.tap")
	xmlPath := filepath.Join(dir, "unit.xml")
	if err := os.WriteFile(tapPath, []byte("supabase/tests/a_test.sql ..\nok 1 - db rule holds\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(xmlPath, []byte(`<testsuite><testcase classname="src/cart.test.ts" name="adds"/></testsuite>`), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := Ingest(dir, "abc", tapPath, xmlPath)
	if err != nil {
		t.Fatal(err)
	}
	if o, ok := set.Lookup("supabase.tests.a_test.test.db-rule-holds"); !ok || o != Pass {
		t.Errorf("tap case = %q, %v", o, ok)
	}
	if o, ok := set.Lookup("src.cart.test.test.adds"); !ok || o != Pass {
		t.Errorf("junit case = %q, %v", o, ok)
	}
}

// nodeJUnit is what Node's built-in runner writes with
// `node --test --test-reporter=junit` (Node 22): top-level test() calls are
// <testcase> elements directly under <testsuites>, classname "test", and no
// file path anywhere.
const nodeJUnit = `<?xml version="1.0" encoding="utf-8"?>
<testsuites>
	<testcase name="every tool description follows the template" time="0.009942" classname="test"/>
	<testcase name="tool names are unique and namespaced" time="0.000261" classname="test">
		<failure type="testCodeFailure" message="duplicate tool name"/>
	</testcase>
	<testcase name="tools invoke the CLI against the sample project" time="0.000644" classname="test">
		<skipped type="skipped" message="true"/>
	</testcase>
	<!-- tests 3 -->
</testsuites>`

// A Node runner report parses, and its cases join the TypeScript adapter's
// test FQNs (<module>.test.<slug of the title>) although it names no file.
func TestParseNodeRunnerJUnit(t *testing.T) {
	set, err := ParseJUnit([]byte(nodeJUnit))
	if err != nil {
		t.Fatalf("ParseJUnit: %v", err)
	}
	for ref, want := range map[string]Outcome{
		"lattice-mcp.test.descriptions.test.test.every-tool-description-follows-the-template":    Pass,
		"lattice-mcp.test.descriptions.test.test.tool-names-are-unique-and-namespaced":           Fail,
		"lattice-mcp.test.integration.test.test.tools-invoke-the-cli-against-the-sample-project": Skip,
	} {
		if got, ok := set.Lookup(ref); !ok || got != want {
			t.Errorf("Lookup(%s) = %q, %v; want %q", ref, got, ok, want)
		}
	}
	// The title key is for test() titles only: an identifier-style name
	// gets none, so a Go or pytest case cannot pick up a TypeScript FQN.
	for _, k := range keysFor("pkg", "test_valid") {
		if k == "test.test-valid" {
			t.Errorf("identifier-style name got a title key: %v", keysFor("pkg", "test_valid"))
		}
	}
}
