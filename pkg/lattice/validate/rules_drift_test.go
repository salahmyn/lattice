package validate

import (
	"reflect"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// driftGraph: feature fees (proposal, three invariants, one mapped, code in
// the graph) on the flow of a production entry point; wallet (accepted, code,
// on no flow); ledger (proposal, no code, on no flow).
func driftGraph() schema.KnowledgeGraph {
	feat := func(id string, st schema.Status, invs ...string) schema.Manifest {
		m := schema.Manifest{ID: id, Version: 1, Status: st, Purpose: id, SourcePath: "features/" + id + ".yaml"}
		for _, i := range invs {
			m.Invariants = append(m.Invariants, schema.Invariant{ID: i, Statement: i})
		}
		return m
	}
	return schema.KnowledgeGraph{
		BRDs: []schema.BRD{{ID: "brd.fees", Status: schema.BRDDraft, SuccessCriteria: []schema.BRDCriterion{
			{ID: "SC-1", MapsToInvariant: schema.InvariantRefs{"fees:INV-1"}}}}},
		Features: []schema.Manifest{
			feat("fees", schema.StatusProposal, "INV-1", "INV-2", "INV-3"),
			feat("wallet", schema.StatusAccepted, "INV-1"),
			feat("ledger", schema.StatusProposal),
			feat("orders", schema.StatusProduction),
		},
		Symbols: []schema.GraphSymbol{
			{FQN: "src.fees.charge", File: "src/fees.ts", Feature: "fees"},
			{FQN: "src.wallet.top_up", File: "src/wallet.ts", Feature: "wallet"},
			{FQN: "src.wallet.top_up_test", File: "src/wallet.test.ts", Feature: "ledger", IsTest: true},
		},
		EntryPoints: []schema.EntryPoint{
			{ID: "orders.route", Status: schema.StatusProduction, Kind: schema.EntryPointKindHTTP,
				Flow: []schema.FlowStep{{Feature: "orders"}, {Feature: "fees"}}},
			{ID: "orders.beta", Status: schema.StatusProposal, Kind: schema.EntryPointKindHTTP,
				Flow: []schema.FlowStep{{Feature: "wallet"}}},
		},
	}
}

func driftByCode(v []schema.Violation, code string) []schema.Violation {
	var out []schema.Violation
	for _, x := range v {
		if x.Code == code {
			out = append(out, x)
		}
	}
	return out
}

// One finding per feature, listing every invariant no criterion maps, and an
// invariant no block carries counts. Criteria of a draft BRD count as mapped.
// @verifies validation:INV-18
func TestInvariantUnrequired(t *testing.T) {
	got := driftByCode(Validate(driftGraph(), config.Default(), Options{}), schema.CodeInvariantUnrequired)
	if len(got) != 2 {
		t.Fatalf("findings = %+v, want one for fees and one for wallet", got)
	}
	by := map[string]schema.Violation{}
	for _, f := range got {
		by[f.FeatureID] = f
		if f.Severity != schema.SeverityInfo {
			t.Errorf("%s severity = %s", f.FeatureID, f.Severity)
		}
	}
	f := by["fees"]
	if !reflect.DeepEqual(f.Refs, []string{"fees:INV-2", "fees:INV-3"}) ||
		!strings.Contains(f.Message, "fees:INV-2, fees:INV-3") || !strings.Contains(f.Message, "mapped by no criterion") {
		t.Errorf("fees finding = %+v", f)
	}
	if !reflect.DeepEqual(by["wallet"].Refs, []string{"wallet:INV-1"}) {
		t.Errorf("wallet finding = %+v", by["wallet"])
	}
	if strings.Contains(strings.ToLower(f.Message), "unimportant") {
		t.Errorf("message judges importance: %q", f.Message)
	}
}

// An allowlist entry accepts one feature:INV: the others stay open, and an
// entry that matches nothing is stale.
// @verifies validation:INV-18
func TestInvariantUnrequiredAllowlistPerRef(t *testing.T) {
	v := driftByCode(Validate(driftGraph(), config.Default(), Options{}), schema.CodeInvariantUnrequired)
	al := Allowlist{Path: "allowlist.yaml", Entries: []AllowEntry{
		{Code: schema.CodeInvariantUnrequired, Subject: "fees:INV-2", Reason: "internal detail"},
	}}
	open, accepted := al.Apply(v)
	if len(accepted) != 1 || accepted[0].Subject != "fees:INV-2" || accepted[0].Reason != "internal detail" {
		t.Fatalf("accepted = %+v", accepted)
	}
	var fees *schema.Violation
	for i := range open {
		if open[i].FeatureID == "fees" {
			fees = &open[i]
		}
		if open[i].Code == schema.CodeAllowlistStale {
			t.Errorf("entry reported stale: %+v", open[i])
		}
	}
	if fees == nil || !reflect.DeepEqual(fees.Refs, []string{"fees:INV-3"}) || strings.Contains(fees.Message, "INV-2") {
		t.Errorf("fees finding after the entry = %+v", fees)
	}
	if len(open) != 2 {
		t.Errorf("open = %+v, want fees (INV-3) and wallet", open)
	}

	// Accepting every ref closes the finding; a ref nothing names is stale.
	al.Entries = append(al.Entries,
		AllowEntry{Code: schema.CodeInvariantUnrequired, Subject: "fees:INV-3", Reason: "r"},
		AllowEntry{Code: schema.CodeInvariantUnrequired, Subject: "fees:INV-9", Reason: "r"})
	open, accepted = al.Apply(v)
	if len(accepted) != 2 {
		t.Errorf("accepted = %+v", accepted)
	}
	stale := driftByCode(open, schema.CodeAllowlistStale)
	if len(stale) != 1 || !strings.Contains(stale[0].Message, "fees:INV-9") || len(open) != 2 {
		t.Errorf("open = %+v", open)
	}
}

// Only a production entry point's flow fires STATUS_DRIFT. A proposal entry
// point, a production feature and a feature on no flow do not, and the words
// "live" and "shipped" never appear.
// @verifies validation:INV-19
func TestStatusDrift(t *testing.T) {
	got := driftByCode(Validate(driftGraph(), config.Default(), Options{}), schema.CodeStatusDrift)
	if len(got) != 1 {
		t.Fatalf("findings = %+v, want one for fees", got)
	}
	f := got[0]
	if f.FeatureID != "fees" || f.Severity != schema.SeverityInfo ||
		!strings.Contains(f.Message, "orders.route") || !strings.Contains(f.Message, "proposal") {
		t.Errorf("finding = %+v", f)
	}
	if strings.Contains(f.Message, "orders.beta") {
		t.Errorf("a proposal entry point is named: %q", f.Message)
	}
	for _, w := range []string{"live", "shipped"} {
		if strings.Contains(strings.ToLower(f.Message), w) {
			t.Errorf("message says %q: %q", w, f.Message)
		}
	}
	if f.NextAction == nil || !strings.Contains(f.NextAction.Detail, "lattice feature promote fees") {
		t.Errorf("next action = %+v", f.NextAction)
	}

	// Two production entry points are both named; a deprecated one is not.
	kg := driftGraph()
	kg.EntryPoints = append(kg.EntryPoints,
		schema.EntryPoint{ID: "a.cron", Status: schema.StatusProduction, Flow: []schema.FlowStep{{Feature: "fees"}}},
		schema.EntryPoint{ID: "z.old", Status: schema.StatusDeprecated, Flow: []schema.FlowStep{{Feature: "fees"}}})
	got = driftByCode(Validate(kg, config.Default(), Options{}), schema.CodeStatusDrift)
	if len(got) != 1 || !strings.Contains(got[0].Message, "a.cron, orders.route") || strings.Contains(got[0].Message, "z.old") {
		t.Errorf("findings = %+v", got)
	}
}

// FEATURE_UNREACHED also covers an unpromoted feature that has non-test code
// and is on no flow; a proposal with only test code, or none, stays silent,
// and the message of a production feature is unchanged.
// @verifies validation:INV-20
func TestFeatureUnreachedUnpromoted(t *testing.T) {
	kg := driftGraph()
	kg.EntryPoints[1].Flow = nil // the beta entry point names no feature
	kg.Features = append(kg.Features, schema.Manifest{ID: "legacy", Version: 1, Status: schema.StatusProduction,
		Purpose: "legacy", SourcePath: "features/legacy.yaml"})
	got := driftByCode(Validate(kg, config.Default(), Options{}), schema.CodeFeatureUnreached)
	by := map[string]schema.Violation{}
	for _, f := range got {
		by[f.FeatureID] = f
	}
	if len(by) != 2 {
		t.Fatalf("unreached = %+v, want wallet and legacy", got)
	}
	if m := by["wallet"].Message; !strings.HasPrefix(m, `accepted feature "wallet"`) || !strings.Contains(m, "has code in the graph") {
		t.Errorf("wallet message = %q", m)
	}
	if m := by["legacy"].Message; m != `production feature "legacy" is on no entry-point flow — dead code or an unmodeled seam` {
		t.Errorf("production message changed: %q", m)
	}
	if _, ok := by["ledger"]; ok {
		t.Error("a proposal with only test code was reported")
	}
	if _, ok := by["fees"]; ok {
		t.Error("a feature on a production flow was reported")
	}
}

// None of the new findings is above info, so a run that fails on warnings
// does not fail on them.
// @verifies validation:INV-21
func TestDriftFindingsNeverFailOnWarning(t *testing.T) {
	kg := driftGraph()
	kg.EntryPoints[1].Flow = nil
	kg.Features = append(kg.Features, schema.Manifest{ID: "legacy", Version: 1, Status: schema.StatusProduction,
		Purpose: "legacy", SourcePath: "features/legacy.yaml"})
	threshold, err := FailOn("warning")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range Validate(kg, config.Default(), Options{}) {
		switch f.Code {
		case schema.CodeInvariantUnrequired, schema.CodeStatusDrift, schema.CodeFeatureUnreached:
			seen[f.Code] = true
			if f.Severity != schema.SeverityInfo || Fails(f, threshold) {
				t.Errorf("%s is %s and fails at warning", f.Code, f.Severity)
			}
		}
	}
	for _, c := range []string{schema.CodeInvariantUnrequired, schema.CodeStatusDrift, schema.CodeFeatureUnreached} {
		if !seen[c] {
			t.Errorf("%s did not fire on the fixture graph", c)
		}
	}
}
