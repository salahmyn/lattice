package cli

// @module-feature cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/graph"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func newFeatureCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "feature",
		Short: "Inspect features",
	}
	cmd.AddCommand(newFeatureListCommand(io), newFeatureShowCommand(io), newFeatureSpecCommand(io))
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
			kg, ws, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			for _, m := range kg.Features {
				if m.ID != args[0] {
					continue
				}
				layers := graph.FeatureLayers(kg)[m.ID]
				if io.JSON {
					// the manifest as before, plus the computed layers
					return io.printJSON(struct {
						schema.Manifest
						Layers []string `json:"layers,omitempty"`
					}{m, layers})
				}
				io.printf("%s  [%s]  v%d\n%s\n\n", m.ID, m.Status, m.Version, strings.TrimSpace(m.Purpose))
				if len(layers) > 0 {
					io.printf("  layers      %s\n", strings.Join(layers, ", "))
				}
				for _, c := range m.Capabilities {
					io.printf("  capability  %s — %s\n", c.ID, c.Summary)
				}
				cases := caseCoverage(ws.LatticeDir, kg, m.ID)
				for _, inv := range m.Invariants {
					io.printf("  invariant   %s — %s\n", inv.ID, inv.Statement)
					for _, c := range cases[m.ID+":"+inv.ID] {
						io.printf("    case      %s\n", c.Line(true))
						if l := c.ConfirmationLine(); l != "" {
							io.printf("              confirmation %s\n", l)
						}
					}
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
				layerOf := map[string]string{}
				for _, sym := range kg.Symbols {
					layerOf[sym.FQN] = sym.Layer
				}
				for _, impl := range m.Implementations {
					layer := ""
					if l := layerOf[impl.Symbol]; l != "" {
						layer = " [" + l + "]"
					}
					io.printf("  impl        %s (%s)%s\n", impl.Symbol, impl.Language, layer)
				}
				return nil
			}
			na := &schema.NextAction{Kind: "create_manifest", Ref: args[0]}
			return io.fail("FEATURE_NOT_FOUND", "feature not found: "+args[0], na)
		},
	}
}

// caseCoverage returns the audit's case rows by promise for the features
// named (every feature when none is), or nil when none of them declares a
// case, so a workspace without cases pays nothing for the audit.
func caseCoverage(latticeDir string, kg schema.KnowledgeGraph, features ...string) map[string][]audit.CaseRow {
	want := map[string]bool{}
	for _, f := range features {
		want[f] = true
	}
	has := false
	for _, f := range kg.Features {
		for _, inv := range f.Invariants {
			has = has || (len(inv.Cases) > 0 && (len(want) == 0 || want[f.ID]))
		}
	}
	if !has {
		return nil
	}
	dirty, _ := gitDirty(filepath.Dir(latticeDir))
	out := map[string][]audit.CaseRow{}
	for _, r := range audit.Compute(audit.Collect(latticeDir, kg, dirty)).Rows {
		if len(r.Cases) > 0 {
			out[r.Promise] = r.Cases
		}
	}
	return out
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
