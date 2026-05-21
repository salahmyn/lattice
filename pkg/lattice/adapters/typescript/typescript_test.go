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
