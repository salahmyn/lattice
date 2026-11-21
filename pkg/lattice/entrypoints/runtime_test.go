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
	if _, err := entrypoints.DecideDerived(dir, all[0], entrypoints.DecisionRejectEP); err != nil {
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

// An accepted proposal keeps what the reviewer made it and takes the flow the
// call graph computes on the next run; a manifest written by DecideDerived
// has no flow of its own.
// @verifies entrypoints:INV-1, entrypoints:INV-3
func TestDerivedProposalDecisions(t *testing.T) {
	kg, g := runtimeFixture(t)
	all := proposals(kg, g, nil)
	dir := t.TempDir()
	res, err := entrypoints.DecideDerived(dir, all[0], entrypoints.DecisionAcceptEP)
	if err != nil || res.NewStatus != string(schema.StatusProduction) {
		t.Fatalf("accept: %+v, %v", res, err)
	}
	if _, err := entrypoints.DecideDerived(dir, all[0], entrypoints.DecisionAcceptEP); err == nil {
		t.Error("a manifest that exists must not be overwritten")
	}
	if _, err := entrypoints.DecideDerived(dir, all[1], entrypoints.DecisionRejectEP); err != nil {
		t.Fatal(err)
	}
	if !entrypoints.IsRejected(dir, all[1].ID) {
		t.Error("the rejected proposal is not in .rejected/")
	}
	persisted, err := entrypoints.LoadEntryPoints(dir)
	if err != nil || len(persisted) != 1 || len(persisted[0].Flow) != 0 {
		t.Fatalf("persisted = %+v, %v", persisted, err)
	}
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

// edgeFixture parses a small Supabase-shaped tree: three edge functions
// (the shared directory holds no function), and a migration with three cron
// jobs. Manifests name things the way people write them: the function the
// serve handler calls, a file, a job name, or the SQL function a job runs.
func edgeFixture(t *testing.T) (schema.KnowledgeGraph, *reach.Graph) {
	t.Helper()
	ts, pg := typescript.New(), postgres.New(nil, nil)
	var mods []ir.Module
	for _, f := range []struct{ path, src string }{
		{"supabase/functions/feeds/index.ts", "// @module-feature feeds\nexport function handle(_r: Request): Response { return new Response('x'); }\nDeno.serve(handle);\n"},
		{"supabase/functions/hook/index.ts", "// @module-feature feeds\nDeno.serve((_r: Request) => new Response('x'));\n"},
		{"supabase/functions/fresh/index.ts", "// @module-feature feeds\nexport function run(): void {}\nDeno.serve(() => { run(); return new Response('x'); });\n"},
		{"supabase/functions/_shared/util.ts", "// @module-feature feeds\nexport function util(): void {}\n"},
	} {
		m, err := ts.Parse(t.Context(), f.path, []byte(f.src))
		if err != nil {
			t.Fatal(err)
		}
		mods = append(mods, m)
	}
	m, err := pg.Parse(t.Context(), "supabase/migrations/001_jobs.sql", []byte(`-- @module-feature feeds
create function app.sweep() returns void language sql as $$ select 1; $$;
create function app.tidy() returns void language sql as $$ select 1; $$;
create function app.fresh_job() returns void language sql as $$ select 1; $$;

select cron.schedule('sweep-job', '0 3 * * *', $$ select app.sweep() $$);
select cron.schedule('tidy-job', '0 3 * * *', $$ select app.tidy() $$);
select cron.schedule('fresh-job', '*/5 * * * *', $$ select app.fresh_job() $$);
`))
	if err != nil {
		t.Fatal(err)
	}
	mods = pg.Resolve(append(mods, m))
	manifests := []schema.Manifest{{ID: "feeds", Version: 1, Status: schema.StatusProduction, Purpose: "feeds",
		SourcePath: "features/feeds.yaml"}}
	kg := graph.Build(graph.Input{Manifests: manifests, Modules: mods}, graph.Options{})
	return kg, reach.Build(mods, reach.Options{})
}

// Entry points declared the way people write them cover the runtime roots
// they stand for, so none is proposed again: an http_route naming the
// function the serve handler calls, a webhook naming only the function's file,
// a cron naming the job, and a cron naming the schedule and the SQL function
// its job calls. Two jobs on one schedule are not merged: tidy-job calls
// another function, so it stays a proposal.
// @verifies entrypoints:INV-5
func TestDeclaredEntryPointsCoverRuntimeRoots(t *testing.T) {
	kg, g := edgeFixture(t)
	if all := proposals(kg, g, nil); len(all) != 6 {
		t.Fatalf("with nothing declared want 6 proposals (3 serve, 3 cron), got %d: %+v", len(all), all)
	}
	declared := []schema.EntryPoint{
		{ID: "ep.http.feeds", Kind: schema.EntryPointKindHTTP, Status: schema.StatusProduction,
			Trigger: schema.Trigger{Method: "POST", Path: "/functions/v1/feeds"},
			Handler: schema.Handler{Symbol: "supabase.functions.feeds.index.handle"}},
		{ID: "ep.webhook.hook", Kind: schema.EntryPointKindWebhook, Status: schema.StatusProduction,
			Handler: schema.Handler{Symbol: "onHook", File: "supabase/functions/hook/index.ts"}},
		{ID: "ep.cron.sweep", Kind: schema.EntryPointKindCron, Status: schema.StatusProduction,
			Trigger: schema.Trigger{Job: "sweep-job"}, Handler: schema.Handler{Symbol: "elsewhere"}},
		{ID: "ep.cron.tidy", Kind: schema.EntryPointKindCron, Status: schema.StatusProduction,
			Trigger: schema.Trigger{Schedule: "0 3 * * *"}, Handler: schema.Handler{Symbol: "sql.app.sweep"}},
	}
	got := proposals(kg, g, declared)
	var ids []string
	for _, ep := range got {
		ids = append(ids, ep.ID)
	}
	// sweep-job is covered twice over; tidy-job shares the schedule but its
	// body calls app.tidy, not the declared app.sweep.
	if len(got) != 3 {
		t.Fatalf("want 3 proposals (fresh serve, tidy-job, fresh-job), got %v", ids)
	}
	jobs, serves := map[string]bool{}, 0
	for _, ep := range got {
		if ep.Kind == schema.EntryPointKindCron {
			jobs[ep.Trigger.Job] = true
		} else {
			serves++
			if !strings.Contains(ep.Handler.File, "fresh") {
				t.Errorf("a covered serve root was proposed: %+v", ep)
			}
		}
	}
	if serves != 1 || !jobs["tidy-job"] || !jobs["fresh-job"] {
		t.Errorf("proposals %v: want the fresh function and jobs tidy-job and fresh-job", ids)
	}

	// Everything declared: nothing is proposed.
	declared = append(declared,
		schema.EntryPoint{ID: "ep.http.fresh", Kind: schema.EntryPointKindHTTP, Status: schema.StatusProduction,
			Handler: schema.Handler{Symbol: "supabase.functions.fresh.index.run"}},
		schema.EntryPoint{ID: "ep.cron.tidy2", Kind: schema.EntryPointKindCron, Status: schema.StatusProduction,
			Trigger: schema.Trigger{Schedule: "0 3 * * *"}, Handler: schema.Handler{Symbol: "sql.app.tidy"}},
		schema.EntryPoint{ID: "ep.cron.fresh", Kind: schema.EntryPointKindCron, Status: schema.StatusProduction,
			Trigger: schema.Trigger{Job: "fresh-job", Schedule: "*/5 * * * *"}, Handler: schema.Handler{Symbol: "sql.app.fresh_job"}})
	if got := proposals(kg, g, declared); len(got) != 0 {
		t.Errorf("everything is declared, yet %d proposals: %+v", len(got), got)
	}

	// A cron whose name differs never covers another job, even on the same
	// schedule with a handler the other job calls.
	other := []schema.EntryPoint{{ID: "ep.cron.x", Kind: schema.EntryPointKindCron, Status: schema.StatusProduction,
		Trigger: schema.Trigger{Job: "someone-else", Schedule: "0 3 * * *"}, Handler: schema.Handler{Symbol: "sql.app.sweep"}}}
	if got := proposals(kg, g, other); len(got) != 6 {
		t.Errorf("a differently named job covered a root: %d proposals", len(got))
	}
	// An http_route of another module does not cover a serve root.
	elsewhere := []schema.EntryPoint{{ID: "ep.http.o", Kind: schema.EntryPointKindHTTP, Status: schema.StatusProduction,
		Handler: schema.Handler{Symbol: "supabase.functions.other.index.handle", File: "supabase/functions/other/index.ts"}}}
	if got := proposals(kg, g, elsewhere); len(got) != 6 {
		t.Errorf("an entry point of another module covered a root: %d proposals", len(got))
	}
}

// A serve proposal for an edge function carries the path the platform serves
// it at and no method; a serve in the shared directory has no path.
// @verifies entrypoints:INV-5
func TestEdgeFunctionProposalPath(t *testing.T) {
	kg, g := edgeFixture(t)
	paths := map[string]string{}
	for _, ep := range proposals(kg, g, nil) {
		if ep.Kind == schema.EntryPointKindHTTP {
			if ep.Trigger.Method != "" {
				t.Errorf("%s: method %q, want none (Deno.serve answers every method)", ep.ID, ep.Trigger.Method)
			}
			paths[ep.Handler.File] = ep.Trigger.Path
		}
	}
	for file, want := range map[string]string{
		"supabase/functions/feeds/index.ts": "/functions/v1/feeds",
		"supabase/functions/hook/index.ts":  "/functions/v1/hook",
		"supabase/functions/fresh/index.ts": "/functions/v1/fresh",
	} {
		if paths[file] != want {
			t.Errorf("%s: path %q, want %q", file, paths[file], want)
		}
	}
	for file, want := range map[string]string{
		"supabase/functions/_shared/index.ts": "",
		"supabase/functions/a/b/index.ts":     "",
		"api/index.ts":                        "",
		"web/supabase/functions/x/index.mjs":  "/functions/v1/x",
	} {
		if got := reach.EdgeFunctionPath(file); got != want {
			t.Errorf("EdgeFunctionPath(%q) = %q, want %q", file, got, want)
		}
	}
}

// layoutRoots parses an edge-function layout: each function's index.ts calls
// Deno.serve(createHandler(...)) from its handler.ts, and a cron job posts to
// one function through a declared setting origin.
func layoutRoots(t *testing.T, jobSchedule string) (schema.KnowledgeGraph, *reach.Graph) {
	t.Helper()
	parseAll := func(a adapters.LanguageAdapter, files map[string]string) []ir.Module {
		var mods []ir.Module
		var names []string
		for p := range files {
			names = append(names, p)
		}
		sort.Strings(names)
		for _, p := range names {
			m, err := a.Parse(context.Background(), p, []byte(files[p]))
			if err != nil {
				t.Fatal(err)
			}
			mods = append(mods, m)
		}
		if r, ok := a.(adapters.Resolver); ok {
			mods = r.Resolve(mods)
		}
		return mods
	}
	ts := parseAll(typescript.New(), map[string]string{
		"supabase/functions/add-source/index.ts":   "import { createHandler } from './handler.ts';\nDeno.serve(createHandler({}));\n",
		"supabase/functions/add-source/handler.ts": "export function createHandler(deps: unknown) { return async (req: Request) => new Response('ok'); }\n",
		"supabase/functions/digest/index.ts":       "import { createHandler } from './handler.ts';\nDeno.serve(createHandler({}));\n",
		"supabase/functions/digest/handler.ts":     "export function createHandler(deps: unknown) { return helper(); }\nfunction helper() { return 1; }\n",
	})
	pg := postgres.New(nil, nil)
	pg.SetHTTPOrigins([]string{"setting:app.api_url"})
	sql := parseAll(pg, map[string]string{
		"db/migrations/001_cron.sql": "select cron.schedule('digest-job', '" + jobSchedule + "', $$\n" +
			"  select net.http_post(url := current_setting('app.api_url') || '/functions/v1/digest');\n$$);\n",
	})
	mods := append(ts, sql...)
	return schema.KnowledgeGraph{}, reach.Build(mods, reach.Options{})
}

// The roots of an edge-function layout are covered by the entry points a
// person declared for them, however they were declared: by the function's
// path (any method), by a handler the serve call builds, and, for a cron job,
// by the same schedule and a handler the job reaches through its http hop.
// @verifies entrypoints:INV-5
func TestDeclaredEntryPointsCoverServeAndCronRoots(t *testing.T) {
	_, g := layoutRoots(t, "0 3 * * *")
	declared := []schema.EntryPoint{
		{ID: "ep.add", Kind: schema.EntryPointKindHTTP, Status: schema.StatusProduction,
			Trigger: schema.Trigger{Method: "POST", Path: "/functions/v1/add-source"},
			Handler: schema.Handler{Symbol: "somewhere.else"}},
		{ID: "ep.digest", Kind: schema.EntryPointKindWebhook, Status: schema.StatusProduction,
			Handler: schema.Handler{Symbol: "supabase.functions.digest.handler.createHandler"}},
		{ID: "ep.cron", Kind: schema.EntryPointKindCron, Status: schema.StatusProduction,
			Trigger: schema.Trigger{Schedule: "0 3 * * *"},
			Handler: schema.Handler{Symbol: "supabase.functions.digest.handler.createHandler"}},
	}
	in := entrypoints.RuntimeInput{Roots: g.RuntimeRoots(), Known: declared, Reach: g}
	if got := entrypoints.ProposeFromRuntime(in); len(got) != 0 {
		t.Errorf("every root is declared, want 0 proposals, got %+v", got)
	}
	if len(in.Roots) != 3 {
		t.Errorf("want 3 roots (2 serve, 1 cron), got %d", len(in.Roots))
	}
}

// A different schedule, a different function path, and a handler the job
// does not reach each leave the root proposed.
// @verifies entrypoints:INV-5
func TestCoverageNeedsTheSameScheduleAndPath(t *testing.T) {
	_, g := layoutRoots(t, "0 3 * * *")
	handler := schema.Handler{Symbol: "supabase.functions.digest.handler.createHandler"}
	cases := []struct {
		name     string
		ep       schema.EntryPoint
		proposed []string // the kinds still proposed
	}{
		{"different schedule", schema.EntryPoint{ID: "a", Kind: schema.EntryPointKindCron, Trigger: schema.Trigger{Schedule: "0 4 * * *"}, Handler: handler},
			[]string{"cron", "http_route", "http_route"}},
		{"different function path", schema.EntryPoint{ID: "b", Kind: schema.EntryPointKindHTTP, Trigger: schema.Trigger{Path: "/functions/v1/other"}, Handler: schema.Handler{Symbol: "x.y"}},
			[]string{"cron", "http_route", "http_route"}},
		{"cron handler the job does not reach", schema.EntryPoint{ID: "c", Kind: schema.EntryPointKindCron, Trigger: schema.Trigger{Schedule: "0 3 * * *"},
			Handler: schema.Handler{Symbol: "supabase.functions.add-source.handler.createHandler"}},
			[]string{"cron", "http_route", "http_route"}},
		{"a job of another name", schema.EntryPoint{ID: "d", Kind: schema.EntryPointKindCron, Trigger: schema.Trigger{Schedule: "0 3 * * *", Job: "other-job"}, Handler: handler},
			[]string{"cron", "http_route", "http_route"}},
	}
	for _, c := range cases {
		got := entrypoints.ProposeFromRuntime(entrypoints.RuntimeInput{Roots: g.RuntimeRoots(), Known: []schema.EntryPoint{c.ep}, Reach: g})
		var kinds []string
		for _, ep := range got {
			kinds = append(kinds, ep.Kind)
		}
		sort.Strings(kinds)
		want := append([]string(nil), c.proposed...)
		sort.Strings(want)
		if !reflect.DeepEqual(kinds, want) {
			t.Errorf("%s: proposed %v, want %v", c.name, kinds, want)
		}
	}
}

// wrappedServe parses an edge function whose serve call wraps the handler it
// builds: Deno.serve(withSentry('n', createHandler({}))).
func wrappedServe(t *testing.T, serveCall string) *reach.Graph {
	t.Helper()
	mods, err := func() ([]ir.Module, error) {
		var out []ir.Module
		ts := typescript.New()
		for _, f := range []struct{ path, src string }{
			{"supabase/functions/n/index.ts", "import { createHandler } from './handler.ts';\nimport { withSentry } from '../_shared/sentry.ts';\n" + serveCall + "\n"},
			{"supabase/functions/n/handler.ts", "export function createHandler(deps: unknown) { return async (req: Request) => new Response('ok'); }\n"},
			{"supabase/functions/n/other.ts", "export function other() { return 1; }\n"},
			{"supabase/functions/_shared/sentry.ts", "export function withSentry(name: string, h: unknown) { return h; }\n"},
		} {
			m, err := ts.Parse(context.Background(), f.path, []byte(f.src))
			if err != nil {
				return nil, err
			}
			out = append(out, m)
		}
		return ts.Resolve(out), nil
	}()
	if err != nil {
		t.Fatal(err)
	}
	return reach.Build(mods, reach.Options{})
}

// A declared entry point covers a serve root whose call mentions the handler
// anywhere in its argument expression, however deep the wrappers nest. A
// handler the serve call does not mention is still proposed.
// @verifies entrypoints:INV-5
func TestWrappedServeHandlerIsCovered(t *testing.T) {
	g := wrappedServe(t, "Deno.serve(withSentry('n', createHandler({})));")
	declared := func(symbol string) []schema.EntryPoint {
		return []schema.EntryPoint{{ID: "ep.n", Kind: schema.EntryPointKindHTTP, Status: schema.StatusProduction,
			Trigger: schema.Trigger{Method: "POST", Path: "/somewhere/else"}, Handler: schema.Handler{Symbol: symbol}}}
	}
	propose := func(g *reach.Graph, symbol string) []schema.EntryPoint {
		return entrypoints.ProposeFromRuntime(entrypoints.RuntimeInput{Roots: g.RuntimeRoots(), Known: declared(symbol), Reach: g})
	}
	if got := propose(g, "supabase.functions.n.handler.createHandler"); len(got) != 0 {
		t.Errorf("a handler named inside the wrapped serve call is covered, got %+v", got)
	}
	deep := wrappedServe(t, "Deno.serve(withSentry('n', withSentry('m', createHandler({}))));")
	if got := propose(deep, "supabase.functions.n.handler.createHandler"); len(got) != 0 {
		t.Errorf("a handler nested two wrappers deep is covered, got %+v", got)
	}
	if got := propose(g, "supabase.functions.n.other.other"); len(got) != 1 {
		t.Errorf("a handler the serve call does not reference still proposes the root, got %+v", got)
	}
}

// vaultLayout is the layout of an edge function a cron job calls through a
// SQL function that reads the function's URL whole from Vault.
func vaultLayout(t *testing.T, secrets map[string]string, cronBody string) *reach.Graph {
	t.Helper()
	parse := func(a adapters.LanguageAdapter, files map[string]string) []ir.Module {
		var out []ir.Module
		var names []string
		for p := range files {
			names = append(names, p)
		}
		sort.Strings(names)
		for _, p := range names {
			m, err := a.Parse(context.Background(), p, []byte(files[p]))
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, m)
		}
		if r, ok := a.(adapters.Resolver); ok {
			out = r.Resolve(out)
		}
		return out
	}
	mods := parse(typescript.New(), map[string]string{
		"supabase/functions/enrich/index.ts":   "import { createHandler } from './handler.ts';\nDeno.serve(createHandler({}));\n",
		"supabase/functions/enrich/handler.ts": "export function createHandler(deps: unknown) { return async (req: Request) => new Response('ok'); }\n",
	})
	pg := postgres.New(nil, nil)
	pg.SetHTTPSecrets(secrets)
	mods = append(mods, parse(pg, map[string]string{
		"db/migrations/001_fns.sql": "create function public.call_enrich() returns void language plpgsql as $$\ndeclare v_url text;\nbegin\n" +
			"  select decrypted_secret into v_url from vault.decrypted_secrets where name = 'enrich_url';\n" +
			"  perform net.http_post(url := v_url, body := '{}'::jsonb);\nend $$;\n",
		"db/migrations/002_cron.sql": "select cron.schedule('enrich-job', '*/5 * * * *', $$ " + cronBody + " $$);\n",
	})...)
	return reach.Build(mods, reach.Options{})
}

// A cron job that calls a SQL function, which posts to an edge function at a
// URL read whole from a declared Vault secret, reaches the function's handler:
// a declared cron entry point for that handler on the job's schedule covers
// the job, so only the serve root is left to propose when nothing covers it.
// Without the declaration the URL is computed, no edge exists, and the job is
// proposed again.
// @verifies entrypoints:INV-5
func TestVaultURLCronJobIsCovered(t *testing.T) {
	declared := []schema.EntryPoint{{ID: "ep.enrich.cron", Kind: schema.EntryPointKindCron, Status: schema.StatusProduction,
		Trigger: schema.Trigger{Schedule: "*/5 * * * *"},
		Handler: schema.Handler{Symbol: "supabase.functions.enrich.handler.createHandler"}}}
	proposed := func(g *reach.Graph) []string {
		var kinds []string
		for _, ep := range entrypoints.ProposeFromRuntime(entrypoints.RuntimeInput{Roots: g.RuntimeRoots(), Known: declared, Reach: g}) {
			kinds = append(kinds, ep.Kind)
		}
		return kinds
	}
	g := vaultLayout(t, map[string]string{"enrich_url": "/functions/v1/enrich"}, "select public.call_enrich();")
	if got := proposed(g); len(got) != 1 || got[0] != schema.EntryPointKindHTTP {
		t.Errorf("declared secret: the cron job is covered, only the serve root is proposed, got %v", got)
	}
	if got := proposed(vaultLayout(t, nil, "select public.call_enrich();")); len(got) != 2 {
		t.Errorf("undeclared secret: the job and the serve root are proposed, got %v", got)
	}
}
