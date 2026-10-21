package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/blocks"
	"github.com/salahmyn/lattice/pkg/lattice/entrypoints"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/store"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

func newRefreshCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Rebuild the block graph from the workspace's sources and judgments",
		Long: "Reads every source declared in workspace.yaml (catalogue/v1 files from a git clone at a ref, or " +
			"from a directory), the judgment records, and the feature manifests, then writes the block graph " +
			"into the workspace store: lattice.json in files mode (the rest of it is kept), the database in postgres mode.",
		RunE: func(_ *cobra.Command, _ []string) error { return runRefresh(io) },
	}
}

type refreshReport struct {
	Blocks   int                  `json:"blocks"`
	Edges    map[string]int       `json:"edges"`
	Sources  []schema.SourceStamp `json:"sources"`
	Skipped  []string             `json:"skipped,omitempty"`
	Warnings []string             `json:"warnings,omitempty"`
	Summary  string               `json:"summary"`
	Diff     blocks.Diff          `json:"diff"`
	Flows    int                  `json:"flows"`
}

func runRefresh(io *IO) error {
	ctx := context.Background()
	ws, err := workspace.Open(io.Repo)
	if err != nil {
		return io.fail("NO_WORKSPACE", err.Error(), "run `lattice init`")
	}
	if ws.Storage.Backend == "files" {
		// shortcut: refresh writes an unsharded lattice.json; add sharded writes when a block workspace needs them
		if _, err := os.Stat(ws.GraphShardDir()); err == nil {
			return io.fail("GRAPH_SHARDED", "refresh does not write sharded graphs yet", nil)
		}
	}
	st, err := store.Open(ctx, ws)
	if err != nil {
		return io.fail("STORE_OPEN", err.Error(), nil)
	}
	defer st.Close()
	recs, err := st.List(ctx, "judgment")
	if err != nil {
		return io.fail("STORE_READ", err.Error(), nil)
	}
	var js []blocks.Judgment
	for _, r := range recs {
		j, err := blocks.ParseJudgment(r.Body, "judgment "+r.ID)
		if err != nil {
			return io.fail("JUDGMENT_INVALID", err.Error(), nil)
		}
		js = append(js, j)
	}
	in := blocks.Inputs{Judgments: js}
	if in.Proposals, err = loadProposals(ctx, st); err != nil {
		return io.fail(proposalCode(err), err.Error(), nil)
	}
	var report refreshReport
	for _, s := range ws.Sources {
		if s.Catalogue == "" {
			continue
		}
		stamp := schema.SourceStamp{ID: s.ID, Repo: s.Repo, Ref: s.Ref, System: s.System}
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
			if err == nil {
				var rel *schema.Release
				if rel, err = readRelease(ws, s, &report); err != nil {
					return io.fail("RELEASE_INVALID", fmt.Sprintf("source %s: %v", s.ID, err), nil)
				}
				stamp.State, stamp.Behind, stamp.Ahead = blocks.Freshness(clone, stamp.Commit, rel)
				stamp.Release = rel
			}
		case s.Path != "":
			files, err = blocks.ReadDir(ws.ResolvePath(s.Path), s.Catalogue)
			if err == nil && s.Stamp != "" {
				var data []byte
				if data, err = os.ReadFile(ws.ResolvePath(s.Stamp)); err == nil {
					var snap schema.SourceStamp
					if snap, err = blocks.ParseSnapshot(data); err != nil {
						return io.fail("STAMP_INVALID", fmt.Sprintf("source %s: %v", s.ID, err), nil)
					}
					if snap.Repo != "" {
						stamp.Repo = snap.Repo
					}
					stamp.Branch, stamp.Commit, stamp.CommitDate, stamp.State = snap.Branch, snap.Commit, snap.CommitDate, snap.State
				}
			}
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
		var eg *blocks.EndpointGrouping
		if o := s.GroupByEndpoint; o != nil {
			eg = &blocks.EndpointGrouping{SkipAreas: o.SkipAreas, SkipSegments: o.SkipSegments, PathPrefixes: o.PathPrefixes}
		}
		var walks []blocks.Walk
		if s.Flows != "" {
			data, err := os.ReadFile(ws.ResolvePath(s.Flows))
			if err != nil {
				return io.fail("FLOWS_READ", fmt.Sprintf("source %s: %v", s.ID, err), nil)
			}
			if walks, err = blocks.ParseWalks(data); err != nil {
				return io.fail("FLOWS_INVALID", fmt.Sprintf("source %s: %s: %v", s.ID, s.Flows, err), nil)
			}
		}
		in.Sources = append(in.Sources, blocks.SourceInput{
			Info: blocks.SourceInfo{ID: s.ID, Prefix: s.Prefix, Tier: s.Tier, System: s.System, Commit: stamp.Commit, Endpoints: eg,
				RoutePrefixes: s.RoutePrefixes},
			Catalogues: cats, Stamp: stamp, Walks: walks})
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

	old, err := st.Graph(ctx)
	if err != nil {
		return io.fail("GRAPH_READ", err.Error(), nil)
	}
	diff := blocks.DiffGraphs(
		schema.KnowledgeGraph{Blocks: old.Blocks, BlockEdges: old.Edges, Sources: old.Sources, Flows: old.Flows},
		schema.KnowledgeGraph{Blocks: res.Blocks, BlockEdges: res.Edges, Sources: res.Sources, Flows: res.Flows})
	if err := st.ReplaceGraph(ctx, store.Graph{Blocks: res.Blocks, Edges: res.Edges, Sources: res.Sources, Flows: res.Flows}); err != nil {
		return io.fail("GRAPH_WRITE", err.Error(), nil)
	}

	report.Blocks, report.Sources = len(res.Blocks), res.Sources
	report.Summary, report.Diff, report.Flows = diff.Summary(), diff, len(res.Flows)
	report.Edges = map[string]int{}
	for _, e := range res.Edges {
		report.Edges[e.Kind]++
	}
	report.Warnings = append(report.Warnings, res.Warnings...)
	if io.JSON {
		return io.printJSON(report)
	}
	io.printf("%s\n", report.Summary)
	io.printf("wrote %s: %d blocks, %d edges\n", st.Where(), len(res.Blocks), len(res.Edges))
	kinds := make([]string, 0, len(report.Edges))
	for k := range report.Edges {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		io.printf("  %-14s %d\n", k, report.Edges[k])
	}
	io.printf("  flows          %d\n", len(res.Flows))
	for _, s := range res.Sources {
		c := s.Commit
		if len(c) > 7 {
			c = c[:7]
		}
		io.printf("  source %s @ %s %s %s\n", s.ID, c, s.CommitDate, s.State)
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

// readRelease reads a source's release/v1 file. No release configured, or a
// missing file, is no release (a warning for the missing file); a bad file is an error.
func readRelease(ws *workspace.Workspace, s workspace.Source, report *refreshReport) (*schema.Release, error) {
	if s.Release == "" {
		return nil, nil
	}
	data, err := os.ReadFile(ws.ResolvePath(s.Release))
	if os.IsNotExist(err) {
		report.Warnings = append(report.Warnings, fmt.Sprintf("source %s: release file %s not found; freshness is no_release", s.ID, s.Release))
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r, err := blocks.ParseRelease(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.Release, err)
	}
	return &r, nil
}
