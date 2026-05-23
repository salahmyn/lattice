package entrypoints

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// TestTraceModuleProximity covers the dominant Laravel pattern: an HTTP
// route handler in Modules/Accounts/Http/Controllers/X reaches a feature
// whose implementations live in Modules/Accounts/Actions/Y. The tracer
// connects them by enclosing module without needing call-graph data.
func TestTraceModuleProximity(t *testing.T) {
	eps := []schema.EntryPoint{{
		ID: "ep.http.post.accounts.consent",
		Trigger: schema.Trigger{Method: "POST", Path: "/api/accounts/consent"},
		Handler: schema.Handler{
			Symbol: `Modules\Accounts\Http\Controllers\ConsentController::store`,
			File:   "modules/Accounts/Http/Controllers/ConsentController.php",
		},
	}}
	features := []schema.Manifest{
		{
			ID: "accounts.consent_actions",
			Capabilities: []schema.Capability{
				{ID: "accept_consent", Summary: "User accepts a consent request."},
				{ID: "reject_consent", Summary: "User rejects a consent request."},
			},
			Implementations: []schema.Implementation{{
				Symbol: `Modules\Accounts\Actions\Consent\AcceptConsentAction::handle`,
				File:   "modules/Accounts/Actions/Consent/AcceptConsentAction.php",
			}},
		},
		// Same-class match — bug-tracker pattern: handler IS the impl.
		{
			ID: "accounts.controller_self",
			Capabilities: []schema.Capability{{ID: "store", Summary: "Persist a consent."}},
			Implementations: []schema.Implementation{{
				Symbol: `Modules\Accounts\Http\Controllers\ConsentController::store`,
				File:   "modules/Accounts/Http/Controllers/ConsentController.php",
			}},
		},
		// Unrelated module — must not match.
		{
			ID: "webhook.failed",
			Implementations: []schema.Implementation{{
				Symbol: `Modules\Webhook\Notifications\X::toMail`,
				File:   "modules/Webhook/Notifications/X.php",
			}},
		},
	}

	traced := Trace(eps, features)
	if len(traced) != 1 {
		t.Fatalf("want 1 ep, got %d", len(traced))
	}
	flow := traced[0].Flow
	if len(flow) != 2 {
		t.Fatalf("want 2 flow steps (accounts.consent_actions + accounts.controller_self), got %d: %+v",
			len(flow), flow)
	}
	have := map[string]bool{}
	for _, s := range flow {
		have[s.Feature] = true
	}
	if !have["accounts.consent_actions"] || !have["accounts.controller_self"] {
		t.Errorf("missing expected features in flow: %+v", flow)
	}
	if have["webhook.failed"] {
		t.Errorf("unrelated webhook feature must not appear in flow")
	}
}

// TestTraceLeavesOrphanFlowsEmpty proves we don't fabricate flows when
// no feature is near — the verify-time UNCLASSIFIED_ENTRY_POINT warning
// is what surfaces orphan triggers, not a fake flow step.
func TestTraceLeavesOrphanFlowsEmpty(t *testing.T) {
	eps := []schema.EntryPoint{{
		ID: "ep.http.get.misc",
		Handler: schema.Handler{
			Symbol: `App\Http\Controllers\MiscController::ping`,
			File:   "app/Http/Controllers/MiscController.php",
		},
	}}
	features := []schema.Manifest{{
		ID: "billing.refunds",
		Implementations: []schema.Implementation{{
			Symbol: `Modules\Billing\Actions\Refund::handle`,
			File:   "modules/Billing/Actions/Refund.php",
		}},
	}}
	traced := Trace(eps, features)
	if len(traced[0].Flow) != 0 {
		t.Errorf("expected empty flow for unrelated handler, got %+v", traced[0].Flow)
	}
}
