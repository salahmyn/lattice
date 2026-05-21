package python

import (
	"context"
	"testing"
)

const sampleSrc = `module_feature("checkout.refund.settlement")
module_enforces_invariant("INV-12", "INV-13")

@feature("checkout.refund", capability="self_service_refund")
@enforces_invariant("INV-1", "INV-2")
def validate_amount(order_id, amount):
    return True


@feature("checkout.refund")
class RefundService:
    @enforces_invariant("INV-5")
    def process(self, refund):
        pass


class FixtureRefundProcessor(RefundService):
    pass
`

func TestParseSymbolsAndAnnotations(t *testing.T) {
	a := New()
	mod, err := a.Parse(context.Background(), "src/checkout/refund/service.py", []byte(sampleSrc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(mod.ModuleAnnotations) != 2 {
		t.Fatalf("module annotations: got %d want 2", len(mod.ModuleAnnotations))
	}

	byName := map[string]int{}
	for _, s := range mod.Symbols {
		byName[s.Name]++
	}
	for _, want := range []string{"validate_amount", "RefundService", "process", "FixtureRefundProcessor"} {
		if byName[want] == 0 {
			t.Errorf("missing symbol %q", want)
		}
	}

	var vf bool
	for _, s := range mod.Symbols {
		if s.Name == "validate_amount" {
			vf = true
			if len(s.Annotations) != 2 {
				t.Errorf("validate_amount annotations: got %d want 2", len(s.Annotations))
			}
			if s.Annotations[0].Kind != "feature" {
				t.Errorf("first annotation kind = %q", s.Annotations[0].Kind)
			}
			if cap, ok := s.Annotations[0].Kwargs["capability"]; !ok || cap != "self_service_refund" {
				t.Errorf("capability kwarg = %v", cap)
			}
		}
		if s.Name == "FixtureRefundProcessor" {
			if len(s.BaseClasses) != 1 || s.BaseClasses[0] != "src.checkout.refund.service.RefundService" {
				t.Errorf("base classes = %v", s.BaseClasses)
			}
		}
	}
	if !vf {
		t.Error("validate_amount not found")
	}
}

func TestParseNonLiteralDiagnostic(t *testing.T) {
	src := "SOME_CONST = 1\n\n@feature(SOME_CONST)\ndef f():\n    pass\n"
	mod, err := New().Parse(context.Background(), "src/x.py", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(mod.Diagnostics) == 0 {
		t.Fatal("expected a non-literal-argument diagnostic")
	}
	if mod.Diagnostics[0].Code != "ANNOTATION_ARG_NOT_LITERAL" {
		t.Errorf("diagnostic code = %q", mod.Diagnostics[0].Code)
	}
}
