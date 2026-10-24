package reach_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// parse runs an adapter over in-memory files (path -> source), then its
// cross-file Resolve, the way extraction does.
func parse(t *testing.T, a adapters.LanguageAdapter, files map[string]string) []ir.Module {
	t.Helper()
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var mods []ir.Module
	for _, p := range paths {
		m, err := a.Parse(context.Background(), p, []byte(files[p]))
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		mods = append(mods, m)
	}
	if r, ok := a.(adapters.Resolver); ok {
		mods = r.Resolve(mods)
	}
	return mods
}

// edges returns "from -> to" for every resolved edge of the given kinds
// (all kinds when none are given), sorted.
func edges(g *reach.Graph, kinds ...string) []string {
	want := map[string]bool{}
	for _, k := range kinds {
		want[k] = true
	}
	var out []string
	for _, e := range g.Edges {
		if len(want) == 0 || want[e.Kind] {
			out = append(out, e.From+" -> "+e.To)
		}
	}
	sort.Strings(out)
	return out
}

func hasEdge(g *reach.Graph, from, to string) bool {
	for _, e := range g.Edges {
		if e.From == from && e.To == to {
			return true
		}
	}
	return false
}

// unresolved returns "from name why" for each unresolved reference.
func unresolved(g *reach.Graph) []string {
	var out []string
	for _, u := range g.Unresolved {
		out = append(out, u.From+" "+u.Name+" "+u.Why)
	}
	sort.Strings(out)
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func joined(list []string) string { return "\n  " + strings.Join(list, "\n  ") }
