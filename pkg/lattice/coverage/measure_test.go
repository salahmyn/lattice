package coverage

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/golang"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// TestMeasureThisRepo times the rnd/0009 mapping on this repository's own
// Go code. Opt-in: it runs only when LATTICE_COVERAGE_PROFILE names a
// profile written by
//
//	go test -coverprofile=cover.out ./...
//	LATTICE_COVERAGE_PROFILE=$PWD/cover.out go test ./pkg/lattice/coverage -run TestMeasureThisRepo -v
func TestMeasureThisRepo(t *testing.T) {
	profile := os.Getenv("LATTICE_COVERAGE_PROFILE")
	if profile == "" {
		t.Skip("set LATTICE_COVERAGE_PROFILE to a go test -coverprofile file")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	ad := golang.New()
	var syms []schema.GraphSymbol
	files := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".claude", "node_modules", "examples", "lattice-mcp":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		src, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		mod, err := ad.Parse(context.Background(), rel, src)
		if err != nil {
			return nil
		}
		files++
		for _, s := range mod.Symbols {
			syms = append(syms, schema.GraphSymbol{Name: s.Name, FQN: s.FQN, Kind: string(s.Kind), File: s.File, Line: s.Line, EndLine: s.EndLine, Language: golang.Name})
		}
		return nil
	})
	parsed := time.Since(start)

	start = time.Now()
	data, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	r, err := ParseGoProfile(data, "github.com/salahmyn/lattice")
	if err != nil {
		t.Fatal(err)
	}
	read := time.Since(start)

	start = time.Now()
	behaviourSyms, known, executed, lines, hit := 0, 0, 0, 0, 0
	for _, s := range syms {
		if !behaviour(s) {
			continue
		}
		behaviourSyms++
		c := Of(r, s)
		if !c.Known {
			continue
		}
		known++
		if c.Executed {
			executed++
		}
		lines += c.Lines
		hit += c.Hit
	}
	mapped := time.Since(start)

	start = time.Now()
	edges, err := StaticEdges(root, schema.KnowledgeGraph{Symbols: syms})
	if err != nil {
		t.Fatal(err)
	}
	nEdges := 0
	for _, to := range edges {
		nEdges += len(to)
	}
	scanned := time.Since(start)

	t.Logf("files %d, symbols %d (%d with runtime code), profile %d bytes, %d files in profile",
		files, len(syms), behaviourSyms, len(data), len(r))
	t.Logf("symbols in a profiled file %d, executed %d (%.1f%%), body lines %d, hit %d (%.1f%%)",
		known, executed, pct(executed, known), lines, hit, pct(hit, lines))
	t.Logf("parse sources %v, read profile %v, map onto symbols %v", parsed, read, mapped)
	t.Logf("name-scan static edges: %d edges from %d symbols in %v", nEdges, len(edges), scanned)
}

// TestMeasureGraph times the static scan and reachability on any extracted
// graph, read-only. Opt-in: LATTICE_COVERAGE_GRAPH names a lattice.json and
// LATTICE_COVERAGE_ROOT the code root its paths are relative to.
func TestMeasureGraph(t *testing.T) {
	path, root := os.Getenv("LATTICE_COVERAGE_GRAPH"), os.Getenv("LATTICE_COVERAGE_ROOT")
	if path == "" || root == "" {
		t.Skip("set LATTICE_COVERAGE_GRAPH and LATTICE_COVERAGE_ROOT")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var kg schema.KnowledgeGraph
	if err := json.Unmarshal(data, &kg); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	edges, err := StaticEdges(root, kg)
	if err != nil {
		t.Fatal(err)
	}
	scanned := time.Since(start)
	start = time.Now()
	reach := Reachability(kg, edges)
	walked := time.Since(start)
	n := 0
	for _, to := range edges {
		n += len(to)
	}
	count := map[Reach]int{}
	for _, s := range kg.Symbols {
		if behaviour(s) {
			count[reach[s.FQN]]++
		}
	}
	t.Logf("symbols %d, tests %d, entry points %d, edges %d", len(kg.Symbols), len(kg.Tests), len(kg.EntryPoints), n)
	t.Logf("reach (runtime symbols): entry %d, test-only %d, unreached %d", count[ReachEntry], count[ReachTestOnly], count[ReachNone])
	t.Logf("scan %v, walk %v", scanned, walked)
}
