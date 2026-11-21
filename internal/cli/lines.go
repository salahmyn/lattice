package cli

// @module-feature audit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
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

	in.Verifier, in.VerifierWhy = verifierLines(ws, dirty)

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

// verifierLines reads verifier-lines.json, only when it is of HEAD's tree and
// the working tree is clean. why says what is wrong with a file that is
// present but not usable; both are empty when there is no file.
func verifierLines(ws *workspace.Workspace, dirty bool) (f *lines.File, why string) {
	f, ok, err := lines.Load(ws.LatticeDir)
	switch {
	case err != nil:
		return nil, err.Error()
	case !ok:
		return nil, ""
	}
	tree := headTree(ws.PrimaryCodeRoot().Abs)
	switch {
	case dirty:
		return f, "the working tree has uncommitted changes, so the lines recorded for HEAD may have moved"
	case tree == "" || f.Tree != tree:
		return f, "verifier-lines.json is of tree " + short8(f.Tree) + ", HEAD is " + short8(tree) + " (run lattice break --lines)"
	}
	return f, ""
}

// computeAudit is audit.Compute over the workspace's evidence, with the call
// graph (rr, when the graph was just built) and the verifiers' line data the
// entry-point proof reads.
func computeAudit(ws *workspace.Workspace, kg schema.KnowledgeGraph, rr *reach.Result, dirty bool) audit.Report {
	src := audit.Collect(ws.LatticeDir, kg, dirty)
	src.Calls = auditCallGraph(ws, kg, rr)
	if f, why := verifierLines(ws, dirty); f != nil && why == "" {
		src.VerifierLines = f
	}
	return audit.Compute(src)
}

// auditCallGraph is the call graph for the audit: rr's, with the runtime
// roots no declared entry point covers as further ways in, else
// callgraph.json (which keeps no roots). nil when there is neither.
func auditCallGraph(ws *workspace.Workspace, kg schema.KnowledgeGraph, rr *reach.Result) *audit.CallGraph {
	g := &audit.CallGraph{}
	switch {
	case rr != nil && rr.Graph != nil:
		for _, e := range rr.Graph.Edges {
			g.Edges = append(g.Edges, audit.CallEdge{From: e.From, To: e.To, Kind: e.Kind})
		}
		for _, u := range rr.Graph.Unresolved {
			g.Unresolved = append(g.Unresolved, audit.CallUnresolved{From: u.From, Kind: u.Kind, Name: u.Name, Qualifier: u.Qualifier, Why: u.Why})
		}
		for _, r := range rr.Graph.RuntimeRoots() {
			if r.Blocked != "" || r.FQN == "" || r.CoveredBy(kg.EntryPoints) {
				continue
			}
			label := fmt.Sprintf("runtime root %s:%d", r.File, r.Line)
			g.Roots = append(g.Roots, audit.WayIn{ID: label, Handler: r.FQN, Kind: r.Kind, Source: audit.WayRuntimeRoot})
		}
	default:
		c, ok := reach.LoadCache(ws.LatticeDir)
		if !ok {
			return nil
		}
		for _, e := range c.Edges {
			g.Edges = append(g.Edges, audit.CallEdge{From: e.From, To: e.To, Kind: e.Kind})
		}
		for _, u := range c.Unresolved {
			g.Unresolved = append(g.Unresolved, audit.CallUnresolved{From: u.From, Kind: u.Kind, Name: u.Name, Qualifier: u.Qualifier, Why: u.Why})
		}
	}
	return g
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
