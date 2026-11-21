package reach_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/python"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/typescript"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/slice"
)

// Two edge functions, each an index that serves a handler, sharing a utility
// module. Function a calls an SDK method named create; function b has an
// object literal with a create of its own.
func edgeFunctions() map[string]string {
	return map[string]string{
		"fn/a/index.ts":      "import { handler } from './handler.ts';\nDeno.serve(handler);\n",
		"fn/a/handler.ts":    "import { makeClient } from './claude.ts';\nimport { shared } from '../_shared/util.ts';\nexport function handler(req: Request) { shared(); return makeClient(); }\n",
		"fn/a/claude.ts":     "import Sdk from 'npm:sdk';\nexport function makeClient() {\n  const client = new Sdk();\n  return client.messages.create({});\n}\n",
		"fn/b/index.ts":      "import { handler } from './handler.ts';\nDeno.serve(handler);\n",
		"fn/b/handler.ts":    "import { shared } from '../_shared/util.ts';\nexport const actions = {\n  /** @enforces fees:INV-1 */\n  create() { return 1; },\n};\nexport function handler(req: Request) { shared(); return actions; }\n",
		"fn/_shared/util.ts": "export function shared() {}\n",
	}
}

func crossFunctionCalls(g *reach.Graph) []string {
	var out []string
	for _, e := range g.Edges {
		if e.Kind == "call" && strings.HasPrefix(e.From, "fn.a.") && strings.HasPrefix(e.To, "fn.b.") ||
			e.Kind == "call" && strings.HasPrefix(e.From, "fn.b.") && strings.HasPrefix(e.To, "fn.a.") {
			out = append(out, e.From+" -> "+e.To)
		}
	}
	return out
}

// A member call on a value of unknown type resolves by name only into a
// module that can be loaded with the caller's. Nothing imports both edge
// functions, so the SDK's create is external, not the other function's.
// @verifies reachability:INV-9
func TestMemberCallIgnoresAModuleNeverLoadedWithTheCaller(t *testing.T) {
	g := reach.Build(parse(t, typescript.New(), edgeFunctions()), reach.Options{})
	if got := crossFunctionCalls(g); len(got) != 0 {
		t.Errorf("cross-function call edges, want none:%s", joined(got))
	}
	for _, u := range g.Unresolved {
		if strings.HasPrefix(u.From, "fn.a.") {
			t.Errorf("a's call is unresolved, want external: %+v", u)
		}
	}
}

// Dependency injection: the entry point imports the handler and the store,
// the handler imports neither store nor anything that does, and still its
// deps.store.save() reaches the store.
// @verifies reachability:INV-9
func TestMemberCallThroughInjectionStaysResolved(t *testing.T) {
	g := reach.Build(parse(t, typescript.New(), map[string]string{
		"fn/index.ts":   "import { createHandler } from './handler.ts';\nimport { createStore } from './store.ts';\nDeno.serve(createHandler({ store: createStore() }));\n",
		"fn/handler.ts": "export function createHandler(deps: any) { return () => deps.store.save(); }\n",
		"fn/store.ts":   "export class Store { save() {} }\nexport function createStore() { return new Store(); }\n",
	}), reach.Options{})
	checkResolution(t, g, []resolveCase{
		{what: "injected store", from: "fn.handler.createHandler", to: "fn.store.Store.save"},
	}, nil)
}

// Shared code both functions import is co-loaded with each of them.
// @verifies reachability:INV-9
func TestMemberCallIntoSharedCodeStaysResolved(t *testing.T) {
	files := edgeFunctions()
	files["fn/_shared/x.ts"] = "export class Tools { run() {} }\n"
	files["fn/a/claude.ts"] = "import Sdk from 'npm:sdk';\nimport './../_shared/x.ts';\nexport function makeClient(c: any) { return c.run(); }\n"
	files["fn/b/handler.ts"] = "import './../_shared/x.ts';\nimport { shared } from '../_shared/util.ts';\nexport function handler(req: Request) { shared(); }\n"
	g := reach.Build(parse(t, typescript.New(), files), reach.Options{})
	if !hasEdge(g, "fn.a.claude.makeClient", "fn._shared.x.Tools.run") {
		t.Errorf("a's .run() should reach the shared member, have%s", joined(edges(g, "call")))
	}
}

// Two candidates in modules both loaded with the caller stay ambiguous.
// @verifies reachability:INV-9
func TestMemberCallWithTwoLoadedCandidatesIsAmbiguous(t *testing.T) {
	g := reach.Build(parse(t, typescript.New(), map[string]string{
		"app/main.ts": "import './use.ts';\nimport './x.ts';\nimport './y.ts';\n",
		"app/use.ts":  "export function go(o: any) { o.pick(); }\n",
		"app/x.ts":    "export class X { pick() {} }\n",
		"app/y.ts":    "export class Y { pick() {} }\n",
	}), reach.Options{})
	if !contains(unresolved(g), "app.use.go pick ambiguous") {
		t.Errorf("want an ambiguous call, have%s", joined(unresolved(g)))
	}
	if cs := crossCalls(g, "app.use.go"); len(cs) != 0 {
		t.Errorf("an ambiguous call draws no edge:%s", joined(cs))
	}
}

func crossCalls(g *reach.Graph, from string) []string {
	var out []string
	for _, e := range g.Edges {
		if e.From == from && e.Kind == "call" {
			out = append(out, e.From+" -> "+e.To)
		}
	}
	return out
}

// A test that imports both functions does not load them together for
// production code, but a test's own calls may see the loaded set.
// @verifies reachability:INV-9
func TestTestImportingBothDoesNotJoinProductionCode(t *testing.T) {
	files := edgeFunctions()
	files["fn/both.test.ts"] = "import './a/claude.ts';\nimport './b/handler.ts';\nimport { test } from 'node:test';\ntest('x', () => {});\n"
	g := reach.Build(parse(t, typescript.New(), files), reach.Options{})
	if got := crossFunctionCalls(g); len(got) != 0 {
		t.Errorf("a test's imports joined production code:%s", joined(got))
	}

	// A test that makes the call itself is a test owner: the test run loads
	// both modules, so the member is a candidate there.
	files["fn/both.test.ts"] = "import './a/claude.ts';\nimport './b/handler.ts';\nexport function probe(o: any) { return o.create(); }\n"
	g = reach.Build(parse(t, typescript.New(), files), reach.Options{})
	if got := crossFunctionCalls(g); len(got) != 0 {
		t.Errorf("production calls still stay apart:%s", joined(got))
	}
	if !hasEdge(g, "fn.both.test.probe", "fn.b.handler.actions.create") {
		t.Errorf("a test's own call sees the modules its run loads, have%s", joined(edges(g, "call")))
	}
}

// Python resolves by the same rule.
// @verifies reachability:INV-9
func TestMemberCallCoLoadedPython(t *testing.T) {
	g := reach.Build(parse(t, python.New(), map[string]string{
		"a/__init__.py": "",
		"b/__init__.py": "",
		"a/svc.py":      "def run(client):\n    client.create()\n",
		"b/act.py":      "class Actions:\n    def create(self):\n        pass\n",
	}), reach.Options{})
	if got := crossCalls(g, "a.svc.run"); len(got) != 0 {
		t.Errorf("python call resolved into a module never loaded with it:%s", joined(got))
	}
	g = reach.Build(parse(t, python.New(), map[string]string{
		"a/__init__.py": "",
		"b/__init__.py": "",
		"main.py":       "import a.svc\nimport b.act\n",
		"a/svc.py":      "def run(client):\n    client.create()\n",
		"b/act.py":      "class Actions:\n    def create(self):\n        pass\n",
	}), reach.Options{})
	if !hasEdge(g, "a.svc.run", "b.act.Actions.create") {
		t.Errorf("a module that imports both loads them together, have%s", joined(edges(g, "call")))
	}
}

// A slice from a's serve finds no path into b's handler.
// @verifies reachability:INV-9
func TestSliceFindsNoPathBetweenEdgeFunctions(t *testing.T) {
	g := reach.Build(parse(t, typescript.New(), edgeFunctions()), reach.Options{})
	rr := reach.Classify(g, g.Roots(reach.RootInput{}))
	sg := slice.NewGraph(rr, schema.KnowledgeGraph{}, nil)
	for _, fqn := range []string{"fn.b.handler.actions.create", "fn.b.handler.handler"} {
		rec := slice.Compute(sg, "x:INV-1", []slice.Enforcer{{FQN: fqn}})
		for _, p := range rec.Paths {
			if strings.Contains(slice.PathLine(p), "fn.a.") {
				t.Errorf("%s: a path from a's code into b: %s", fqn, slice.PathLine(p))
			}
		}
	}
}

// The ancestor sets keep co-loading cheap on a large repository: a few
// thousand modules, each importing its neighbour and a shared one, each
// calling a by-name member.
// @verifies reachability:INV-9
func TestCoLoadingScalesToThousandsOfModules(t *testing.T) {
	const n = 3000
	files := map[string]string{"lib/shared.ts": "export function shared() {}\n"}
	for i := 0; i < n; i++ {
		next := ""
		if i+1 < n {
			next = fmt.Sprintf("import './m%d.ts';\n", i+1)
		}
		files[fmt.Sprintf("lib/m%d.ts", i)] = fmt.Sprintf("import '../lib/shared.ts';\n%sexport class O%d { act() {} }\nexport function f%d(x: any) { return x.act(); }\n", next, i, i)
	}
	mods := parse(t, typescript.New(), files)
	start := time.Now()
	g := reach.Build(mods, reach.Options{})
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("Build took %s for %d modules", d, n)
	}
	// m0 imports m1 ... : m5's act is co-loaded with m0's call, so it is
	// ambiguous among every module loaded with it.
	if !contains(unresolved(g), "lib.m0.f0 act ambiguous") {
		t.Errorf("want an ambiguous act call from m0")
	}
}
