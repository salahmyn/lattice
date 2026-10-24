package validate

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/typescript"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/graph"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// reachGraph is a small TS workspace: main (feature app, the entry point's
// handler) calls charge (feature billing, on no entry-point flow) and
// used; corpse (feature dead) and lonely are called by nothing.
func reachGraph(t *testing.T, withEntryPoint bool) (schema.KnowledgeGraph, *reach.Result) {
	t.Helper()
	files := map[string]string{
		"src/main.ts":    "// @module-feature app\nimport { charge } from './billing';\nimport { used } from './lib';\nexport function main() { charge(); used(); }\n",
		"src/billing.ts": "// @module-feature billing\nexport function charge() {}\n",
		"src/lib.ts":     "// @module-feature app\nexport function used() {}\nexport function lonely() {}\n",
		"src/dead.ts":    "// @module-feature dead\nexport function corpse() {}\n",
	}
	a := typescript.New()
	var mods []ir.Module
	for _, p := range []string{"src/billing.ts", "src/dead.ts", "src/lib.ts", "src/main.ts"} {
		m, err := a.Parse(context.Background(), p, []byte(files[p]))
		if err != nil {
			t.Fatal(err)
		}
		mods = append(mods, m)
	}
	mods = a.Resolve(mods)
	var manifests []schema.Manifest
	for _, id := range []string{"app", "billing", "dead"} {
		manifests = append(manifests, schema.Manifest{ID: id, Version: 1, Status: schema.StatusProduction,
			Purpose: id, SourcePath: "features/" + id + ".yaml"})
	}
	kg := graph.Build(graph.Input{Manifests: manifests, Modules: mods}, graph.Options{})
	if withEntryPoint {
		kg.EntryPoints = []schema.EntryPoint{{ID: "ep.cli.main", Kind: schema.EntryPointKindCLI,
			Status: schema.StatusProduction, Handler: schema.Handler{Symbol: "src.main.main"},
			Flow: []schema.FlowStep{{Feature: "app"}}}}
	}
	r := reach.Compute(reach.Input{Modules: mods, EntryPoints: kg.EntryPoints})
	reach.Annotate(&kg, r)
	return kg, r
}

func findingKeys(v []schema.Violation, skip ...string) []string {
	drop := map[string]bool{}
	for _, c := range skip {
		drop[c] = true
	}
	var out []string
	for _, x := range v {
		if !drop[x.Code] {
			out = append(out, string(x.Severity)+" "+x.Code+" "+x.Subject+" "+x.FeatureID+" "+x.Message)
		}
	}
	sort.Strings(out)
	return out
}

func featureUnreached(v []schema.Violation) []string {
	var out []string
	for _, x := range v {
		if x.Code == schema.CodeFeatureUnreached {
			out = append(out, x.FeatureID)
		}
	}
	sort.Strings(out)
	return out
}

// FEATURE_UNREACHED without a call graph fires for every feature off the
// entry points' flows; with roots, a feature a root reaches through its
// symbols (billing) is reached, and only the dead one fires. With no roots
// the flow-based rule is unchanged.
// @verifies reachability:INV-7
func TestFeatureUnreachedWithRoots(t *testing.T) {
	kg, r := reachGraph(t, true)
	cfg := config.Default()
	if got := featureUnreached(Validate(kg, cfg, Options{})); !reflect.DeepEqual(got, []string{"billing", "dead"}) {
		t.Errorf("flow rule: FEATURE_UNREACHED for %v", got)
	}
	v := Validate(kg, cfg, Options{Reach: r})
	if got := featureUnreached(v); !reflect.DeepEqual(got, []string{"dead"}) {
		t.Errorf("with roots: FEATURE_UNREACHED for %v", got)
	}
	for _, x := range v {
		if x.Code == schema.CodeFeatureUnreached && !strings.Contains(x.Message, "no root reaches any of its symbols") {
			t.Errorf("message = %q", x.Message)
		}
	}

	// No entry point and no other root: nothing is classified, and the rule
	// stays silent as before.
	kg, r = reachGraph(t, false)
	if r.HasRoots() {
		t.Fatal("the graph should have no roots")
	}
	if got := featureUnreached(Validate(kg, cfg, Options{Reach: r})); len(got) != 0 {
		t.Errorf("no roots: FEATURE_UNREACHED for %v", got)
	}
}

// No change without config: with no coverage: block and no severity
// overrides, a call graph changes no finding but FEATURE_UNREACHED, and
// that only when the graph has roots. A coverage: block alone does not
// switch the reachability codes on.
// @verifies reachability:INV-6
func TestReachNoChangeWithoutConfig(t *testing.T) {
	for _, withRoots := range []bool{true, false} {
		kg, r := reachGraph(t, withRoots)
		for _, cfg := range []config.Config{config.Default(), func() config.Config {
			c := config.Default()
			c.Coverage = &config.Coverage{}
			return c
		}()} {
			before := Validate(kg, cfg, Options{})
			after := Validate(kg, cfg, Options{Reach: r})
			skip := []string{}
			if withRoots {
				skip = append(skip, schema.CodeFeatureUnreached)
			}
			if b, a := findingKeys(before, skip...), findingKeys(after, skip...); !reflect.DeepEqual(b, a) {
				t.Errorf("roots=%v coverage=%v: findings changed\nbefore:%s\nafter:%s", withRoots, cfg.Coverage != nil,
					strings.Join(b, "\n  "), strings.Join(a, "\n  "))
			}
			if !withRoots && !reflect.DeepEqual(findingKeys(before), findingKeys(after)) {
				t.Errorf("no roots: FEATURE_UNREACHED changed")
			}
		}
	}
}

// Each reachability code is off by default and comes on alone when its
// severity is set; enabling a per-symbol code brings REACHABILITY_NO_ROOTS
// (when there are no roots) at info.
// @verifies reachability:INV-6
func TestReachCodesOffUntilEnabled(t *testing.T) {
	kg, r := reachGraph(t, true)
	reachCodes := func(v []schema.Violation) []string {
		var out []string
		for _, x := range v {
			switch x.Code {
			case schema.CodeSymbolUnreached, schema.CodeSymbolTestOnly, schema.CodeSymbolUntested,
				schema.CodeUnusedExport, schema.CodeReachabilityNoRoots, schema.CodeRPCUnresolved, schema.CodeSuiteOverlap:
				out = append(out, string(x.Severity)+" "+x.Code+" "+x.Subject)
			}
		}
		sort.Strings(out)
		return out
	}
	cfg := config.Default()
	if got := reachCodes(Validate(kg, cfg, Options{Reach: r})); len(got) != 0 {
		t.Errorf("off by default: %v", got)
	}
	cfg.Validate.Severity = map[string]string{schema.CodeSymbolUnreached: "warning"}
	got := reachCodes(Validate(kg, cfg, Options{Reach: r}))
	want := []string{"warning SYMBOL_UNREACHED src.dead.corpse", "warning SYMBOL_UNREACHED src.lib.lonely"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SYMBOL_UNREACHED on: %v, want %v", got, want)
	}
	cfg.Validate.Severity = map[string]string{schema.CodeUnusedExport: "info"}
	if got := reachCodes(Validate(kg, cfg, Options{Reach: r})); !reflect.DeepEqual(got,
		[]string{"info UNUSED_EXPORT src.dead.corpse", "info UNUSED_EXPORT src.lib.lonely"}) {
		t.Errorf("UNUSED_EXPORT on: %v", got)
	}

	kg, r = reachGraph(t, false)
	cfg.Validate.Severity = map[string]string{schema.CodeSymbolUnreached: "warning"}
	if got := reachCodes(Validate(kg, cfg, Options{Reach: r})); !reflect.DeepEqual(got, []string{"info REACHABILITY_NO_ROOTS reachability"}) {
		t.Errorf("no roots: %v", got)
	}
}
