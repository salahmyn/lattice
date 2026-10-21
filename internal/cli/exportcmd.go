package cli

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/ui"
)

func newExportCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{Use: "export", Short: "Write views of the workspace as files"}
	cmd.AddCommand(&cobra.Command{
		Use:   "map <out.html>",
		Short: "Write the block map as one self-contained HTML file (the stored graph inlined)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			st, err := openStore(io)
			if err != nil {
				return err
			}
			defer st.Close()
			g, err := st.Graph(context.Background())
			if err != nil {
				return io.fail("GRAPH_READ", err.Error(), nil)
			}
			page, err := ui.MapHTML(g)
			if err != nil {
				return io.fail("EXPORT_FAILED", err.Error(), nil)
			}
			if err := os.WriteFile(args[0], page, 0o644); err != nil {
				return io.fail("WRITE", err.Error(), nil)
			}
			io.printf("wrote %s: %d blocks, %d edges, %d flows\n", args[0], len(g.Blocks), len(g.Edges), len(g.Flows))
			return nil
		},
	})
	return cmd
}
