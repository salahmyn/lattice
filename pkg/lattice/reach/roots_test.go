package reach_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/golang"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/typescript"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func rootSet(roots []reach.Root) map[string]string {
	out := map[string]string{}
	for _, r := range roots {
		out[r.FQN] = r.Reason
	}
	return out
}

// Every automatic root kind and a declared root: an entry point's handler,
// package.json main (a library: its exports) and bin (a script: its
// top-level code, mapped from dist/ back to src/), an index.html module
// script, a Deno.serve handler, Go's func main, and coverage.roots by
// symbol glob and by file glob. Nothing else is a root: not a file named
// main.ts, not an exported symbol of a module no configuration names.
// @verifies reachability:INV-3
func TestRoots(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("lib/package.json", `{"name": "lib", "main": "./index.js", "bin": {"tool": "dist/cli.js"}}`)
	write("web/index.html", `<html><body><script type="module" src="/src/main.tsx"></script></body></html>`)

	ts := map[string]string{
		"lib/index.ts":       "export function api() {}\nfunction internal() {}\nexport class K { m() {} private p() {} }\n",
		"lib/src/cli.ts":     "run();\nfunction run() {}\n",
		"web/src/main.tsx":   "import { App } from './App';\nrender(<App />);\n",
		"web/src/App.tsx":    "export function App() { return null; }\n",
		"edge/fn/index.ts":   "Deno.serve(async (req) => handle(req));\nfunction handle(r: any) {}\n",
		"src/main.ts":        "export function notARoot() {}\n",
		"src/routes/home.ts": "export function Home() {}\n",
		"src/ep.ts":          "export function handler() {}\n",
	}
	mods := parse(t, typescript.New(), ts)
	mods = append(mods, parse(t, golang.New(), map[string]string{
		"cmd/tool/main.go": "package main\n\nfunc main() { start() }\n\nfunc start() {}\n",
		"pkg/x/x.go":       "package x\n\nfunc main() {}\n", // not package main
	})...)
	g := reach.Build(mods, reach.Options{})
	in := reach.RootInput{
		EntryPoints: []schema.EntryPoint{{ID: "ep.http.x", Handler: schema.Handler{Symbol: "src.ep.handler"}}},
		EntryFiles:  reach.EntryFiles([]reach.CodeRoot{{Abs: dir}}, mods),
		Declared: []config.CoverageRoot{
			{Symbol: "pkg.x.*", Reason: "exported API"},
			{File: "src/routes/**", Reason: "route table"},
		},
	}
	got := rootSet(g.Roots(in))
	for fqn, reason := range map[string]string{
		"src.ep.handler":          "entry point ep.http.x",
		"lib.index.api":           "package.json main",
		"lib.index.<top>":         "package.json main",
		"lib.index.K.m":           "package.json main", // an exported method of the API
		"lib.index.K.p":           "",
		"lib.src.cli.<top>":       "package.json bin",
		"web.src.main.<top>":      "index.html",
		"edge.fn.index.<serve>":   "serve",
		"cmd.tool.main":           "main",
		"pkg.x.main":              "coverage.roots: exported API",
		"src.routes.home.Home":    "coverage.roots: route table",
		"src.routes.home.<top>":   "coverage.roots: route table",
		"cmd.tool.<top>":          "main",
		"lib.src.cli.run":         "",
		"lib.index.internal":      "",
		"src.main.notARoot":       "",
		"web.src.App.App":         "",
		"edge.fn.index.handle":    "",
		"cmd.tool.start":          "",
		"pkg.x.<top>":             "",
		"src.routes.home.missing": "",
	} {
		if have, ok := got[fqn]; reason == "" && ok || reason != "" && have != reason {
			t.Errorf("root %s: reason %q, want %q (roots %v)", fqn, have, reason, got)
		}
	}

	// Classified from those roots, everything they call is entry.
	r := reach.Classify(g, g.Roots(in))
	for _, fqn := range []string{"lib.src.cli.run", "web.src.App.App", "edge.fn.index.handle", "cmd.tool.start"} {
		if r.Class[fqn] != schema.ReachEntry {
			t.Errorf("%s: class %q, want entry", fqn, r.Class[fqn])
		}
	}
	if r.Class["lib.index.internal"] != schema.ReachUnreached || r.Class["src.main.notARoot"] != schema.ReachUnreached {
		t.Errorf("internal = %q, notARoot = %q, want unreached", r.Class["lib.index.internal"], r.Class["src.main.notARoot"])
	}
}

// package.json exports and module name library entries too; a build output
// maps back to its source through tsconfig's outDir and rootDir.
// @verifies reachability:INV-3
func TestEntryFilesExportsAndTsconfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pkg", "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "pkg", "package.json"),
		[]byte(`{"exports": {".": {"import": "./out/index.js", "types": "./out/index.d.ts"}, "./util": "./out/util.js"}}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "pkg", "tsconfig.json"),
		[]byte("{\n  // compiled from lib/\n  \"compilerOptions\": {\"outDir\": \"./out\", \"rootDir\": \"./lib\"}\n}"), 0o644)
	mods := parse(t, typescript.New(), map[string]string{
		"pkg/lib/index.ts": "export function a() {}\n",
		"pkg/lib/util.ts":  "export function b() {}\n",
	})
	files := reach.EntryFiles([]reach.CodeRoot{{Abs: dir}}, mods)
	want := map[string]bool{"pkg/lib/index.ts": true, "pkg/lib/util.ts": true}
	for _, f := range files {
		if !want[f.File] || !f.API || f.Reason != "package.json exports" {
			t.Errorf("unexpected entry file %+v", f)
		}
		delete(want, f.File)
	}
	if len(want) > 0 {
		t.Errorf("missing entry files %v (got %+v)", want, files)
	}
}
