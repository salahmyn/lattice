package cli

import (
	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func newFeatureCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "feature",
		Short: "Inspect features",
	}
	cmd.AddCommand(newFeatureListCommand(io), newFeatureShowCommand(io))
	return cmd
}

func newFeatureListCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List features",
		RunE: func(cmd *cobra.Command, _ []string) error {
			kg, err := buildGraph(cmd.Context(), io.Repo, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			type row struct {
				ID      string        `json:"id"`
				Status  schema.Status `json:"status"`
				Version int           `json:"version"`
				Purpose string        `json:"purpose"`
			}
			var rows []row
			for _, m := range kg.Features {
				rows = append(rows, row{m.ID, m.Status, m.Version, m.Purpose})
			}
			if io.JSON {
				return io.printJSON(rows)
			}
			for _, r := range rows {
				io.printf("%-28s %-11s v%d  %s\n", r.ID, r.Status, r.Version, r.Purpose)
			}
			return nil
		},
	}
}

func newFeatureShowCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show one feature with hydrated edges",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kg, err := buildGraph(cmd.Context(), io.Repo, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			for _, m := range kg.Features {
				if m.ID != args[0] {
					continue
				}
				if io.JSON {
					return io.printJSON(m)
				}
				io.printf("%s  [%s]  v%d\n%s\n\n", m.ID, m.Status, m.Version, m.Purpose)
				for _, c := range m.Capabilities {
					io.printf("  capability %s — %s\n", c.ID, c.Summary)
				}
				for _, inv := range m.Invariants {
					io.printf("  invariant  %s — %s\n", inv.ID, inv.Statement)
				}
				for _, impl := range m.Implementations {
					io.printf("  impl       %s (%s)\n", impl.Symbol, impl.Language)
				}
				return nil
			}
			na := &schema.NextAction{Kind: "create_manifest", Ref: args[0]}
			return io.fail("FEATURE_NOT_FOUND", "feature not found: "+args[0], na)
		},
	}
}
