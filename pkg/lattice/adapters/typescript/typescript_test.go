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
