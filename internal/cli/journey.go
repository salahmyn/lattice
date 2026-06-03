package cli

import (
	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/views"
)

// newJourneyCommand exposes the v0.6 journey aggregation: every EP
// that touches any feature in the named BRD's `implements_via`. The
// JSON shape is identical to /api/v1/journeys/{id} so MCP agents and
// scripts get the same picture as the UI.
//
// The command always emits structured output — there's no useful
// "human-readable" form for a journey beyond the rendered mermaid
// graph, which is embedded in the JSON.
func newJourneyCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "journey <brd-id>",
		Short: "Show every entry point that touches a BRD's features",
		Long: `Aggregates every entry point whose flow visits a feature implementing
the named BRD. Emits a JSON object with:
  - features:    the BRD's in-scope feature set
  - entry_points: every EP that joins the journey
  - mermaid:     rendered flowchart (same as the UI's /journeys page)

This is the answer to "show me the X flow" — one call, no reconstruction
required.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kg, _, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			j := views.BuildJourney(kg, args[0])
			if j == nil {
				return io.fail("BRD_NOT_FOUND", "no BRD with id "+args[0], nil)
			}
			// JSON is the only useful shape here; force it regardless of
			// the --json flag so callers (MCP tools, scripts) get the
			// structured output reliably.
			return io.printJSON(j)
		},
	}
}
