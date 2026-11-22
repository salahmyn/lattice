package validate

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/postgres"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/typescript"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/graph"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
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

// rootReachGraph is a small TS workspace with two declared roots (job_a and
// job_b, both calling the digest), a production route calling checkout, and a
// test calling the audit code. Features: digest (proposal, two roots reach
// it), checkout (proposal, on a production flow: STATUS_DRIFT), audit
// (proposal, reached only from a test), billing (production, reached by a
// root), legacy (deprecated, reached by a root), orphan (proposal, nothing
// reaches it).
func rootReachGraph(t *testing.T) (schema.KnowledgeGraph, *reach.Result) {
	t.Helper()
	files := map[string]string{
		"src/digest.ts":   "// @module-feature digest\nexport function send() {}\n",
		"src/checkout.ts": "// @module-feature checkout\nexport function place() {}\n",
		"src/audit.ts":    "// @module-feature audit\nexport function record() {}\n",
		"src/billing.ts":  "// @module-feature billing\nexport function charge() {}\n",
		"src/legacy.ts":   "// @module-feature legacy\nexport function old() {}\n",
		"src/orphan.ts":   "// @module-feature orphan\nexport function idle() {}\n",
		"src/jobs.ts": "// @module-feature jobs\nimport { send } from './digest';\nimport { charge } from './billing';\nimport { old } from './legacy';\n" +
			"export function jobA() { send(); charge(); old(); }\nexport function jobB() { send(); }\n",
		"src/route.ts":      "// @module-feature jobs\nimport { place } from './checkout';\nexport function route() { place(); }\n",
		"src/audit.test.ts": "// @module-feature jobs\nimport { record } from './audit';\nexport function auditTest() { record(); }\n",
	}
	order := []string{"src/audit.test.ts", "src/audit.ts", "src/billing.ts", "src/checkout.ts", "src/digest.ts", "src/jobs.ts", "src/legacy.ts", "src/orphan.ts", "src/route.ts"}
	a := typescript.New()
	var mods []ir.Module
	for _, p := range order {
		m, err := a.Parse(context.Background(), p, []byte(files[p]))
		if err != nil {
			t.Fatal(err)
		}
		mods = append(mods, m)
	}
	mods = a.Resolve(mods)
	var manifests []schema.Manifest
	for id, st := range map[string]schema.Status{"digest": schema.StatusProposal, "checkout": schema.StatusProposal,
		"audit": schema.StatusProposal, "billing": schema.StatusProduction, "legacy": schema.StatusDeprecated,
		"orphan": schema.StatusProposal, "jobs": schema.StatusProduction} {
		manifests = append(manifests, schema.Manifest{ID: id, Version: 1, Status: st, Purpose: id, SourcePath: "features/" + id + ".yaml"})
	}
	sort.Slice(manifests, func(i, j int) bool { return manifests[i].ID < manifests[j].ID })
	kg := graph.Build(graph.Input{Manifests: manifests, Modules: mods}, graph.Options{})
	kg.EntryPoints = []schema.EntryPoint{{ID: "ep.route", Kind: schema.EntryPointKindHTTP, Status: schema.StatusProduction,
		Handler: schema.Handler{Symbol: "src.route.route"}, Flow: []schema.FlowStep{{Feature: "jobs"}, {Feature: "checkout"}}}}
	r := reach.Compute(reach.Input{Modules: mods, EntryPoints: kg.EntryPoints, Declared: []config.CoverageRoot{
		{Symbol: "src.jobs.jobA", Reason: "job a"}, {Symbol: "src.jobs.jobB", Reason: "job b"},
		{Symbol: "src.audit.test.auditTest", Reason: "a test"}}})
	reach.Annotate(&kg, r)
	return kg, r
}

// One finding per proposal or accepted feature that no production flow names
// and a non-test root reaches, naming each root and a reached symbol and
// saying "reachable in code". It never fires for a feature STATUS_DRIFT
// reports, for a production or deprecated feature, for one reached only from
// a test, or for one nothing reaches; a run that fails on warnings stays
// green with it present.
// @verifies validation:INV-25
func TestProposalReachedByRoot(t *testing.T) {
	kg, r := rootReachGraph(t)
	v := Validate(kg, config.Default(), Options{Reach: r})
	got := driftByCode(v, schema.CodeProposalReachedByRoot)
	if len(got) != 1 || got[0].FeatureID != "digest" || got[0].Severity != schema.SeverityInfo {
		t.Fatalf("PROPOSAL_REACHED_BY_ROOT = %+v", got)
	}
	m := got[0].Message
	for _, want := range []string{"src.jobs.jobA (coverage.roots: job a) through src.digest.send", "src.jobs.jobB (coverage.roots: job b) through src.digest.send",
		"reachable in code", "runtime flags"} {
		if !strings.Contains(m, want) {
			t.Errorf("message lacks %q: %s", want, m)
		}
	}
	if !reflect.DeepEqual(got[0].Refs, []string{"src.jobs.jobA", "src.jobs.jobB"}) {
		t.Errorf("refs = %v", got[0].Refs)
	}
	if d := driftByCode(v, schema.CodeStatusDrift); len(d) != 1 || d[0].FeatureID != "checkout" {
		t.Errorf("STATUS_DRIFT = %+v", d)
	}
	for _, x := range v {
		if x.Severity == schema.SeverityWarning && x.Code == schema.CodeProposalReachedByRoot {
			t.Errorf("a warning: %+v", x)
		}
	}
	// No roots: the rule stays quiet, which is unknown, not a pass.
	kg2, _ := rootReachGraph(t)
	if n := len(driftByCode(Validate(kg2, config.Default(), Options{}), schema.CodeProposalReachedByRoot)); n != 0 {
		t.Errorf("without a reach result: %d findings", n)
	}
}

// migrationReachGraph: two proposal features backed by SQL functions. "seeded"
// is called only from a migration's top level; "mixed" is called from one
// migration and from a cron job.
func migrationReachGraph(t *testing.T) (schema.KnowledgeGraph, *reach.Result) {
	t.Helper()
	pg := postgres.New(nil, nil)
	files := map[string]string{
		"db/migrations/001_seed.sql":    "-- @module-feature seeded\ncreate function public.seed_rows() returns void language sql as $$ select 1 $$;\n",
		"db/migrations/001_refresh.sql": "-- @module-feature mixed\ncreate function public.refresh_rows() returns void language sql as $$ select 1 $$;\n",
		"db/migrations/002_apply.sql": "-- @module-feature jobs\nselect public.seed_rows();\nselect public.refresh_rows();\n",
		"db/migrations/003_cron.sql":  "-- @module-feature jobs\nselect cron.schedule('refresh', '0 3 * * *', $$ select public.refresh_rows() $$);\n",
	}
	var mods []ir.Module
	for _, p := range []string{"db/migrations/001_refresh.sql", "db/migrations/001_seed.sql", "db/migrations/002_apply.sql", "db/migrations/003_cron.sql"} {
		m, err := pg.Parse(context.Background(), p, []byte(files[p]))
		if err != nil {
			t.Fatal(err)
		}
		mods = append(mods, m)
	}
	mods = pg.Resolve(mods)
	var manifests []schema.Manifest
	for id, st := range map[string]schema.Status{"seeded": schema.StatusProposal, "mixed": schema.StatusProposal, "jobs": schema.StatusProduction} {
		manifests = append(manifests, schema.Manifest{ID: id, Version: 1, Status: st, Purpose: id, SourcePath: "features/" + id + ".yaml"})
	}
	sort.Slice(manifests, func(i, j int) bool { return manifests[i].ID < manifests[j].ID })
	kg := graph.Build(graph.Input{Manifests: manifests, Modules: mods}, graph.Options{})
	r := reach.Compute(reach.Input{Modules: mods, EntryPoints: kg.EntryPoints})
	reach.Annotate(&kg, r)
	return kg, r
}

// A root that is a migration's applied top level is a deploy step, not
// runtime reach: a proposal feature reached only from one gets no finding, and
// one also reached from a runtime root lists only that root.
// @verifies validation:INV-26
func TestProposalReachedByRootSkipsMigrationRoots(t *testing.T) {
	kg, r := migrationReachGraph(t)
	if r.FeaturesReached(kg)["seeded"] == false {
		t.Fatal("fixture: a migration root should still reach seeded (FEATURE_UNREACHED reads that reach)")
	}
	got := driftByCode(Validate(kg, config.Default(), Options{Reach: r}), schema.CodeProposalReachedByRoot)
	if len(got) != 1 || got[0].FeatureID != "mixed" {
		t.Fatalf("PROPOSAL_REACHED_BY_ROOT = %+v", got)
	}
	if strings.Contains(got[0].Message, "sql.applied.") || len(got[0].Refs) != 1 {
		t.Errorf("a migration root is named: refs %v, %s", got[0].Refs, got[0].Message)
	}
}
