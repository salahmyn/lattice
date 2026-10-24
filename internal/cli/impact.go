package cli

// @module-feature impact

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/all"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/impact"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/scip"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

func newImpactCommand(io *IO) *cobra.Command {
	var since, format string
	var maxSymbols int
	var includeDrafts bool
	cmd := &cobra.Command{
		Use:   "impact --since <ref>",
		Short: "Read-only change-impact report for a pull request",
		Long: `Compares merge-base(<ref>, HEAD) with the working tree (HEAD plus
uncommitted changes) and reports, without writing anything:

  1. changed code      symbols whose own lines a hunk touches, on either side
  2. affected criteria BRD criteria whose invariant has a changed enforcer,
                       approved BRDs first, draft/proposed ones apart
  3. removed enforcement  an enforcer that no longer enforces its invariant:
                       deleted, @enforces tag dropped, file deleted or moved
                       out of the scanned tree
  4. manifest changes  features, BRDs and entry points added, removed,
                       renamed or changed, field by field
  5. code roll-up      the SCIP blast radius of each changed enforcer, rolled
                       up to modules, test files and entry points (optional)

Unlike backprop it records nothing in the ledger and raises no flag, so it
is safe to run on every pull request. --format markdown is the PR-comment
form; it starts with ` + impact.MarkdownMarker + ` so a job can update its
own comment.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if io.JSON {
				format = "json"
			}
			switch format {
			case "text", "markdown", "md", "json":
			default:
				return io.fail("BAD_FLAG", fmt.Sprintf("--format %q: want text, markdown or json", format), nil)
			}
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			kg, err := readOnlyGraph(cmd, ws)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			adCfg, err := config.LoadAdapters(ws.LatticeDir)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			in := impact.Input{
				Workspace: ws, Registry: all.Registry(adCfg), Graph: kg,
				Since: since, MaxSymbols: maxSymbols, IncludeDrafts: includeDrafts,
			}
			if corpus, cerr := scip.Load(scipIndexPaths(ws)...); cerr != nil {
				in.CorpusError = cerr.Error()
			} else {
				in.Corpus = corpus
			}
			rep, err := impact.Compute(cmd.Context(), in)
			if err != nil {
				return io.fail("GIT_FAILED", err.Error(), nil)
			}
			switch format {
			case "json":
				return io.printJSON(rep)
			case "markdown", "md":
				impact.Markdown(io.Out, rep)
			default:
				impact.Text(io.Out, rep)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "git ref to compare with; the base is merge-base(<ref>, HEAD) (required)")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text, markdown or json (--json implies json)")
	cmd.Flags().IntVar(&maxSymbols, "max-symbols", impact.DefaultMaxSymbols, "cap on enforcer symbols the code roll-up expands")
	cmd.Flags().BoolVar(&includeDrafts, "include-drafts", true, "list criteria of draft and proposed BRDs in their own section")
	_ = cmd.MarkFlagRequired("since")
	return cmd
}

// readOnlyGraph builds the working tree's graph the way read commands do,
// but with every write switched off: no LLM entry-point labeling (whose
// labels are saved to lattice/entry-points/), no SCIP indexer run, no
// lattice.json, no ledger entry and no flag.
// @enforces impact:INV-1
func readOnlyGraph(cmd *cobra.Command, ws *workspace.Workspace) (schema.KnowledgeGraph, error) {
	return buildGraphWith(cmd.Context(), ws, buildOptions{readOnly: true})
}
