package validate

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/postgres"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/typescript"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/entrypoints"
	"github.com/salahmyn/lattice/pkg/lattice/graph"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// Two serve handlers and a cron job become proposals that validate takes
// as they are: their handlers are call-graph nodes (no HANDLER_MISSING),
// two with no trigger are not a routing collision (no DUPLICATE_TRIGGER),
// and a route whose handler the graph does not hold is named by one info
// RUNTIME_ROOT_UNPROPOSED, not left silent.
// @verifies entrypoints:INV-5
func TestRuntimeRootProposalsValidate(t *testing.T) {
	ts, pg := typescript.New(), postgres.New(nil, nil)
	var mods []ir.Module
	for p, src := range map[string]string{
		"fn/a/index.ts": "// @module-feature a\nexport function helperA() {}\nDeno.serve(() => new Response('a'));\n",
		"fn/b/index.ts": "// @module-feature b\nDeno.serve(() => new Response('b'));\n",
		"fn/routes.ts":  "// @module-feature a\ndeclare const app: any;\napp.get('/gone', gone);\n",
	} {
		m, err := ts.Parse(t.Context(), p, []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		mods = append(mods, m)
	}
	m, err := pg.Parse(t.Context(), "db/jobs.sql", []byte("-- @module-feature a\nselect cron.schedule('job-a', '0 3 * * *', $$ select 1 $$);\n"))
	if err != nil {
		t.Fatal(err)
	}
	mods = pg.Resolve(append(mods, m))
	var manifests []schema.Manifest
	for _, id := range []string{"a", "b"} {
		manifests = append(manifests, schema.Manifest{ID: id, Version: 1, Status: schema.StatusProduction, Purpose: id,
			SourcePath: "features/" + id + ".yaml"})
	}
	kg := graph.Build(graph.Input{Manifests: manifests, Modules: mods}, graph.Options{})
	g := reach.Build(mods, reach.Options{})
	kg.EntryPoints = entrypoints.ProposeFromRuntime(entrypoints.RuntimeInput{
		Roots: g.RuntimeRoots(), Reach: g, Symbols: kg.Symbols, Features: kg.Features})
	if len(kg.EntryPoints) != 3 {
		t.Fatalf("want 3 proposals (2 serve, 1 cron), got %d", len(kg.EntryPoints))
	}
	r := reach.Compute(reach.Input{Modules: mods, EntryPoints: kg.EntryPoints, Graph: g})
	reach.Annotate(&kg, r)

	count := map[string]int{}
	for _, v := range Validate(kg, config.Default(), Options{Reach: r}) {
		count[v.Code]++
		if v.Code == schema.CodeRuntimeRootUnproposed && v.Severity != schema.SeverityInfo {
			t.Errorf("%s is %s, want info", v.Code, v.Severity)
		}
	}
	if count[schema.CodeHandlerMissing] != 0 || count[schema.CodeDuplicateTrigger] != 0 || count[schema.CodePhantomFlow] != 0 {
		t.Errorf("proposals raised entry-point errors: %v", count)
	}
	if count[schema.CodeRuntimeRootUnproposed] != 1 {
		t.Errorf("want one RUNTIME_ROOT_UNPROPOSED (the route with no handler), got %d", count[schema.CodeRuntimeRootUnproposed])
	}
}
