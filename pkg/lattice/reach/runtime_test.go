package reach_test

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/postgres"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/typescript"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Modules loaded at run time are edges of the code that loads them: a
// lazy import('./x').then(m => ...) behind JSX, one behind a flag, one
// inside a function, and an unbound require('./x'). Which exports the
// loader uses is not told apart, so the module's top level and every
// export are reached; a bound require stays precise. A computed import(x)
// is an unresolved (dynamic) reference and an IMPORT_UNRESOLVED finding.
// @verifies reachability:INV-1, reachability:INV-2
func TestDynamicImports(t *testing.T) {
	mods := parse(t, typescript.New(), map[string]string{
		"web/main.tsx": `import { lazy } from 'react';
const { used } = require('./bound');
const Console = lazy(() => import('./console/App').then((m) => ({ default: m.App })));
const Demo = flags.dev ? lazy(() => import('./demo/Demo').then((m) => ({ default: m.Demo }))) : null;
render(<Console />);
start(location.hash);
export function start(which: string) {
  void import('./boot').then((m) => m.boot());
  require('./legacy');
  used();
  return import(which);
}
`,
		"web/console/App.tsx":   "import { Panel } from './Panel';\nexport function App() { return <Panel />; }\nexport function Other() {}\nfunction local() {}\n",
		"web/console/Panel.tsx": "export function Panel() { return null; }\n",
		"web/demo/Demo.tsx":     "export function Demo() { return null; }\n",
		"web/boot.ts":           "export async function boot() {}\n",
		"web/legacy.js":         "module.exports = {};\nexport function old() {}\n",
		"web/bound.ts":          "export function used() {}\nexport function unused() {}\n",
	})
	g := reach.Build(mods, reach.Options{})
	roots := g.Roots(reach.RootInput{EntryFiles: []reach.EntryFile{{File: "web/main.tsx", Reason: "index.html"}}})
	r := reach.Classify(g, roots)
	for fqn, want := range map[string]string{
		"web.console.App.App":     schema.ReachEntry, // behind lazy(import().then)
		"web.console.App.Other":   schema.ReachEntry, // every export of a dynamic import
		"web.console.Panel.Panel": schema.ReachEntry, // and what they call
		"web.console.App.local":   schema.ReachUnreached,
		"web.demo.Demo.Demo":      schema.ReachEntry, // behind a dev flag
		"web.boot.boot":           schema.ReachEntry, // import() inside a function
		"web.legacy.old":          schema.ReachEntry, // an unbound require
		"web.bound.used":          schema.ReachEntry,
		"web.bound.unused":        schema.ReachUnreached, // a bound require names what it uses
	} {
		if got := r.Class[fqn]; got != want {
			t.Errorf("%s: class %q, want %q", fqn, got, want)
		}
	}
	if !hasEdge(g, "web.main.start", "web.boot.<top>") || hasEdge(g, "web.main.<top>", "web.boot.<top>") {
		t.Errorf("an import() is an edge of the function that runs it:%s", joined(edges(g, "import")))
	}
	if !contains(unresolved(g), "web.main.start  dynamic") || g.Stats.Dynamic != 1 {
		t.Errorf("a computed import(x) is unresolved (dynamic):%s", joined(unresolved(g)))
	}
	found := false
	for _, v := range reach.Findings(schema.KnowledgeGraph{}, r, nil) {
		if v.Code == schema.CodeImportUnresolved && strings.HasPrefix(v.Subject, "web.main.start:") {
			found = true
		}
	}
	if !found {
		t.Error("IMPORT_UNRESOLVED names the computed import")
	}
}

// Code the runtime runs as a whole: a Deno function's module top level
// beside its serve() call (a store built there for the handler), a tool
// config file (vite.config.ts and what it imports), and a project's own
// rpc('fn') wrapper naming a database function.
// @verifies reachability:INV-1, reachability:INV-3
func TestRuntimeConventions(t *testing.T) {
	mods := parse(t, typescript.New(), map[string]string{
		"fn/feeds/index.ts": "import { makeStore } from './store.ts';\nconst store = makeStore();\nDeno.serve((req) => handle(req, store));\nfunction handle(r: any, s: any) {}\n",
		"fn/feeds/store.ts": "export function makeStore() { return { async secret() { return db.rpc('read_secret'); } }; }\n",
		"app/vite.config.ts": "import { stamp } from './build/stamp';\nexport default defineConfig({ define: { v: stamp() } });\n" +
			"function unusedHelper() {}\n",
		"app/build/stamp.ts":    "export function stamp() { return ''; }\n",
		"app/src/api.ts":        "import { rpc } from './rest';\nexport function report() { void rpc('log_round', { a: 1 }); }\n",
		"app/src/rest.ts":       "export async function rpc(fn: string, args: unknown) { return fetch('/rpc/' + fn); }\n",
		"app/src/main.ts":       "import { report } from './api';\nreport();\n",
		"app/src/configured.ts": "export function notAConfig() {}\n",
	})
	mods = append(mods, parse(t, postgres.New(nil, nil), map[string]string{
		"db/schema.sql": "create function public.read_secret() returns text language sql as $$ select '' $$;\n" +
			"create function public.log_round(a int) returns void language sql as $$ select 1 $$;\n",
	})...)
	g := reach.Build(mods, reach.Options{})
	roots := g.Roots(reach.RootInput{EntryFiles: append(
		reach.EntryFiles([]reach.CodeRoot{{Abs: t.TempDir()}}, mods),
		reach.EntryFile{File: "app/src/main.ts", Reason: "index.html"})})
	got := rootSet(roots)
	if got["fn.feeds.index.<top>"] != "serve" || got["app.vite.config.<top>"] != "tool config" {
		t.Errorf("roots: %v", got)
	}
	r := reach.Classify(g, roots)
	for fqn, want := range map[string]string{
		"fn.feeds.store.makeStore":         schema.ReachEntry, // the serve module's top level calls it
		"sql.public.read_secret":           schema.ReachEntry, // through the store's .rpc
		"app.build.stamp.stamp":            schema.ReachEntry, // the tool config imports it
		"app.vite.config.unusedHelper":     schema.ReachUnreached,
		"sql.public.log_round":             schema.ReachEntry, // rpc('log_round') through the wrapper
		"app.src.configured.notAConfig":    schema.ReachUnreached,
		"app.src.rest.rpc":                 schema.ReachEntry,
		"fn.feeds.index.handle":            schema.ReachEntry,
		"app.src.api.report":               schema.ReachEntry,
		"app.vite.config.<top>":            "", // a node, not a symbol: no class
		"fn.feeds.store.makeStore.missing": "",
	} {
		if r.Class[fqn] != want {
			t.Errorf("%s: class %q, want %q", fqn, r.Class[fqn], want)
		}
	}
}

// SQL a migration applies at its top level runs: a seed select f(...) is a
// root ("migration"), while grant, revoke and comment only name functions.
// A comment with an apostrophe inside a function body opens no string, so
// the calls after it are still read.
// @verifies reachability:INV-1, reachability:INV-3
func TestSQLAppliedAndComments(t *testing.T) {
	mods := parse(t, postgres.New(nil, nil), map[string]string{
		"db/migrations/001.sql": `create function public.import_pack(p jsonb) returns void language plpgsql as $$
begin
  perform public.validate_pack(p);
end;
$$;

create function public.validate_pack(p jsonb) returns void language sql as $$ select 1 $$;

create function public.log_answer(a int) returns void language plpgsql as $$
begin
  -- one lock per family, so each other's writes are seen
  perform pg_advisory_xact_lock(1);
  perform public.store_answer(a);
end;
$$;

create function public.store_answer(a int) returns void language sql as $$ select 1 $$;

create function public.only_granted() returns void language sql as $$ select 1 $$;

grant execute on function public.only_granted() to service_role;
revoke all on function public.validate_pack(jsonb) from public;
comment on function public.only_granted() is 'ops';
`,
		"db/migrations/002_seed.sql": "select public.import_pack('{}'::jsonb);\n",
	})
	g := reach.Build(mods, reach.Options{})
	if !hasEdge(g, "sql.public.log_answer", "sql.public.store_answer") {
		t.Errorf("a call after a comment with an apostrophe:%s", joined(edges(g)))
	}
	roots := rootSet(g.Roots(reach.RootInput{}))
	if roots["sql.applied.db_migrations_002_seed"] != "migration" {
		t.Errorf("the seed is a root: %v", roots)
	}
	for fqn := range roots {
		if fqn == "sql.applied.db_migrations_001" {
			t.Error("a migration that only defines and grants has no applied root")
		}
	}
	r := reach.Classify(g, g.Roots(reach.RootInput{}))
	for fqn, want := range map[string]string{
		"sql.public.import_pack":   schema.ReachEntry,
		"sql.public.validate_pack": schema.ReachEntry,
		"sql.public.only_granted":  schema.ReachUnreached, // granted, never called
		"sql.public.log_answer":    schema.ReachUnreached,
	} {
		if r.Class[fqn] != want {
			t.Errorf("%s: class %q, want %q", fqn, r.Class[fqn], want)
		}
	}
}
