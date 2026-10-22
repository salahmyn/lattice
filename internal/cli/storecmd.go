package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/buildinfo"
	"github.com/salahmyn/lattice/pkg/lattice/graph"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/store"
)

func newStoreCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{Use: "store", Short: "Query and export the block graph in the workspace store"}

	var f store.Filter
	search := &cobra.Command{Use: "search <text>", Short: "Blocks whose id, title or description words start with every term",
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			st, err := openStore(io)
			if err != nil {
				return err
			}
			defer st.Close()
			bs, err := st.Search(context.Background(), strings.Join(args, " "), f)
			if err != nil {
				return io.fail("STORE_READ", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(bs)
			}
			for _, b := range bs {
				io.printf("%s\t%s\t%s\n", b.ID, b.Kind, b.Title)
			}
			return nil
		}}
	search.Flags().StringVar(&f.Kind, "kind", "", "only blocks of this kind")
	search.Flags().StringVar(&f.System, "system", "", "only blocks of this system")
	search.Flags().StringVar(&f.Tier, "tier", "", "only blocks of this tier")
	search.Flags().IntVar(&f.Limit, "limit", 50, "at most this many blocks")

	var dir string
	var kinds []string
	var depth int
	relations := &cobra.Command{Use: "relations <block id>", Short: "Edges around a block, up to a depth",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			d := store.Direction(dir)
			if d != store.Out && d != store.In && d != store.Both {
				return io.fail("BAD_DIRECTION", "direction must be out, in or both", nil)
			}
			st, err := openStore(io)
			if err != nil {
				return err
			}
			defer st.Close()
			es, err := st.Relations(context.Background(), args[0], d, kinds, depth)
			if err != nil {
				return io.fail("STORE_READ", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(es)
			}
			for _, e := range es {
				io.printf("%s -%s-> %s\n", e.From, e.Kind, e.To)
			}
			return nil
		}}
	relations.Flags().StringVar(&dir, "direction", "both", "out, in or both")
	relations.Flags().StringSliceVar(&kinds, "kinds", nil, "only these edge kinds (comma-separated)")
	relations.Flags().IntVar(&depth, "depth", 1, "hops, 1 to 10")

	export := &cobra.Command{Use: "export <out.json>", Short: "Write the stored block graph as a lattice.json-shaped file",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			st, err := openStore(io)
			if err != nil {
				return err
			}
			defer st.Close()
			g, err := st.Graph(context.Background())
			if err != nil {
				return io.fail("STORE_READ", err.Error(), nil)
			}
			kg := schema.KnowledgeGraph{SchemaVersion: buildinfo.SchemaVersion, Blocks: g.Blocks, BlockEdges: g.Edges, Sources: g.Sources, Flows: g.Flows}
			if err := graph.Write(args[0], kg); err != nil {
				return io.fail("WRITE", err.Error(), nil)
			}
			io.printf("wrote %s: %d blocks, %d edges, %d flows\n", args[0], len(g.Blocks), len(g.Edges), len(g.Flows))
			return nil
		}}

	cmd.AddCommand(search, relations, export)
	return cmd
}
