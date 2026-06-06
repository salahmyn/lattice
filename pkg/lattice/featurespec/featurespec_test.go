package featurespec

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// minimal returns a feature with only the required fields populated,
// so each test can layer on exactly what it cares about.
func minimal() schema.Manifest {
	return schema.Manifest{
		ID: "checkout.refund", Version: 1, Status: schema.StatusProduction,
		Purpose: "Customer-initiated refunds for orders in the return window.",
		Owners:  schema.Owners{Business: "payments", Engineering: "checkout-eng"},
	}
}

func TestRenderTitleAndPurpose(t *testing.T) {
	spec := Render(minimal())
	if !strings.HasPrefix(spec, "# checkout.refund\n\n") {
		t.Errorf("missing title header: %q", spec[:50])
	}
	if !strings.Contains(spec, "> Customer-initiated refunds") {
		t.Errorf("missing purpose blockquote: %q", spec)
	}
}

func TestRenderOmitsEmptySections(t *testing.T) {
	// Minimal feature has no invariants/errors/capabilities/surface —
	// none of those sections should appear. AMA spec is meant to be
	// scannable; empty headers would just pad token count.
	spec := Render(minimal())
	for _, header := range []string{"## Inputs", "## Outputs", "## System Side Effects", "## Invariants", "## Errors", "## Capabilities"} {
		if strings.Contains(spec, header) {
			t.Errorf("empty section %q should not appear: %q", header, spec)
		}
	}
}

func TestRenderInputs(t *testing.T) {
	m := minimal()
	m.Surface = []schema.Surface{
		{Type: schema.SurfaceHTTP, Method: "POST", Path: "/api/refunds", RequestSchema: "CreateRefundRequest"},
		{Type: schema.SurfaceScheduled, Schedule: "0 * * * *", Job: "refund.retry"},
	}
	spec := Render(m)
	if !strings.Contains(spec, "## Inputs") {
		t.Fatal("missing Inputs header")
	}
	if !strings.Contains(spec, "POST /api/refunds → CreateRefundRequest") {
		t.Errorf("missing HTTP input line: %q", spec)
	}
	if !strings.Contains(spec, "cron 0 * * * * — refund.retry") {
		t.Errorf("missing scheduled input line: %q", spec)
	}
}

func TestRenderEventEmitInSideEffects(t *testing.T) {
	m := minimal()
	m.Surface = []schema.Surface{
		{Type: schema.SurfaceEventEmit, Name: "refund.completed.v1", Semantics: "at-least-once"},
	}
	spec := Render(m)
	if !strings.Contains(spec, "## Outputs") {
		t.Error("event emit should appear under Outputs")
	}
	if !strings.Contains(spec, "## System Side Effects") {
		t.Error("event emit should also appear under System Side Effects")
	}
	if !strings.Contains(spec, "emits: refund.completed.v1") {
		t.Errorf("missing emits ledger: %q", spec)
	}
}

func TestRenderInvariantsAndErrors(t *testing.T) {
	m := minimal()
	m.Invariants = []schema.Invariant{
		{ID: "INV-1", Statement: "refund amount\nnever exceeds the original charge"},
	}
	m.Errors = []schema.ErrorDecl{
		{Code: "REFUND_OUTSIDE_WINDOW", Status: 409, Description: "Order is past the return window."},
	}
	spec := Render(m)
	// oneLine should collapse the multi-line invariant statement.
	if !strings.Contains(spec, "INV-1: refund amount never exceeds the original charge") {
		t.Errorf("invariant statement not collapsed: %q", spec)
	}
	if !strings.Contains(spec, "- REFUND_OUTSIDE_WINDOW (409)") {
		t.Errorf("missing error code line: %q", spec)
	}
}

func TestRenderCapabilitiesIncludesKindWhenSet(t *testing.T) {
	m := minimal()
	m.Capabilities = []schema.Capability{
		{ID: "issue_refund", Kind: schema.CapabilityCommand, Summary: "Issues the refund."},
		{ID: "lookup_status", Kind: schema.CapabilityQuery, Summary: "Returns current status."},
		{ID: "legacy", Summary: "A legacy capability without a kind."}, // blank → mixed (no annotation in output)
	}
	spec := Render(m)
	if !strings.Contains(spec, "- issue_refund [command]") {
		t.Errorf("command kind should annotate the capability: %q", spec)
	}
	if !strings.Contains(spec, "- lookup_status [query]") {
		t.Errorf("query kind should annotate the capability: %q", spec)
	}
	if strings.Contains(spec, "- legacy [mixed]") {
		t.Errorf("mixed (the default) should NOT annotate: %q", spec)
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	m := minimal()
	m.Surface = []schema.Surface{
		{Type: schema.SurfaceEventEmit, Name: "b.event"},
		{Type: schema.SurfaceEventEmit, Name: "a.event"},
		{Type: schema.SurfaceEventEmit, Name: "c.event"},
	}
	a := Render(m)
	b := Render(m)
	if a != b {
		t.Error("Render should be byte-stable for identical manifests")
	}
	// And sorted — alpha order so re-runs don't churn.
	if !strings.Contains(a, "emits: a.event, b.event, c.event") {
		t.Errorf("emits not alpha-sorted: %q", a)
	}
}

func TestWordCount(t *testing.T) {
	// Whitespace-separated runs — markdown punctuation (`#`) counts.
	// That matches how an LLM tokenizes the spec, which is the right
	// signal for the AMA word cap.
	if got := WordCount("# foo\n\nbar baz qux"); got != 5 {
		t.Errorf("WordCount = %d, want 5", got)
	}
	if got := WordCount(""); got != 0 {
		t.Errorf("empty WordCount = %d, want 0", got)
	}
}

func TestSmallFeatureFitsCap(t *testing.T) {
	// The minimal feature should be well under 500 words.
	if w := WordCount(Render(minimal())); w >= WordCap {
		t.Errorf("minimal feature spec is %d words, expected well under %d", w, WordCap)
	}
}
