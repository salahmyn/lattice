package cli

// @module-feature cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/blocks"
	"github.com/salahmyn/lattice/pkg/lattice/store"
	"github.com/salahmyn/lattice/pkg/lattice/ui"
)

func newExportCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{Use: "export", Short: "Write views of the workspace as files"}
	var title string
	mapCmd := &cobra.Command{
		Use:   "map <out.html>",
		Short: "Write the block map as one self-contained HTML file (the stored graph and its assurance lens inlined)",
		Long: `Write the block map as one self-contained HTML file: the stored block graph,
and beside it the assurance lens, the audit (lattice audit) of the promises
each block holds. The audit reads the ingested results, coverage and break
runs; missing evidence is never drawn as a pass, and when the audit cannot
run, or has no row for a promise block, that promise reads unknown.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore(io)
			if err != nil {
				return err
			}
			defer st.Close()
			g, err := st.Graph(context.Background())
			if err != nil {
				return io.fail("GRAPH_READ", err.Error(), nil)
			}
			lens := mapAssurance(cmd, io, g)
			page, err := ui.MapPage(ui.MapData{Graph: g, Assurance: lens}, title)
			if err != nil {
				return io.fail("EXPORT_FAILED", err.Error(), nil)
			}
			if err := os.WriteFile(args[0], page, 0o644); err != nil {
				return io.fail("WRITE", err.Error(), nil)
			}
			io.printf("wrote %s: %d blocks, %d edges, %d flows\n", args[0], len(g.Blocks), len(g.Edges), len(g.Flows))
			if !lens.Available {
				io.printf("  assurance: unknown for every promise (%s)\n", lens.Note)
			} else {
				at := ""
				if lens.Commit != "" {
					at = " at " + shortCommit(lens.Commit)
				}
				io.printf("  assurance: %d promises audited%s\n", len(lens.Promises), at)
			}
			return nil
		},
	}
	mapCmd.Flags().StringVar(&title, "title", "", "page title (default \"Block map\")")

	var vtitle string
	viewsCmd := &cobra.Command{
		Use:   "views <out.html>",
		Short: "Write the views page (features, authoring, actors, services) as one self-contained HTML file",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			st, err := openStore(io)
			if err != nil {
				return err
			}
			defer st.Close()
			d, err := ui.Business(context.Background(), st)
			if err != nil {
				return io.fail("GRAPH_READ", err.Error(), nil)
			}
			page, err := ui.ViewsHTML(d, vtitle)
			if err != nil {
				return io.fail("EXPORT_FAILED", err.Error(), nil)
			}
			if err := os.WriteFile(args[0], page, 0o644); err != nil {
				return io.fail("WRITE", err.Error(), nil)
			}
			io.printf("wrote %s: %d features, %d shared blocks\n", args[0], len(d.View.Features), len(d.View.Shared))
			return nil
		},
	}
	viewsCmd.Flags().StringVar(&vtitle, "title", "", "page title (default \"Block views\")")

	featuresCmd := &cobra.Command{
		Use:   "features <dir>",
		Short: "Write each BRD record as a Gherkin .feature file (named after the last part of its id)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			st, err := openStore(io)
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			g, err := st.Graph(ctx)
			if err != nil {
				return io.fail("GRAPH_READ", err.Error(), nil)
			}
			recs, err := st.List(ctx, "brd")
			if err != nil {
				return io.fail("STORE_READ", err.Error(), nil)
			}
			if err := os.MkdirAll(args[0], 0o755); err != nil {
				return io.fail("WRITE", err.Error(), nil)
			}
			written := map[string]string{} // file name -> BRD id
			for _, r := range recs {
				b, err := blocks.ParseBRD(r.Body, "brd "+r.ID)
				if err != nil {
					return io.fail("BRD_INVALID", err.Error(), nil)
				}
				name := b.ID[strings.LastIndex(b.ID, ".")+1:]
				if name == "" || strings.ContainsAny(name, "/\\") {
					return io.fail("BRD_INVALID", "brd "+b.ID+": cannot name a file after this id", nil)
				}
				if other := written[name]; other != "" {
					return io.fail("BRD_INVALID", fmt.Sprintf("brd %s and %s would both write %s.feature; rename one", other, b.ID, name), nil)
				}
				written[name] = b.ID
				p := filepath.Join(args[0], name+".feature")
				if err := os.WriteFile(p, []byte(blocks.Gherkin(b, g.Blocks)), 0o644); err != nil {
					return io.fail("WRITE", err.Error(), nil)
				}
				io.printf("wrote %s\n", p)
			}
			return nil
		},
	}
	cmd.AddCommand(mapCmd, viewsCmd, featuresCmd)
	return cmd
}

// mapAssurance audits the workspace for an exported map, read-only. When the
// knowledge graph cannot be built, the lens says why and every promise on the
// map reads unknown; the export still succeeds.
func mapAssurance(cmd *cobra.Command, io *IO, g store.Graph) ui.AssuranceLens {
	ws, err := openWorkspace(io)
	if err != nil {
		return ui.Assurance(g, nil, err.Error())
	}
	kg, err := readOnlyGraph(cmd, ws)
	if err != nil {
		return ui.Assurance(g, nil, "the audit could not build the knowledge graph: "+err.Error())
	}
	dirty, _ := gitDirty(filepath.Dir(ws.LatticeDir), ws.LatticeDir)
	rep := audit.Compute(audit.Collect(ws.LatticeDir, kg, dirty))
	return ui.Assurance(g, &rep, "")
}
