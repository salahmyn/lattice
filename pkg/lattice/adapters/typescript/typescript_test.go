package typescript

import (
	"context"
	"testing"
)

const tsSample = `/**
 * @module-feature checkout.refund.settlement
 * @module-enforces INV-12, INV-13
 */

import { Money } from './types';

/**
 * @feature checkout.refund
 * @capability self_service_refund, wallet_refund_destination
 * @enforces INV-2, INV-3
 */
export async function enqueueRefund(orderId: string): Promise<void> {}

export class RefundProcessor extends BaseProcessor {
  /** @enforces INV-5 */
  async process(refund: Money): Promise<void> {}
}
`

func TestParseTypeScript(t *testing.T) {
	mod, err := New().Parse(context.Background(), "src/checkout/refund/service.ts", []byte(tsSample))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(mod.ModuleAnnotations) != 2 {
		t.Fatalf("module annotations: got %d want 2", len(mod.ModuleAnnotations))
	}

	want := map[string]bool{"enqueueRefund": false, "RefundProcessor": false, "process": false}
	for _, s := range mod.Symbols {
		if _, ok := want[s.Name]; ok {
			want[s.Name] = true
		}
		if s.Name == "enqueueRefund" {
			if len(s.Annotations) != 3 {
				t.Errorf("enqueueRefund annotations: got %d want 3", len(s.Annotations))
			}
		}
		if s.Name == "RefundProcessor" {
			if len(s.BaseClasses) != 1 || s.BaseClasses[0] != "src.checkout.refund.service.BaseProcessor" {
				t.Errorf("RefundProcessor bases = %v", s.BaseClasses)
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing symbol %q", name)
		}
	}
}

const tsTestSample = `import { describe, it } from 'vitest';

describe('cart', () => {
  /** @verifies cart:INV-1 */
  it('rejects a bad quantity', () => {});
});

/** @verifies cart:INV-2 */
it('a top-level case', () => {});
`

// TestParseTestCalls covers annotation capture on idiomatic Jest/Vitest test
// calls, including an it() nested inside a describe() block.
func TestParseTestCalls(t *testing.T) {
	mod, err := New().Parse(context.Background(), "test/cart.test.ts", []byte(tsTestSample))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	verifies := map[string]bool{} // verified ref -> seen
	for _, s := range mod.Symbols {
		if !s.IsTest {
			t.Errorf("symbol %q from a test file should be IsTest", s.Name)
		}
		for _, a := range s.Annotations {
			if a.Kind == "verifies" && len(a.Args) == 1 {
				if ref, ok := a.Args[0].(string); ok {
					verifies[ref] = true
				}
			}
		}
	}
	for _, want := range []string{"cart:INV-1", "cart:INV-2"} {
		if !verifies[want] {
			t.Errorf("missing @verifies %s on a test symbol; got %v", want, verifies)
		}
	}
}

const tsRouteSample = `import { Hono } from 'hono';
const app = new Hono();

app.get('/health', (c) => c.json({ ok: true }));
app.post('/carts/:id/items', addItemRoute);

/** @feature cart */
function addItemRoute(c) { return c; }

/** @verifies cart:INV-1 */
function checksQuantity() {}
it('checks quantity', checksQuantity);
`

// TestDetectsRoutesAndNoDoubleCount covers HTTP route auto-detection and the
// rule that it("desc", namedFn) does not create a duplicate test symbol.
// @verifies adapters.typescript:INV-3
func TestDetectsRoutesAndNoDoubleCount(t *testing.T) {
	mod, err := New().Parse(context.Background(), "src/index.ts", []byte(tsRouteSample))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(mod.Surfaces) != 2 {
		t.Fatalf("surfaces: got %d want 2 (%+v)", len(mod.Surfaces), mod.Surfaces)
	}
	got := map[string]bool{}
	for _, s := range mod.Surfaces {
		if !s.Detected || s.Type != "http" {
			t.Errorf("surface %+v: want detected http", s)
		}
		got[s.Method+" "+s.Path] = true
		// A route handed a plain identifier names it; an inline function names none.
		wantSym := map[string]string{"POST /carts/:id/items": "src.index.addItemRoute"}[s.Method+" "+s.Path]
		if s.Symbol != wantSym {
			t.Errorf("surface %s %s: handler symbol %q, want %q", s.Method, s.Path, s.Symbol, wantSym)
		}
	}
	for _, want := range []string{"GET /health", "POST /carts/:id/items"} {
		if !got[want] {
			t.Errorf("missing detected route %q; got %v", want, got)
		}
	}

	// it("checks quantity", checksQuantity) references an already-declared
	// function, so it must not be synthesized as a second symbol.
	for _, s := range mod.Symbols {
		if s.Name == "checks quantity" {
			t.Error("it() with a named-function callback was double-counted as a symbol")
		}
	}
}

// TestTestNameKeepsEscapedQuotes: a description with an escape sequence is
// split into several string nodes; the symbol name must keep all of them.
func TestTestNameKeepsEscapedQuotes(t *testing.T) {
	src := "/** @verifies cart:INV-3 */\nit('the customer\\'s cart keeps its items', () => {});\n"
	mod, err := New().Parse(context.Background(), "src/cart.test.ts", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := "the customer's cart keeps its items"
	for _, s := range mod.Symbols {
		if s.Name == want {
			return
		}
	}
	t.Errorf("no test symbol named %q; got %+v", want, mod.Symbols)
}

// TestDenoTestIsATest: Deno's runner registers tests with Deno.test(name, fn)
// (and Deno.test.ignore); @verifies above them must attach to a test symbol.
func TestDenoTestIsATest(t *testing.T) {
	src := "/** @verifies lib:INV-1 */\nDeno.test('refuses private hosts', () => {});\n" +
		"/** @verifies lib:INV-2 */\nDeno.test.ignore('skipped case', async () => {});\n"
	mod, err := New().Parse(context.Background(), "fn/url_test.ts", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := map[string]bool{}
	for _, s := range mod.Symbols {
		for _, a := range s.Annotations {
			if a.Kind == "verifies" && s.IsTest {
				got[s.Name] = true
			}
		}
	}
	for _, want := range []string{"refuses private hosts", "skipped case"} {
		if !got[want] {
			t.Errorf("no verifying test symbol %q; got %v", want, got)
		}
	}
}

func TestModuleExtensionsAreParsed(t *testing.T) {
	a := New()
	for _, p := range []string{"e2e/smoke.spec.cjs", "e2e/pair.test.mjs", "src/lib.mts", "src/lib.cts"} {
		if !a.CanParse(p) {
			t.Errorf("CanParse(%q) = false", p)
		}
	}
	src := "const { test } = require('@playwright/test');\n" +
		"/** @verifies app:INV-3 */\ntest('signs in with a code', async ({ page }) => {});\n"
	mod, err := a.Parse(context.Background(), "e2e/signin.spec.cjs", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var found bool
	for _, s := range mod.Symbols {
		if s.IsTest && s.Name == "signs in with a code" {
			for _, an := range s.Annotations {
				found = found || an.Kind == "verifies"
			}
			if s.FQN != "e2e.signin.spec.test.signs-in-with-a-code" {
				t.Errorf("FQN = %q", s.FQN)
			}
		}
	}
	if !found {
		t.Errorf("no verifying test symbol in a .spec.cjs file; got %+v", mod.Symbols)
	}
}

const playwrightSample = `import { test, expect } from '@playwright/test';

test.use({ locale: 'en-US' });
test.beforeEach(async ({ page }) => { await page.goto('/'); });

test.describe('Sign in', () => {
  test.describe.configure({ mode: 'serial' });
  test.skip(({ browserName }) => browserName === 'firefox', 'flaky there');

  /** @verifies auth:INV-1 */
  test('signs in with a code', async ({ page }) => {
    await test.step('enter the code', async () => {});
  });

  test.describe.serial('expiry', () => {
    /** @verifies auth:INV-2 */
    test('rejects an expired code', async ({ page }) => {});
  });

  test.fixme('remembers the device', async ({ page }) => {});
});

test.describe(() => {
  test('anonymous group case', async ({ page }) => {});
});
`

// TestPlaywrightSpec: test.describe(...) and its .serial/.parallel forms are
// groups whose bodies hold the real tests; hooks, test.use, configure and
// untitled test.skip(cond) are not tests.
func TestPlaywrightSpec(t *testing.T) {
	mod, err := New().Parse(context.Background(), "e2e/auth/signin.spec.ts", []byte(playwrightSample))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := map[string]string{} // name -> FQN
	verified := map[string]string{}
	for _, s := range mod.Symbols {
		if !s.IsTest {
			continue
		}
		got[s.Name] = s.FQN
		for _, a := range s.Annotations {
			if a.Kind == "verifies" && len(a.Args) == 1 {
				verified[s.Name], _ = a.Args[0].(string)
			}
		}
	}
	want := map[string]string{
		"signs in with a code":    "e2e.auth.signin.spec.test.signs-in-with-a-code",
		"rejects an expired code": "e2e.auth.signin.spec.test.rejects-an-expired-code",
		"remembers the device":    "e2e.auth.signin.spec.test.remembers-the-device",
		"anonymous group case":    "e2e.auth.signin.spec.test.anonymous-group-case",
	}
	for name, fqn := range want {
		if got[name] != fqn {
			t.Errorf("test %q: FQN %q, want %q", name, got[name], fqn)
		}
	}
	if len(got) != len(want) {
		t.Errorf("test symbols = %v, want exactly %v", got, want)
	}
	if verified["signs in with a code"] != "auth:INV-1" || verified["rejects an expired code"] != "auth:INV-2" {
		t.Errorf("@verifies not attached inside test.describe: %v", verified)
	}
}

const tsxComponentSample = `import { useState } from 'react';

/** @feature kids.profiles */
export function ProfilePicker({ kids }: { kids: string[] }) {
  const [sel, setSel] = useState<string | null>(null);
  return <ul>{kids.map((k) => <li key={k} onClick={() => setSel(k)}>{k}</li>)}</ul>;
}

/** @capability kids.profiles:pick */
export const KidBadge = ({ name }: { name: string }) => <span className="badge">{name}</span>;
`

// TestTSXComponents: a .tsx file is parsed with the tsx grammar, so React
// function components are symbols and their annotations attach.
func TestTSXComponents(t *testing.T) {
	mod, err := New().Parse(context.Background(), "src/kid/ProfilePicker.tsx", []byte(tsxComponentSample))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := map[string]string{}
	for _, s := range mod.Symbols {
		for _, a := range s.Annotations {
			got[s.Name] = a.Kind
		}
	}
	if got["ProfilePicker"] != "feature" || got["KidBadge"] != "capability" {
		t.Errorf("component annotations = %v, want ProfilePicker:feature KidBadge:capability", got)
	}
	if len(mod.Diagnostics) != 0 {
		t.Errorf("unexpected diagnostics: %+v", mod.Diagnostics)
	}
}

// TestTSStillParsesTypeAssertions: .ts keeps the TypeScript grammar, where
// <T>expr is a type assertion rather than JSX.
func TestTSStillParsesTypeAssertions(t *testing.T) {
	src := "/** @feature cart */\nexport function total(x: unknown) { return <number>x + 1; }\n"
	mod, err := New().Parse(context.Background(), "src/cart.ts", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(mod.Symbols) != 1 || mod.Symbols[0].Name != "total" || len(mod.Symbols[0].Annotations) != 1 {
		t.Errorf("symbols = %+v", mod.Symbols)
	}
}

// TestOrphanedAnnotations: an annotation that attaches to no symbol is
// reported instead of silently dropped.
// @verifies adapters.typescript:INV-1
func TestOrphanedAnnotations(t *testing.T) {
	src := `/** @feature cart */
export default 42;

/** @verifies cart:INV-1 */
console.log('not a test');

/** @capability cart:total */
export function total() { return 1; }

/** @enforces cart:INV-2 */
`
	mod, err := New().Parse(context.Background(), "src/cart.ts", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var lines []int
	for _, d := range mod.Diagnostics {
		if d.Code != "ANNOTATION_ORPHANED" {
			t.Errorf("unexpected diagnostic %+v", d)
		}
		lines = append(lines, d.Line)
	}
	if len(lines) != 3 || lines[0] != 2 || lines[1] != 5 || lines[2] != 10 {
		t.Errorf("orphan lines = %v, want [2 5 10] (diagnostics %+v)", lines, mod.Diagnostics)
	}
	if len(mod.Symbols) != 1 || mod.Symbols[0].Name != "total" || len(mod.Symbols[0].Annotations) != 1 {
		t.Errorf("total should keep its own annotation only: %+v", mod.Symbols)
	}
}

// TestSymbolEndLine: symbols carry the last line of their declaration, so
// length rules measure the declaration itself.
func TestSymbolEndLine(t *testing.T) {
	src := "export function a() {\n  return 1;\n}\n\nexport const b = () => {\n  return 2;\n};\n"
	mod, err := New().Parse(context.Background(), "src/x.ts", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := map[string][2]int{"a": {1, 3}, "b": {5, 7}}
	for _, s := range mod.Symbols {
		if w, ok := want[s.Name]; ok && (s.Line != w[0] || s.EndLine != w[1]) {
			t.Errorf("%s: lines %d-%d, want %d-%d", s.Name, s.Line, s.EndLine, w[0], w[1])
		}
	}
}

const annotationTargetsSample = `// @module-feature parent.kids
import { createClient } from '@supabase/supabase-js';

/** @capability parent.kids:connect */
export const supabase = createClient('url', 'key');

export const LIMITS = { max: 3 };

export const storage = {
  /** @enforces parent.kids:INV-1 */
  get(key: string) { return key; },
  /** @capability parent.kids:clear */
  clear: () => {},
  plain() {},
};

/** @capability parent.kids:list */
export function KidsTab() {
  /** @capability parent.kids:unlink */
  const unlink = () => {};
  const untracked = () => {};
  /** @enforces parent.kids:INV-2 */
  function confirmUnlink() {}
  /** @enforces parent.kids:INV-3 */
  const n = 1;
  return (
    <ul>
      <li onClick={unlink}>x</li>
    </ul>
  );
}
`

// TestAnnotationTargets: annotated value consts, object-literal methods and
// functions nested in a component are symbols; one-line module tags work;
// an annotation on a nested non-function is reported, not dropped.
func TestAnnotationTargets(t *testing.T) {
	mod, err := New().Parse(context.Background(), "src/parent/KidsTab.tsx", []byte(annotationTargetsSample))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(mod.ModuleAnnotations) != 1 || mod.ModuleAnnotations[0].Kind != "module_feature" {
		t.Errorf("module annotations = %+v, want one module_feature from the line comment", mod.ModuleAnnotations)
	}
	got := map[string]string{}
	for _, s := range mod.Symbols {
		kinds := ""
		for _, a := range s.Annotations {
			kinds += a.Kind + ";"
		}
		got[s.FQN] = string(s.Kind) + ":" + kinds
	}
	base := "src.parent.KidsTab."
	want := map[string]string{
		base + "supabase":              "const:capability;",
		base + "storage":               "const:",
		base + "storage.get":           "method:enforces_invariant;",
		base + "storage.clear":         "method:capability;",
		base + "KidsTab":               "function:capability;",
		base + "KidsTab.unlink":        "function:capability;",
		base + "KidsTab.confirmUnlink": "function:enforces_invariant;",
	}
	for fqn, w := range want {
		if got[fqn] != w {
			t.Errorf("%s = %q, want %q", fqn, got[fqn], w)
		}
	}
	for _, absent := range []string{base + "LIMITS", base + "storage.plain", base + "KidsTab.untracked", base + "KidsTab.n"} {
		if _, ok := got[absent]; ok {
			t.Errorf("%s should not be a symbol", absent)
		}
	}
	if len(mod.Diagnostics) != 1 || mod.Diagnostics[0].Code != "ANNOTATION_ORPHANED" {
		t.Errorf("want one orphan (the @enforces on const n), got %+v", mod.Diagnostics)
	}
}

// A line comment never carries symbol tags: a stray // @capability must not
// re-attribute the declaration below it.
// @verifies adapters.typescript:INV-2
func TestLineCommentIgnoresSymbolTags(t *testing.T) {
	src := "// @capability cart:total\nexport function total() { return 1; }\n"
	mod, err := New().Parse(context.Background(), "src/cart.ts", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(mod.Symbols) != 1 || len(mod.Symbols[0].Annotations) != 0 {
		t.Errorf("symbols = %+v", mod.Symbols)
	}
}

// TestEachTests: it.each / test.each / describe.each are tests; their FQN
// marks each title placeholder with "_" so results can join every row.
// @verifies adapters.typescript:INV-4
func TestEachTests(t *testing.T) {
	src := `describe('library', () => {
  /** @verifies parent.library_ui:INV-1 */
  it.each(['kid_profiles', 'feeds'])('fails as a whole when the %s read fails', async (table) => {});
  test.each([[1, 2]])('$a plus $b', (a, b) => {});
  describe.each([['x']])('group %s', (g) => {
    it('inner case', () => {});
  });
});
`
	mod, err := New().Parse(context.Background(), "src/parent/data.test.ts", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := map[string]int{}
	for _, s := range mod.Symbols {
		got[s.FQN] = len(s.Annotations)
	}
	base := "src.parent.data.test.test."
	for fqn, anns := range map[string]int{
		base + "fails-as-a-whole-when-the-_-read-fails": 1,
		base + "_-plus-_":   0,
		base + "inner-case": 0,
	} {
		if n, ok := got[fqn]; !ok || n != anns {
			t.Errorf("%s: present=%v annotations=%d, want %d (symbols %v)", fqn, ok, n, anns, got)
		}
	}
	if len(mod.Diagnostics) != 0 {
		t.Errorf("unexpected diagnostics %+v", mod.Diagnostics)
	}
}

func TestIsTestFile(t *testing.T) {
	for path, want := range map[string]bool{
		"supabase/functions/add-source/url_test.ts": true, // Deno
		"src/cart.test.ts":                          true,
		"e2e/login.spec.cjs":                        true,
		"src/latest.ts":                             false,
		"src/contest.ts":                            false,
		"src/test_utils.ts":                         true, // test-support name
	} {
		if got := isTestFile(path); got != want {
			t.Errorf("isTestFile(%q) = %v, want %v", path, got, want)
		}
	}
}

// A file has top-level code when something runs at load: a render call or a
// plain value const. Imports, type declarations, directives and re-exports
// run nothing, and a test file never counts.
// @verifies adapters.typescript:INV-5
func TestHasTopLevelCode(t *testing.T) {
	for _, tc := range []struct {
		path, src string
		want      bool
	}{
		{"src/main.tsx", "/** @module-feature app.shell */\nimport { createRoot } from 'react-dom/client';\ncreateRoot(document.body).render(<App />);\n", true},
		{"src/i18n.ts", "export const en = { hello: 'Hello' };\n", true},
		{"src/default.ts", "export default { retries: 3 };\n", true},
		{"src/types.ts", "'use client';\nimport type { X } from './x';\nexport type Id = string;\nexport interface Row { id: Id }\ndeclare const VERSION: string;\n", false},
		{"src/index.ts", "export { total } from './cart';\nexport * from './pay';\n", false},
		{"src/cart.test.ts", "import { it } from 'vitest';\nit('adds', () => {});\n", false},
	} {
		mod, err := New().Parse(context.Background(), tc.path, []byte(tc.src))
		if err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		if mod.HasCode != tc.want {
			t.Errorf("%s: HasCode = %v, want %v", tc.path, mod.HasCode, tc.want)
		}
	}
}

// TestParameterizedTests: a test whose title is not a plain string literal is
// one test per call site, named by its title template with the placeholders
// as written: a template literal in nested loops, test.each with an array,
// it.each with a tagged-template table, describe.each, a forEach callback
// and a brace-less for loop. Annotations above the call attach as they do to
// a plain test, and a second call site with the same template gets the
// file's duplicate suffix.
// @verifies adapters.typescript:INV-6
func TestParameterizedTests(t *testing.T) {
	src := "import { test, expect } from '@playwright/test';\n" +
		"const LANGS = [{ name: 'ar' }, { name: 'en' }];\n" +
		"const VIEWS = [{ name: 'mobile' }, { name: 'desktop' }];\n" +
		"\n" +
		"test.describe('shop header', () => {\n" +
		"  for (const lang of LANGS) {\n" +
		"    for (const view of VIEWS) {\n" +
		"      /** @verifies shop:INV-1 */\n" +
		"      test(`header fits on ${view.name}, ${lang.name}`, async ({ page }) => {});\n" +
		"    }\n" +
		"  }\n" +
		"});\n" +
		"\n" +
		"describe('fees', () => {\n" +
		"  /** @verifies fees:INV-1#rounding */\n" +
		"  test.each(cases)('rounds %s to %s', (a, b) => {});\n" +
		"  /** @verifies fees:INV-2 */\n" +
		"  it.each`\n" +
		"    a    | b\n" +
		"    ${1} | ${2}\n" +
		"  `('adds $a to $b', ({ a, b }) => {});\n" +
		"  describe.each([['cart'], ['wallet']])('suite %s', (kind) => {\n" +
		"    test('applies the fee', () => {});\n" +
		"  });\n" +
		"  /** @verifies fees:INV-3 */\n" +
		"  [1, 2].forEach((n) => it(`charges ${n} fees`, () => {}));\n" +
		"  CASES.forEach((c) => {\n" +
		"    /** @verifies fees:INV-4 */\n" +
		"    it(`waives fees for ${c.name}`, () => {});\n" +
		"  });\n" +
		"  for (const c of CASES) test(`caps ${c.name}`, () => {});\n" +
		"  for (let i = 0; i < 2; i++) {\n" +
		"    test(`caps ${CASES[i].name}`, () => {});\n" +
		"  }\n" +
		"  test(`keeps \\`cart\\` totals`, () => {});\n" +
		"  test('plain title', () => {});\n" +
		"});\n"
	mod, err := New().Parse(context.Background(), "e2e/shop/fees.spec.ts", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	type sym struct {
		name          string
		parameterized bool
		verifies      string
	}
	got := map[string]sym{}
	for _, s := range mod.Symbols {
		if !s.IsTest {
			continue
		}
		v := ""
		for _, a := range s.Annotations {
			if a.Kind == "verifies" && len(a.Args) == 1 {
				v, _ = a.Args[0].(string)
			}
		}
		got[s.FQN] = sym{s.Name, s.Parameterized, v}
	}
	base := "e2e.shop.fees.spec.test."
	want := map[string]sym{
		base + "header-fits-on-_-_": {"header fits on ${view.name}, ${lang.name}", true, "shop:INV-1"},
		base + "rounds-_-to-_":      {"rounds %s to %s", true, "fees:INV-1#rounding"},
		base + "adds-_-to-_":        {"adds $a to $b", true, "fees:INV-2"},
		base + "applies-the-fee":    {"applies the fee", false, ""},
		base + "charges-_-fees":     {"charges ${n} fees", true, "fees:INV-3"},
		base + "waives-fees-for-_":  {"waives fees for ${c.name}", true, "fees:INV-4"},
		base + "caps-_":             {"caps ${c.name}", true, ""},
		base + "caps-__2":           {"caps ${CASES[i].name}", true, ""},
		base + "keeps-cart-totals":  {"keeps `cart` totals", false, ""},
		base + "plain-title":        {"plain title", false, ""},
	}
	for fqn, w := range want {
		if g, ok := got[fqn]; !ok || g != w {
			t.Errorf("%s: got %+v (present %v), want %+v", fqn, g, ok, w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("test symbols = %v, want exactly %v", got, want)
	}
	if len(mod.Diagnostics) != 0 {
		t.Errorf("unexpected diagnostics %+v", mod.Diagnostics)
	}
}

// TestParameterizedTestOrphans: a tag inside a test file's loop body that
// reaches no test call is orphaned, as anywhere else; a declaration in a loop
// body is not a module symbol.
// @verifies adapters.typescript:INV-1
func TestParameterizedTestOrphans(t *testing.T) {
	src := "for (const c of CASES) {\n" +
		"  /** @verifies fees:INV-1 */\n" +
		"  const total = c.amount;\n" +
		"  function helper() {}\n" +
		"}\n"
	mod, err := New().Parse(context.Background(), "src/fees.test.ts", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(mod.Symbols) != 0 {
		t.Errorf("symbols = %+v, want none", mod.Symbols)
	}
	if len(mod.Diagnostics) != 1 || mod.Diagnostics[0].Line != 3 {
		t.Errorf("diagnostics = %+v, want one ANNOTATION_ORPHANED on line 3", mod.Diagnostics)
	}
	// Outside a test file, loop bodies and forEach callbacks are not read.
	prod := src + "ROUTES.forEach((r) => {\n  /** @feature fees */\n  register(r);\n});\n"
	mod, err = New().Parse(context.Background(), "src/fees.ts", []byte(prod))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(mod.Symbols) != 0 || len(mod.Diagnostics) != 0 {
		t.Errorf("production file: symbols %+v, diagnostics %+v, want none", mod.Symbols, mod.Diagnostics)
	}
}

// TestRecordsHTTPRefs: a literal request is a ref of kind http keyed by
// method and path; a computed URL or method is one with an empty key; and an
// axios call sends a request without registering a route.
// @verifies reachability:INV-11
func TestRecordsHTTPRefs(t *testing.T) {
	src := "export function calls(base: string, m: string) {\n" +
		"  fetch('/api/orders');\n" +
		"  fetch('https://svc.example.com/api/orders/?x=1', { method: 'post' });\n" +
		"  fetch(`/api/plain`);\n" +
		"  fetch(`${base}/api/orders`);\n" +
		"  fetch('/api/orders', { method: m });\n" +
		"  fetch('/api/orders', { ...opts });\n" +
		"  axios.delete('/api/orders/1');\n" +
		"  axios({ url: '/api/x', method: 'patch' });\n" +
		"  axios.get('/api/axios');\n" +
		"  supabase.functions.invoke('charge');\n" +
		"  cache.get('/not-a-request');\n" +
		"}\n"
	mod, err := New().Parse(context.Background(), "web/calls.ts", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range mod.Symbols {
		for _, r := range s.Refs {
			if r.Kind == "http" {
				got = append(got, r.Name+"|"+r.Qualifier)
			}
		}
	}
	want := []string{
		"GET /api/orders|",
		"POST /api/orders|https://svc.example.com",
		"GET /api/plain|",
		"|", "|", "|", // computed URL, computed method, a spread that may set the method
		"DELETE /api/orders/1|",
		"PATCH /api/x|",
		"GET /api/axios|",
		"POST /functions/v1/charge|",
	}
	if len(got) != len(want) {
		t.Fatalf("http refs %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ref %d = %q, want %q", i, got[i], want[i])
		}
	}
	for _, s := range mod.Surfaces {
		t.Errorf("a request registers no route: %+v", s)
	}
}

// A top-level axios.get('/path', config) sends a request; it is not a route
// registration, though app.get('/path', handler) is.
// @verifies adapters.typescript:INV-3
func TestAxiosCallIsNotARoute(t *testing.T) {
	src := "const config = {};\naxios.get('/api/orders', config);\napp.get('/api/orders', config);\n"
	mod, err := New().Parse(context.Background(), "src/x.ts", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.Surfaces) != 1 || mod.Surfaces[0].Method != "GET" || mod.Surfaces[0].Line != 3 {
		t.Errorf("surfaces = %+v, want only the app.get on line 3", mod.Surfaces)
	}
}

// A reference keyed by a literal records where the literal is: the byte span,
// quotes included, of the string that carries the name. A computed URL, a
// reference with no name and a literal that is not the name records none.
// @verifies reachability:INV-11
func TestRecordsLiteralSpans(t *testing.T) {
	src := "export function calls(base: string, db: any) {\n" +
		"  fetch('/api/orders');\n" +
		"  fetch(`/api/plain`, { method: 'PUT' });\n" +
		"  axios.get(\"/api/axios\");\n" +
		"  axios({ url: '/api/cfg', method: 'patch' });\n" +
		"  supabase.functions.invoke('charge');\n" +
		"  db.rpc('refund_order', { id: 1 });\n" +
		"  db.from('orders').select();\n" +
		"  fetch(`${base}/computed`);\n" +
		"  db.rpc(name);\n" +
		"}\n"
	mod, err := New().Parse(context.Background(), "web/calls.ts", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range mod.Symbols {
		for _, r := range s.Refs {
			switch r.Kind {
			case "http", "rpc", "table":
				lit := ""
				if r.HasLiteral() {
					lit = src[r.LitStart:r.LitEnd]
				}
				got = append(got, r.Kind+"|"+r.Name+"|"+lit)
			}
		}
	}
	want := []string{
		"http|GET /api/orders|'/api/orders'",
		"http|PUT /api/plain|`/api/plain`",
		"http|GET /api/axios|\"/api/axios\"",
		"http|PATCH /api/cfg|'/api/cfg'",
		"http|POST /functions/v1/charge|'charge'",
		"rpc|refund_order|'refund_order'",
		"table|orders|'orders'",
		"http||", // computed
		"rpc||",  // a name that is not a literal
	}
	if len(got) != len(want) {
		t.Fatalf("refs %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ref %d = %q, want %q", i, got[i], want[i])
		}
	}
}
