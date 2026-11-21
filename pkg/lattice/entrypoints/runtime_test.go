package entrypoints_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/postgres"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/typescript"
	"github.com/salahmyn/lattice/pkg/lattice/entrypoints"
	"github.com/salahmyn/lattice/pkg/lattice/graph"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// runtimeFixture parses testdata/runtime: a serve handler (feature orders), a
// scheduled SQL job (reports), a module beside the handler that nothing it
// runs calls (audit), a route naming a handler nobody defines, and a cron
// job whose name is an expression.
func runtimeFixture(t *testing.T) (schema.KnowledgeGraph, *reach.Graph) {
	t.Helper()
	root := filepath.Join("testdata", "runtime")
	var mods []ir.Module
	for _, a := range []struct {
		ad  adapters.LanguageAdapter
		ext string
	}{{typescript.New(), ".ts"}, {postgres.New(nil, nil), ".sql"}} {
		var parsed []ir.Module
		err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || filepath.Ext(p) != a.ext {
				return err
			}
			src, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			m, err := a.ad.Parse(context.Background(), filepath.ToSlash(rel), src)
			parsed = append(parsed, m)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if r, ok := a.ad.(adapters.Resolver); ok {
			parsed = r.Resolve(parsed)
		}
		mods = append(mods, parsed...)
	}
	var manifests []schema.Manifest
	for _, id := range []string{"audit", "orders", "reports"} {
		manifests = append(manifests, schema.Manifest{ID: id, Version: 1, Status: schema.StatusProduction,
			Purpose: id, SourcePath: "features/" + id + ".yaml"})
	}
	kg := graph.Build(graph.Input{Manifests: manifests, Modules: mods}, graph.Options{})
	return kg, reach.Build(mods, reach.Options{})
}

func proposals(kg schema.KnowledgeGraph, g *reach.Graph, known []schema.EntryPoint) []schema.EntryPoint {
	return entrypoints.ProposeFromRuntime(entrypoints.RuntimeInput{
		Roots: g.RuntimeRoots(), Known: known, Reach: g,
		Symbols: kg.Symbols, Features: kg.Features,
	})
}

func flowOf(ep schema.EntryPoint) []string {
	var out []string
	for _, s := range ep.Flow {
		out = append(out, s.Feature)
	}
	return out
}

// A serve handler and a scheduled job each become one proposed entry point
// of the right kind, and each one's flow is exactly the feature its handler
// reaches: audit, which sits beside the handler, is not on it.
// @verifies entrypoints:INV-5, entrypoints:INV-6
func TestRuntimeRootsBecomeProposals(t *testing.T) {
	kg, g := runtimeFixture(t)
	got := proposals(kg, g, nil)
	if len(got) != 2 {
		t.Fatalf("want 2 proposals (serve, cron), got %d: %+v", len(got), got)
	}
	byKind := map[string]schema.EntryPoint{}
	for _, ep := range got {
		byKind[ep.Kind] = ep
		if ep.Status != schema.StatusProposal {
			t.Errorf("%s: status %q, want proposal", ep.ID, ep.Status)
		}
		if ep.DetectedBy == "" || ep.Handler.Symbol == "" {
			t.Errorf("%s: names no producing fact or handler: %+v", ep.ID, ep)
		}
		for _, s := range ep.Flow {
			if s.Basis != schema.FlowBasisReach || len(s.ViaSymbols) == 0 {
				t.Errorf("%s: step %+v is not a reach step with its symbols", ep.ID, s)
			}
		}
	}
	http, cron := byKind[schema.EntryPointKindHTTP], byKind[schema.EntryPointKindCron]
	if http.Handler.Symbol != "api.index.<serve>" || http.DetectedBy != "runtime_root:serve" {
		t.Errorf("http proposal: %+v", http)
	}
	if !reflect.DeepEqual(flowOf(http), []string{"orders"}) {
		t.Errorf("http flow = %v, want exactly [orders]", flowOf(http))
	}
	if cron.Trigger.Schedule != "0 3 * * *" || cron.Trigger.Job != "nightly-x" || cron.DetectedBy != "runtime_root:cron" {
		t.Errorf("cron proposal: %+v", cron)
	}
	if !reflect.DeepEqual(flowOf(cron), []string{"reports"}) {
		t.Errorf("cron flow = %v, want exactly [reports]", flowOf(cron))
	}
	if again := proposals(kg, g, nil); !reflect.DeepEqual(again, got) {
		t.Errorf("proposals are not deterministic:\n%+v\n%+v", got, again)
	}
}

// A root with no handler symbol in the graph, and a cron job whose name is
// an expression, yield no proposal and one named finding each.
// @verifies entrypoints:INV-5
func TestUnproposableRootsAreNamed(t *testing.T) {
	kg, g := runtimeFixture(t)
	for _, ep := range proposals(kg, g, nil) {
		if strings.Contains(ep.Handler.File, "routes") || strings.Contains(ep.Handler.File, "002_dynamic") {
			t.Errorf("a root that cannot be proposed was: %+v", ep)
		}
	}
	r := &reach.Result{Graph: g}
	var files []string
	for _, v := range reach.RuntimeRootFindings(kg, r) {
		if v.Code != schema.CodeRuntimeRootUnproposed || v.Severity != schema.SeverityInfo || v.Location == nil {
			t.Errorf("finding %+v: want RUNTIME_ROOT_UNPROPOSED, info, with a location", v)
		}
		files = append(files, v.Location.File)
	}
	sort.Strings(files)
	if want := []string{"api/routes.ts", "db/002_dynamic.sql"}; !reflect.DeepEqual(files, want) {
		t.Errorf("findings name %v, want %v", files, want)
	}
	// A declared entry point for the route covers it: no finding.
	kg.EntryPoints = []schema.EntryPoint{{ID: "ep.http.missing", Kind: schema.EntryPointKindHTTP, Status: schema.StatusProduction,
		Trigger: schema.Trigger{Method: "GET", Path: "/orders/missing"}, Handler: schema.Handler{Symbol: "api.routes.other"}}}
	if n := len(reach.RuntimeRootFindings(kg, r)); n != 1 {
		t.Errorf("a declared entry point should cover the route, leaving 1 finding, got %d", n)
	}
}

// A root a declared entry point already covers is not proposed again, and a
// rejected proposal stays rejected.
// @verifies entrypoints:INV-5
func TestCoveredAndRejectedRootsAreNotProposed(t *testing.T) {
	kg, g := runtimeFixture(t)
	declared := []schema.EntryPoint{{ID: "ep.http.api", Kind: schema.EntryPointKindHTTP, Status: schema.StatusProduction,
		Handler: schema.Handler{Symbol: "api.index.<serve>"}}}
	got := proposals(kg, g, declared)
	if len(got) != 1 || got[0].Kind != schema.EntryPointKindCron {
		t.Fatalf("with the serve handler declared, only the cron job is proposed: %+v", got)
	}
	named := []schema.EntryPoint{{ID: "ep.cron.mine", Kind: schema.EntryPointKindCron, Status: schema.StatusProduction,
		Trigger: schema.Trigger{Job: "nightly-x"}, Handler: schema.Handler{Symbol: "elsewhere"}}}
	if got := proposals(kg, g, named); len(got) != 1 || got[0].Kind != schema.EntryPointKindHTTP {
		t.Fatalf("a declared job of the same name covers the cron root: %+v", got)
	}

	dir := t.TempDir()
	all := proposals(kg, g, nil)
	if _, err := entrypoints.PersistProposals(dir, all, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := entrypoints.Decide(dir, all[0].ID, entrypoints.DecisionRejectEP); err != nil {
		t.Fatal(err)
	}
	rejected := entrypoints.ProposeFromRuntime(entrypoints.RuntimeInput{
		Roots: g.RuntimeRoots(), Reach: g, Symbols: kg.Symbols, Features: kg.Features,
		Rejected: func(id string) bool { return entrypoints.IsRejected(dir, id) },
	})
	if len(rejected) != 1 || rejected[0].ID == all[0].ID {
		t.Errorf("the rejected proposal %s came back: %+v", all[0].ID, rejected)
	}
}

// Proposals persist without their flow so a reviewer can accept or reject
// them; an accepted one stays what the reviewer made it, and the next
// extract gives it the flow the call graph computes.
// @verifies entrypoints:INV-1, entrypoints:INV-5
func TestProposalsPersistAndStayProposals(t *testing.T) {
	kg, g := runtimeFixture(t)
	all := proposals(kg, g, nil)
	dir := t.TempDir()
	wrote, err := entrypoints.PersistProposals(dir, all, nil)
	if err != nil || len(wrote) != 2 {
		t.Fatalf("wrote %v, %v", wrote, err)
	}
	persisted, err := entrypoints.LoadEntryPoints(dir)
	if err != nil || len(persisted) != 2 {
		t.Fatalf("loaded %d: %v", len(persisted), err)
	}
	for _, ep := range persisted {
		if ep.Status != schema.StatusProposal || len(ep.Flow) != 0 || ep.DetectedBy == "" {
			t.Errorf("persisted %s: %+v", ep.ID, ep)
		}
	}
	if again, _ := entrypoints.PersistProposals(dir, all, persisted); len(again) != 0 {
		t.Errorf("a manifest that exists was written again: %v", again)
	}
	res, err := entrypoints.Decide(dir, all[0].ID, entrypoints.DecisionAcceptEP)
	if err != nil || res.NewStatus != string(schema.StatusProduction) {
		t.Fatalf("accept: %+v, %v", res, err)
	}
	persisted, _ = entrypoints.LoadEntryPoints(dir)
	merged := entrypoints.Merge(all, persisted)
	for _, ep := range merged {
		if len(ep.Flow) != 1 {
			t.Errorf("%s: merged flow %v, want the computed one", ep.ID, flowOf(ep))
		}
		if ep.ID == all[0].ID && ep.Status != schema.StatusProduction {
			t.Errorf("the accepted entry point is %q", ep.Status)
		}
		if ep.ID != all[0].ID && ep.Status != schema.StatusProposal {
			t.Errorf("a proposal became %q by itself", ep.Status)
		}
	}
}

// The flow steps of the proximity tracer say so.
// @verifies entrypoints:INV-6
func TestProximityStepsAreLabelled(t *testing.T) {
	eps := []schema.EntryPoint{{ID: "ep.http.a", Handler: schema.Handler{Symbol: "App\\Http\\A::run", File: "app/Http/A.php"}}}
	features := []schema.Manifest{{ID: "billing", Implementations: []schema.Implementation{
		{Symbol: "App\\Http\\A::other", File: "app/Http/A.php"}}}}
	flow := entrypoints.Trace(eps, features)[0].Flow
	if len(flow) != 1 || flow[0].Basis != schema.FlowBasisProximity {
		t.Fatalf("flow = %+v, want one proximity step", flow)
	}
}
