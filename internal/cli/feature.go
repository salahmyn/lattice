package cli

import (
	"fmt"
	"strings"

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
			kg, _, err := graphFor(io, cmd, false)
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
				rows = append(rows, row{m.ID, m.Status, m.Version, schema.InlineText(m.Purpose)})
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
			kg, _, err := graphFor(io, cmd, false)
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
				io.printf("%s  [%s]  v%d\n%s\n\n", m.ID, m.Status, m.Version, strings.TrimSpace(m.Purpose))
				for _, c := range m.Capabilities {
					io.printf("  capability  %s — %s\n", c.ID, c.Summary)
				}
				for _, inv := range m.Invariants {
					io.printf("  invariant   %s — %s\n", inv.ID, inv.Statement)
				}
				for _, s := range kg.Surfaces {
					if s.Feature == m.ID {
						io.printf("  surface     %-28s [%s]\n", surfaceLabelCLI(s), surfaceStatusCLI(s))
					}
				}
				for _, e := range kg.Errors {
					if e.Feature == m.ID {
						io.printf("  error       %-28s [%s]\n", errorLabelCLI(e), errorStatusCLI(e))
					}
				}
				for _, child := range m.Children {
					io.printf("  sub-feature %s\n", child)
				}
				for _, impl := range m.Implementations {
					io.printf("  impl        %s (%s)\n", impl.Symbol, impl.Language)
				}
				return nil
			}
			na := &schema.NextAction{Kind: "create_manifest", Ref: args[0]}
			return io.fail("FEATURE_NOT_FOUND", "feature not found: "+args[0], na)
		},
	}
}

// surfaceLabelCLI renders a surface as "METHOD /path" or "type name".
func surfaceLabelCLI(s schema.GraphSurface) string {
	if s.Path != "" {
		return strings.TrimSpace(s.Method + " " + s.Path)
	}
	return strings.TrimSpace(s.Type + " " + s.Name)
}

// surfaceStatusCLI summarizes whether a surface is declared and implemented.
func surfaceStatusCLI(s schema.GraphSurface) string {
	switch {
	case s.Declared && s.Implemented:
		return "ok"
	case s.Declared:
		return "declared, not implemented"
	default:
		return "implemented, undeclared"
	}
}

// errorLabelCLI renders an error as "code (status)" or just "code".
func errorLabelCLI(e schema.GraphError) string {
	if e.Status != 0 {
		return fmt.Sprintf("%s (%d)", e.Code, e.Status)
	}
	return e.Code
}

// errorStatusCLI summarizes whether an error is declared and raised.
func errorStatusCLI(e schema.GraphError) string {
	switch {
	case e.Declared && e.Implemented:
		return "ok"
	case e.Declared:
		return "declared, not raised"
	default:
		return "raised, undeclared"
	}
}
