package results

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// tests is a graph's test list for WithTests.
func tests(fqns ...string) []schema.GraphSymbol {
	out := make([]schema.GraphSymbol, len(fqns))
	for i, f := range fqns {
		out[i] = schema.GraphSymbol{FQN: f, Language: "typescript", IsTest: true}
	}
	return out
}

// junitOf builds a Playwright-style report for one spec file: the name is the
// describe path and the expanded title joined by " › ".
func junitOf(t *testing.T, classname string, cases map[string]string) Set {
	t.Helper()
	var b strings.Builder
	b.WriteString("<testsuites><testsuite name=\"" + classname + "\">")
	for name, outcome := range cases {
		b.WriteString("<testcase classname=\"" + classname + "\" name=\"" + name + "\">")
		switch outcome {
		case "fail":
			b.WriteString("<failure/>")
		case "skip":
			b.WriteString("<skipped/>")
		}
		b.WriteString("</testcase>")
	}
	b.WriteString("</testsuite></testsuites>")
	set, err := ParseJUnit([]byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	return set
}

const headerRef = "e2e.shop.header.spec.test.header-fits-on-_-_"

// Every expansion of a parameterized test reported by its expanded name
// joins the test: all passed is a pass, one failure is a failure, all
// skipped is a skip.
// @verifies results:INV-4
func TestLookupParameterizedExpansions(t *testing.T) {
	expansions := []string{"mobile, ar", "mobile, en", "desktop, ar", "desktop, en"}
	cases := func(failing string) map[string]string {
		m := map[string]string{}
		for _, e := range expansions {
			o := "pass"
			if e == failing {
				o = "fail"
			}
			m["shop header › header fits on "+e] = o
		}
		return m
	}
	for _, bind := range []bool{false, true} {
		green := junitOf(t, "shop/header.spec.ts", cases(""))
		red := junitOf(t, "shop/header.spec.ts", cases("desktop, en"))
		if bind {
			green, red = green.WithTests(tests(headerRef)), red.WithTests(tests(headerRef))
		}
		if o, ok := green.Lookup(headerRef); !ok || o != Pass {
			t.Errorf("bound=%v all expansions passed: (%s,%v), want pass", bind, o, ok)
		}
		if o, ok := red.Lookup(headerRef); !ok || o != Fail {
			t.Errorf("bound=%v one expansion failed: (%s,%v), want fail", bind, o, ok)
		}
	}
	skipped := junitOf(t, "shop/header.spec.ts", map[string]string{
		"header fits on mobile, ar": "skip", "header fits on mobile, en": "skip"})
	if o, ok := skipped.Lookup(headerRef); !ok || o != Skip {
		t.Errorf("all skipped: (%s,%v), want skip", o, ok)
	}
	// A placeholder matches non-empty text only, and the literal text is
	// matched as written.
	other := junitOf(t, "shop/header.spec.ts", map[string]string{
		"header fits on ": "pass", "footer fits on mobile, ar": "pass"})
	if o, ok := other.Lookup(headerRef); ok {
		t.Errorf("no expansion of the template in the report, got %s", o)
	}
}

// A report case a plain test names exactly is that test's, never an
// expansion of a parameterized test whose pattern it also fits; two
// parameterized tests that match one case equally leave both unknown, and
// the duplicate of one template in the same file is such a pair.
// @verifies results:INV-8
func TestLookupParameterizedClaims(t *testing.T) {
	set := junitOf(t, "src/cart/fees.test.ts", map[string]string{
		"fees > rounds 1 to 2": "fail", // the plain test's case
		"fees > rounds 3 to 4": "pass",
		"fees > rounds 5 to 6": "pass",
	})
	plain, tmpl := "src.cart.fees.test.test.rounds-1-to-2", "src.cart.fees.test.test.rounds-_-to-_"
	if o, _ := set.Lookup(tmpl); o != Fail {
		t.Fatalf("unbound, the template takes every case it fits: got %s", o)
	}
	bound := set.WithTests(tests(plain, tmpl))
	if o, ok := bound.Lookup(plain); !ok || o != Fail {
		t.Errorf("plain test: (%s,%v), want its own fail", o, ok)
	}
	if o, ok := bound.Lookup(tmpl); !ok || o != Pass {
		t.Errorf("template: (%s,%v), want pass: the failing case is the plain test's", o, ok)
	}
	// A plain test of the same title in another file claims nothing here.
	elsewhere := set.WithTests(tests("src.wallet.fees.test.test.rounds-1-to-2", tmpl))
	if o, _ := elsewhere.Lookup(tmpl); o != Fail {
		t.Errorf("a plain test in another file must not take this file's case: got %s", o)
	}

	// Two templates match "caps fees at 5 in cart": neither may pass.
	amb := junitOf(t, "src/cart/fees.test.ts", map[string]string{
		"caps fees at 5 in cart":   "pass",
		"caps fees at 9 in wallet": "pass",
	})
	a, b := "src.cart.fees.test.test.caps-fees-at-_-in-cart", "src.cart.fees.test.test.caps-fees-at-_-in-_"
	boundAmb := amb.WithTests(tests(a, b))
	for _, ref := range []string{a, b} {
		if o, ok := boundAmb.Lookup(ref); ok {
			t.Errorf("%s: ambiguous case resolved to %s", ref, o)
		}
	}
	if got := boundAmb.Ambiguous(a); len(got) != 1 || !strings.Contains(got[0], "caps fees at 5 in cart") {
		t.Errorf("Ambiguous(%s) = %v, want the shared case", a, got)
	}
	// A template in another file matches on a shorter path: no ambiguity.
	far := amb.WithTests(tests(a, "src.wallet.fees.test.test.caps-fees-at-_-in-_"))
	if o, ok := far.Lookup(a); !ok || o != Pass {
		t.Errorf("template elsewhere: (%s,%v), want pass", o, ok)
	}

	// The same template at two call sites of one file: _2 shares its pattern.
	dup := junitOf(t, "src/cart/fees.test.ts", map[string]string{"caps cart": "pass", "caps wallet": "pass"})
	first, second := "src.cart.fees.test.test.caps-_", "src.cart.fees.test.test.caps-__2"
	boundDup := dup.WithTests(tests(first, second))
	for _, ref := range []string{first, second} {
		if o, ok := boundDup.Lookup(ref); ok {
			t.Errorf("%s: a case two call sites share resolved to %s", ref, o)
		}
	}
}

// A parameterized test is selected by a name filter that matches each of
// its expansions: the literal text escaped, each placeholder any non-empty
// text; a plain title is matched as written.
// @verifies results:INV-5
func TestSelectorsParameterized(t *testing.T) {
	kg := schema.KnowledgeGraph{Tests: []schema.GraphSymbol{
		{FQN: "e2e.shop.header.spec.test.header-fits-on-_-_", Name: "header fits on ${view.name}, ${lang.name} (RTL)",
			Language: "typescript", File: "e2e/shop/header.spec.ts", Parameterized: true, Verifies: []string{"shop:INV-1"}},
		{FQN: "src.cart.fees.test.test.rounds-_-to-_", Name: "rounds %s to $b.c", Language: "typescript",
			File: "src/cart/fees.test.ts", Parameterized: true, Verifies: []string{"fees:INV-1"}},
		{FQN: "src.cart.fees.test.test.costs-100-s", Name: "costs 100%s", Language: "typescript",
			File: "src/cart/fees.test.ts", Verifies: []string{"fees:INV-2"}},
	}}
	by := map[string]Selector{}
	for _, s := range Selectors(kg, true) {
		by[s.Test] = s
	}
	header := by["e2e.shop.header.spec.test.header-fits-on-_-_"]
	if !header.Parameterized {
		t.Error("selector lost the parameterized flag")
	}
	if got, want := strings.Join(header.Filters["vitest"], " "), `-t header fits on .+?, .+? \(RTL\)$`; got != want {
		t.Errorf("vitest filter = %q, want %q", got, want)
	}
	if got, want := strings.Join(header.Filters["node"], " "), `--test-name-pattern ^header fits on .+?, .+? \(RTL\)$`; got != want {
		t.Errorf("node filter = %q, want %q", got, want)
	}
	re := regexp.MustCompile(header.TitlePattern() + "$")
	for name, want := range map[string]bool{
		"shop header › header fits on mobile, ar (RTL)": true,
		"header fits on desktop, en (RTL)":              true,
		"header fits on , ar (RTL)":                     false,
		"header fits on mobile, ar":                     false,
	} {
		if re.MatchString(name) != want {
			t.Errorf("filter %q on %q = %v, want %v", re, name, !want, want)
		}
	}
	if got := by["src.cart.fees.test.test.rounds-_-to-_"].TitlePattern(); got != `rounds .+? to .+?` {
		t.Errorf(".each filter = %q", got)
	}
	if got := by["src.cart.fees.test.test.costs-100-s"].TitlePattern(); got != `costs 100%s` {
		t.Errorf("a plain title with %%s must stay literal: %q", got)
	}
}

// End to end: a small JUnit report of expanded names, ingested from disk,
// demonstrates the parameterized test whose every expansion passed, fails
// the one with a red expansion, and leaves the ambiguous pair unknown.
// @verifies results:INV-4, results:INV-8
func TestIngestParameterizedJUnit(t *testing.T) {
	const report = `<?xml version="1.0" encoding="UTF-8"?>
<testsuites name="vitest tests">
  <testsuite name="src/cart/fees.test.ts" tests="6">
    <testcase classname="src/cart/fees.test.ts" name="fees &gt; charges 1 fees"/>
    <testcase classname="src/cart/fees.test.ts" name="fees &gt; charges 2 fees"/>
    <testcase classname="src/cart/fees.test.ts" name="fees &gt; rounds 1.5 to 2"/>
    <testcase classname="src/cart/fees.test.ts" name="fees &gt; rounds 2.5 to 3"><failure message="expected 3"/></testcase>
    <testcase classname="src/cart/fees.test.ts" name="fees &gt; waives fees for cart"/>
  </testsuite>
</testsuites>`
	dir := t.TempDir()
	path := filepath.Join(dir, "junit.xml")
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := Ingest(filepath.Join(dir, "lattice"), "abc123", path)
	if err != nil {
		t.Fatal(err)
	}
	const base = "apps.web.src.cart.fees.test.test."
	bound := Load(filepath.Join(dir, "lattice")).WithTests(tests(
		base+"charges-_-fees", base+"rounds-_-to-_", base+"waives-fees-for-_", base+"waives-_-for-cart"))
	for ref, want := range map[string]Outcome{
		base + "charges-_-fees": Pass,
		base + "rounds-_-to-_":  Fail,
	} {
		if o, ok := bound.Lookup(ref); !ok || o != want {
			t.Errorf("%s: (%s,%v), want %s", ref, o, ok, want)
		}
	}
	for _, ref := range []string{base + "waives-fees-for-_", base + "waives-_-for-cart"} {
		if o, ok := bound.Lookup(ref); ok {
			t.Errorf("%s: ambiguous expansion resolved to %s", ref, o)
		}
	}
	if cases, pass, fail, _ := set.Tally(); cases != 5 || pass != 4 || fail != 1 {
		t.Errorf("tally = %d cases, %d pass, %d fail", cases, pass, fail)
	}
}
