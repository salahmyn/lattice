package reach_test

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/golang"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/python"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/typescript"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
)

// resolveCase is one expectation of a resolution test: an edge that must
// exist, or an unresolved reference.
type resolveCase struct {
	what     string
	from, to string // an edge from -> to
	ambig    string // or: "from name ambiguous"
}

func checkResolution(t *testing.T, g *reach.Graph, cases []resolveCase, absent [][2]string) {
	t.Helper()
	for _, c := range cases {
		if c.ambig != "" {
			if !contains(unresolved(g), c.ambig) {
				t.Errorf("%s: want unresolved %q, have%s", c.what, c.ambig, joined(unresolved(g)))
			}
			continue
		}
		if !hasEdge(g, c.from, c.to) {
			t.Errorf("%s: want edge %s -> %s, have%s", c.what, c.from, c.to, joined(edges(g)))
		}
	}
	for _, a := range absent {
		if hasEdge(g, a[0], a[1]) {
			t.Errorf("guessed edge %s -> %s", a[0], a[1])
		}
	}
}

// Go: an import alias, a same-package call, a method on the receiver, a
// typed local, a member name unique in the language, and an ambiguous one
// left unresolved. A function named as a value (a callback) is an edge;
// the standard library, and an outside path that ends in a local
// directory name, resolve to nothing.
// @verifies reachability:INV-1, reachability:INV-2
func TestResolveGo(t *testing.T) {
	mods := parse(t, golang.New(), map[string]string{
		"pkg/a/a.go": `package a

import (
	r "example.com/m/pkg/b"
	"example.com/m/pkg/c"
	oc "github.com/other/pkg/c" // outside the workspace, though it ends in pkg/c
	"strings"
	"time"
)

type T struct{}

func (t *T) Run() {
	t.helper()
	r.Do()
	c.Shared()
	local()
}

func (t *T) helper() {}

func local() {
	var x c.Box
	x.Open()
	v := pick()
	v.Unique()
	v.Common()
	strings.ToUpper("x")
	time.Now().Add(time.Second)
}

func pick() *c.Box { return nil }

func Main() {
	run(handler)
}

func run(f func()) { f() }

func handler() {}

func ext() { oc.Shared() }
`,
		"pkg/b/b.go": "package b\n\nfunc Do() {}\n",
		"pkg/c/c.go": `package c

type Box struct{}

func (b *Box) Open()   {}
func (b *Box) Unique() {}
func (b *Box) Common() {}
func (b *Box) Add(int) {}

func Shared() {}

type Other struct{}

func (o *Other) Common() {}
`,
	})
	g := reach.Build(mods, reach.Options{})
	checkResolution(t, g, []resolveCase{
		{what: "receiver method", from: "pkg.a.T.Run", to: "pkg.a.T.helper"},
		{what: "import alias", from: "pkg.a.T.Run", to: "pkg.b.Do"},
		{what: "import", from: "pkg.a.T.Run", to: "pkg.c.Shared"},
		{what: "same package", from: "pkg.a.T.Run", to: "pkg.a.local"},
		{what: "typed local", from: "pkg.a.local", to: "pkg.c.Box.Open"},
		{what: "unique member", from: "pkg.a.local", to: "pkg.c.Box.Unique"},
		{what: "ambiguous member", ambig: "pkg.a.local Common ambiguous"},
		{what: "callback value", from: "pkg.a.Main", to: "pkg.a.handler"},
		{what: "import edge", from: "pkg.a.<top>", to: "pkg.c.<top>"},
	}, [][2]string{
		{"pkg.a.local", "pkg.c.Box.Common"},
		{"pkg.a.local", "pkg.c.Other.Common"},
		{"pkg.a.local", "pkg.c.Box.Add"}, // time.Time.Add is no workspace method
		{"pkg.a.ext", "pkg.c.Shared"},    // github.com/other/pkg/c is not ours
	})
	if g.Stats.Ambiguous != 1 || g.Stats.Unresolved != 1 {
		t.Errorf("stats = %+v, want one unresolved (ambiguous) reference", g.Stats)
	}
}

// TypeScript: a named import with an alias, a default import, a namespace
// import, a same-module call, a method on this, a re-export through an
// index file, a unique member and an ambiguous one.
// @verifies reachability:INV-1, reachability:INV-2
func TestResolveTypeScript(t *testing.T) {
	mods := parse(t, typescript.New(), map[string]string{
		"src/a.ts": `import Def, { helper as h } from './b.js';
import * as ns from './c';
import { reexported, star } from './index';

export function run(obj: any) {
  h();
  Def();
  ns.fromC();
  local();
  reexported();
  star();
  obj.uniqueMethod();
  obj.common();
}

function local() {}

export class K {
  m() { this.n(); }
  n() {}
}
`,
		"src/b.ts":     "export function helper() {}\nexport default function Defaulted() {}\n",
		"src/c.ts":     "export function fromC() {}\nexport function star() {}\n",
		"src/index.ts": "export { helper as reexported } from './b';\nexport * from './c';\n",
		"src/d.ts": `export class A {
  uniqueMethod() {}
  common() {}
}
export class B {
  common() {}
}
`,
	})
	g := reach.Build(mods, reach.Options{})
	checkResolution(t, g, []resolveCase{
		{what: "import alias", from: "src.a.run", to: "src.b.helper"},
		{what: "default import", from: "src.a.run", to: "src.b.Defaulted"},
		{what: "namespace import", from: "src.a.run", to: "src.c.fromC"},
		{what: "same module", from: "src.a.run", to: "src.a.local"},
		{what: "this", from: "src.a.K.m", to: "src.a.K.n"},
		{what: "named re-export", from: "src.a.run", to: "src.b.helper"},
		{what: "export *", from: "src.a.run", to: "src.c.star"},
		{what: "unique member", from: "src.a.run", to: "src.d.A.uniqueMethod"},
		{what: "ambiguous member", ambig: "src.a.run common ambiguous"},
	}, [][2]string{{"src.a.run", "src.d.A.common"}, {"src.a.run", "src.d.B.common"}})
}

// Python: a relative import with an alias, an aliased module import, a
// submodule imported from its package, a same-module call, self, a unique
// member and an ambiguous one.
// @verifies reachability:INV-1, reachability:INV-2
func TestResolvePython(t *testing.T) {
	mods := parse(t, python.New(), map[string]string{
		"app/__init__.py": "",
		"app/a.py": `from .b import helper as h
import app.c as cc
from app import d


class K:
    def m(self, obj):
        self.n()
        h()
        local()
        cc.fromc()
        d.fd()
        obj.unique_method()
        obj.common()

    def n(self):
        pass


def local():
    pass
`,
		"app/b.py": "def helper():\n    pass\n",
		"app/c.py": "def fromc():\n    pass\n",
		"app/d.py": "def fd():\n    pass\n",
		"app/e.py": `class A:
    def unique_method(self):
        pass

    def common(self):
        pass


class B:
    def common(self):
        pass
`,
	})
	g := reach.Build(mods, reach.Options{})
	checkResolution(t, g, []resolveCase{
		{what: "self", from: "app.a.K.m", to: "app.a.K.n"},
		{what: "relative import alias", from: "app.a.K.m", to: "app.b.helper"},
		{what: "same module", from: "app.a.K.m", to: "app.a.local"},
		{what: "aliased module import", from: "app.a.K.m", to: "app.c.fromc"},
		{what: "submodule from its package", from: "app.a.K.m", to: "app.d.fd"},
		{what: "unique member", from: "app.a.K.m", to: "app.e.A.unique_method"},
		{what: "ambiguous member", ambig: "app.a.K.m common ambiguous"},
	}, [][2]string{{"app.a.K.m", "app.e.A.common"}, {"app.a.K.m", "app.e.B.common"}})
}
