package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/blocks"
	"github.com/salahmyn/lattice/pkg/lattice/buildinfo"
	"github.com/salahmyn/lattice/pkg/lattice/entrypoints"
	"github.com/salahmyn/lattice/pkg/lattice/graph"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

func newRefreshCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Rebuild the block graph from the workspace's sources and judgments",
		Long: "Reads every source declared in workspace.yaml (catalogue/v1 files from a git clone at a ref, or " +
			"from a directory), the judgment records, and the feature manifests, then writes the block graph " +
			"into lattice.json. The rest of lattice.json is kept.",
		RunE: func(_ *cobra.Command, _ []string) error { return runRefresh(io) },
	}
}

type refreshReport struct {
	Blocks   int                  `json:"blocks"`
	Edges    map[string]int       `json:"edges"`
	Sources  []schema.SourceStamp `json:"sources"`
	Skipped  []string             `json:"skipped,omitempty"`
	Warnings []string             `json:"warnings,omitempty"`
}

func runRefresh(io *IO) error {
	ws, err := workspace.Open(io.Repo)
	if err != nil {
		return io.fail("NO_WORKSPACE", err.Error(), "run `lattice init`")
	}
	// shortcut: refresh writes an unsharded lattice.json; add sharded writes when a block workspace needs them
	if _, err := os.Stat(ws.GraphShardDir()); err == nil {
		return io.fail("GRAPH_SHARDED", "refresh does not write sharded graphs yet", nil)
	}
	js, err := blocks.LoadJudgments(ws.JudgmentsDir())
	if err != nil {
		return io.fail("JUDGMENT_INVALID", err.Error(), nil)
	}
	in := blocks.Inputs{Judgments: js}
	var report refreshReport
	for _, s := range ws.Sources {
		if s.Catalogue == "" {
			continue
		}
		stamp := schema.SourceStamp{ID: s.ID, Repo: s.Repo, Ref: s.Ref}
		var files []blocks.File
		switch {
		case s.Clone != "":
			clone := ws.ResolvePath(s.Clone)
			if _, err := os.Stat(clone); err != nil {
				// stop rather than skip: a skipped source would drop its blocks from lattice.json
				return io.fail("SOURCE_MISSING", fmt.Sprintf("source %s: clone %s not found; nothing written", s.ID, clone),
					"clone the repository there, or fix `clone:` in workspace.yaml")
			}
			files, stamp.Commit, stamp.CommitDate, err = blocks.ReadGit(clone, s.Ref, s.Catalogue)
		case s.Path != "":
			files, err = blocks.ReadDir(ws.ResolvePath(s.Path), s.Catalogue)
		default:
			err = fmt.Errorf("needs clone or path")
		}
		if err != nil {
			return io.fail("SOURCE_READ", fmt.Sprintf("source %s: %v", s.ID, err), nil)
		}
		cats, skipped, err := blocks.ParseAll(files)
		if err != nil {
			return io.fail("CATALOGUE_INVALID", fmt.Sprintf("source %s: %v", s.ID, err), nil)
		}
		if len(cats) == 0 {
			return io.fail("SOURCE_EMPTY", fmt.Sprintf("source %s: no catalogue/v1 files match %s; nothing written", s.ID, s.Catalogue), nil)
		}
		for _, p := range skipped {
			report.Skipped = append(report.Skipped, s.ID+":"+p)
		}
		in.Sources = append(in.Sources, blocks.SourceInput{
			Info:       blocks.SourceInfo{ID: s.ID, Prefix: s.Prefix, Tier: s.Tier, System: s.System, Commit: stamp.Commit},
			Catalogues: cats, Stamp: stamp})
	}
	if in.Features, err = loadFeatureManifests(ws.FeaturesDir()); err != nil {
		return io.fail("FEATURE_READ", err.Error(), nil)
	}
	if _, err := os.Stat(ws.EntryPointsDir()); err == nil {
		if in.EntryPoints, err = entrypoints.LoadEntryPoints(ws.EntryPointsDir()); err != nil {
			return io.fail("ENTRY_POINT_READ", err.Error(), nil)
		}
	}

	res := blocks.Build(in)

	kg := schema.KnowledgeGraph{SchemaVersion: buildinfo.SchemaVersion}
	if data, err := os.ReadFile(ws.GraphPath()); err == nil {
		if err := json.Unmarshal(data, &kg); err != nil {
			return io.fail("GRAPH_READ", fmt.Sprintf("%s: %v", ws.GraphPath(), err), nil)
		}
	} else if !os.IsNotExist(err) {
		return io.fail("GRAPH_READ", err.Error(), nil)
	}
	kg.Blocks, kg.BlockEdges, kg.Sources = res.Blocks, res.Edges, res.Sources
	if err := graph.Write(ws.GraphPath(), kg); err != nil {
		return io.fail("GRAPH_WRITE", err.Error(), nil)
	}

	report.Blocks, report.Sources = len(res.Blocks), res.Sources
	report.Edges = map[string]int{}
	for _, e := range res.Edges {
		report.Edges[e.Kind]++
	}
	report.Warnings = append(report.Warnings, res.Warnings...)
	if io.JSON {
		return io.printJSON(report)
	}
	io.printf("wrote %s: %d blocks, %d edges\n", ws.GraphPath(), len(res.Blocks), len(res.Edges))
	kinds := make([]string, 0, len(report.Edges))
	for k := range report.Edges {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		io.printf("  %-14s %d\n", k, report.Edges[k])
	}
	for _, s := range res.Sources {
		c := s.Commit
		if len(c) > 7 {
			c = c[:7]
		}
		io.printf("  source %s @ %s %s\n", s.ID, c, s.CommitDate)
	}
	for _, p := range report.Skipped {
		io.printf("  skipped (not catalogue/v1): %s\n", p)
	}
	for _, w := range report.Warnings {
		io.errorf("warning: %s\n", w)
	}
	return nil
}

func loadFeatureManifests(dir string) ([]schema.Manifest, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var out []schema.Manifest
	for _, p := range paths {
		m, err := schema.LoadManifest(p)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		out = append(out, *m)
	}
	return out, nil
}
