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
func TestOrphanedAnnotations(t *testing.T) {
	src := `/** @feature cart */
export const LIMITS = { max: 3 };

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
