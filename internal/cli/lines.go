package cli

// @module-feature audit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/coverage"
	"github.com/salahmyn/lattice/pkg/lattice/lines"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// linesReport joins the call graph (rr when the graph was just built, else
// callgraph.json), the verifiers' line data (verifier-lines.json, read only
// when it is of HEAD's tree and the working tree is clean), the breaks, the
// suite coverage and the per-test coverage into the lines section of the
// audit. Every input that is missing or stale reads as unknown lines, never
// as covered ones.
func linesReport(ws *workspace.Workspace, kg schema.KnowledgeGraph, cfg config.Config, rr *reach.Result, dirty bool) lines.Report {
	in := lines.Input{KG: kg, Coverage: coverageIndex(ws, kg)}

	switch {
	case rr != nil && rr.Graph != nil:
		for _, e := range rr.Graph.Edges {
			in.Edges = append(in.Edges, lines.Edge{From: e.From, To: e.To, Kind: e.Kind})
		}
	default:
		if c, ok := reach.LoadCache(ws.LatticeDir); ok {
			for _, e := range c.Edges {
				in.Edges = append(in.Edges, lines.Edge{From: e.From, To: e.To, Kind: e.Kind})
			}
		}
	}

	if f, ok, err := lines.Load(ws.LatticeDir); err != nil {
		in.VerifierWhy = err.Error()
	} else if ok {
		in.Verifier = f
		tree := headTree(ws.PrimaryCodeRoot().Abs)
		switch {
		case dirty:
			in.VerifierWhy = "the working tree has uncommitted changes, so the lines recorded for HEAD may have moved"
		case tree == "" || f.Tree != tree:
			in.VerifierWhy = "verifier-lines.json is of tree " + short8(f.Tree) + ", HEAD is " + short8(tree) + " (run lattice break --lines)"
		}
	}

	var why string
	in.Breaks, why = lines.LoadBreaks(ws.LatticeDir, kg.GeneratedFromCommit)
	in.BreaksWhy = why

	if cfg.CoverageConfigured() {
		rep, inc, problems := coverage.SuiteReport(filepath.Dir(ws.LatticeDir), kg, cfg.CoverageConfig())
		in.Suite, in.SuiteIncluded, in.SuiteProblems = rep, inc, problems
	}

	roots := reachCodeRoots(ws)
	in.Source = func(file string) ([]byte, bool) {
		for _, r := range roots {
			rel := file
			if r.Prefix != "" {
				var ok bool
				if rel, ok = strings.CutPrefix(file, r.Prefix+"/"); !ok {
					continue
				}
			}
			if data, err := os.ReadFile(filepath.Join(r.Abs, filepath.FromSlash(rel))); err == nil {
				return data, true
			}
		}
		return nil, false
	}
	return lines.Compute(in)
}

// headTree is the tree object of HEAD in the repository that holds dir, ""
// when it cannot be read.
func headTree(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD^{tree}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func short8(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	if s == "" {
		return "(none)"
	}
	return s
}
