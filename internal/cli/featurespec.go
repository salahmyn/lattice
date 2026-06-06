package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/featurespec"
)

// newFeatureSpecCommand adds `lattice feature spec <id>` — the v0.7
// AMA `.ai-spec.md` emitter. Produces the deterministic
// markdown form by default; --out writes to a file (typically the
// feature folder so it co-locates with the code an agent will edit).
//
// Wired by feature.go's newFeatureCommand alongside `list` / `show`
// so the discoverability is `lattice feature --help` → `spec` shows up.
func newFeatureSpecCommand(io *IO) *cobra.Command {
	var outPath string
	cmd := &cobra.Command{
		Use:   "spec <id>",
		Short: "Emit the AMA-shaped .ai-spec.md for one feature",
		Long: `Renders a feature's manifest as the AMA spec §3 ai-spec.md format —
a ≤500-word markdown summary an AI agent can load alongside the
feature's folder. Output is fully deterministic; the same manifest
produces byte-identical output.

When --out is set, the spec is written to that path. Otherwise the
spec prints to stdout.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kg, _, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			var found bool
			var spec string
			for _, f := range kg.Features {
				if f.ID == args[0] {
					spec = featurespec.Render(f)
					found = true
					break
				}
			}
			if !found {
				return io.fail("FEATURE_NOT_FOUND", "feature not found: "+args[0], nil)
			}

			words := featurespec.WordCount(spec)

			if outPath != "" {
				if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
					return io.fail("WRITE_FAILED", err.Error(), nil)
				}
				if err := os.WriteFile(outPath, []byte(spec), 0o644); err != nil {
					return io.fail("WRITE_FAILED", err.Error(), nil)
				}
				if io.JSON {
					return io.printJSON(map[string]interface{}{
						"feature":   args[0],
						"path":      outPath,
						"word_count": words,
						"over_cap":  words > featurespec.WordCap,
					})
				}
				io.printf("wrote %s (%d words)\n", outPath, words)
				if words > featurespec.WordCap {
					io.printf("  warning: %d > %d-word AMA cap — consider decomposition\n",
						words, featurespec.WordCap)
				}
				return nil
			}

			if io.JSON {
				return io.printJSON(map[string]interface{}{
					"feature":    args[0],
					"spec":       spec,
					"word_count": words,
					"over_cap":   words > featurespec.WordCap,
				})
			}
			// Print the spec verbatim — operators pipe it into less /
			// editor / clip without an extra wrapper line.
			fmt.Fprint(io.Out, spec)
			return nil
		},
	}
	cmd.Flags().StringVar(&outPath, "out", "", "write spec to this path (defaults to stdout)")
	return cmd
}
