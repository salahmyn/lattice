package cli

import (
	"sort"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// newActorCommand exposes the v0.6 actor touchpoint view: every EP an
// actor can fire and every feature/BRD they exercise. Reads
// lattice/context.yaml; resolves actor.uses against the feature graph.
//
// The CLI shape mirrors /api/v1/actors and /api/v1/actors/{id} so MCP
// agents call the same answer that the UI shows.
func newActorCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "actor",
		Short: "Inspect actors declared in lattice/context.yaml and their touchpoints",
	}
	cmd.AddCommand(newActorListCommand(io), newActorShowCommand(io))
	return cmd
}

func newActorListCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every declared actor with EP/feature counts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			ctx, err := schema.LoadContext(ws.ContextPath())
			if err != nil {
				return io.fail("CONTEXT_LOAD_FAILED", err.Error(), nil)
			}
			kg, _, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			type row struct {
				ID           string `json:"id"`
				Name         string `json:"name"`
				FeatureCount int    `json:"feature_count"`
				EPCount      int    `json:"ep_count"`
			}
			rows := make([]row, 0, len(ctx.Actors))
			for _, a := range ctx.Actors {
				touched := resolveActorFeatures(a, kg)
				rows = append(rows, row{
					ID: a.ID, Name: a.Name,
					FeatureCount: len(touched),
					EPCount:      len(epsForFeatures(touched, kg)),
				})
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
			if io.JSON {
				return io.printJSON(rows)
			}
			if len(rows) == 0 {
				io.printf("no actors declared — add some to lattice/context.yaml\n")
				return nil
			}
			for _, r := range rows {
				io.printf("%-24s %-32s features=%-3d  EPs=%d\n",
					r.ID, r.Name, r.FeatureCount, r.EPCount)
			}
			return nil
		},
	}
}

func newActorShowCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show one actor's full touchpoint set",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			ctx, err := schema.LoadContext(ws.ContextPath())
			if err != nil {
				return io.fail("CONTEXT_LOAD_FAILED", err.Error(), nil)
			}
			var actor *schema.Actor
			for i := range ctx.Actors {
				if ctx.Actors[i].ID == args[0] {
					actor = &ctx.Actors[i]
					break
				}
			}
			if actor == nil {
				return io.fail("ACTOR_NOT_FOUND", "no actor with id "+args[0], nil)
			}
			kg, _, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			features := resolveActorFeatures(*actor, kg)
			eps := epsForFeatures(features, kg)
			brds := brdsForFeatures(features, kg)

			payload := map[string]interface{}{
				"actor":        actor,
				"features":     featureList(features),
				"entry_points": eps,
				"brds":         brds,
			}
			if io.JSON {
				return io.printJSON(payload)
			}
			io.printf("%s — %s\n", actor.ID, actor.Name)
			if actor.Description != "" {
				io.printf("\n%s\n", actor.Description)
			}
			io.printf("\nFeatures (%d):\n", len(features))
			for _, f := range featureList(features) {
				io.printf("  - %s\n", f)
			}
			io.printf("\nEntry points (%d):\n", len(eps))
			for _, ep := range eps {
				io.printf("  - %-12s %s\n", ep.Kind, ep.ID)
			}
			io.printf("\nBRDs touched (%d):\n", len(brds))
			for _, b := range brds {
				io.printf("  - %s\n", b)
			}
			return nil
		},
	}
}

// --- helpers (same resolution rules the UI uses) -------------------

func resolveActorFeatures(a schema.Actor, kg schema.KnowledgeGraph) map[string]bool {
	featureIDs := map[string]bool{}
	for _, f := range kg.Features {
		featureIDs[f.ID] = true
	}
	touched := map[string]bool{}
	for _, u := range a.Uses {
		if featureIDs[u] {
			touched[u] = true
		}
	}
	for _, u := range a.Uses {
		if featureIDs[u] {
			continue
		}
		for fid := range featureIDs {
			if fid == u || len(fid) > len(u)+1 && fid[:len(u)+1] == u+"." {
				touched[fid] = true
			}
		}
	}
	return touched
}

func epsForFeatures(features map[string]bool, kg schema.KnowledgeGraph) []schema.EntryPoint {
	var out []schema.EntryPoint
	for _, ep := range kg.EntryPoints {
		for _, step := range ep.Flow {
			if features[step.Feature] {
				out = append(out, ep)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func brdsForFeatures(features map[string]bool, kg schema.KnowledgeGraph) []string {
	seen := map[string]bool{}
	for _, f := range kg.Features {
		if features[f.ID] && f.ImplementsBRD != "" {
			seen[f.ImplementsBRD] = true
		}
	}
	for _, b := range kg.BRDs {
		for _, fid := range b.ImplementsVia {
			if features[fid] {
				seen[b.ID] = true
				break
			}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func featureList(features map[string]bool) []string {
	out := make([]string, 0, len(features))
	for f := range features {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}
