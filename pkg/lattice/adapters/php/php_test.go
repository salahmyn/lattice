package php

import (
	"context"
	"testing"
)

const phpSample = `<?php

namespace App\Checkout\Refund;

use Lattice\Attributes\{Feature, Capability, EnforcesInvariant};

#[Feature('checkout.refund')]
#[Capability('self_service_refund', 'wallet_refund_destination')]
#[EnforcesInvariant('INV-2', 'INV-3')]
final class RefundService
{
    #[EnforcesInvariant('INV-1')]
    public function validateAmount(string $orderId): bool
    {
        return true;
    }
}
`

func TestParsePHP(t *testing.T) {
	mod, err := New().Parse(context.Background(), "src/Checkout/Refund/RefundService.php", []byte(phpSample))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	var class, method bool
	for _, s := range mod.Symbols {
		if s.Name == "RefundService" {
			class = true
			if s.FQN != "App\\Checkout\\Refund\\RefundService" {
				t.Errorf("class FQN = %q", s.FQN)
			}
			if len(s.Annotations) != 3 {
				t.Errorf("class annotations: got %d want 3", len(s.Annotations))
			}
		}
		if s.Name == "validateAmount" {
			method = true
			if s.FQN != "App\\Checkout\\Refund\\RefundService::validateAmount" {
				t.Errorf("method FQN = %q", s.FQN)
			}
			if len(s.Annotations) != 1 || s.Annotations[0].Kind != "enforces_invariant" {
				t.Errorf("method annotations = %v", s.Annotations)
			}
		}
	}
	if !class {
		t.Error("RefundService class not found")
	}
	if !method {
		t.Error("validateAmount method not found")
	}
}
