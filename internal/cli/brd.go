package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/agentic"
	"github.com/salahmyn/lattice/pkg/lattice/brd"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// newBRDCommand assembles the `lattice brd` subtree — the v0.5.0 entry
// point to the Business Requirements Document axis. Mirrors the shape
// of `lattice initiative` so users coming from the work-in-flight axis
// have a familiar command grammar.
func newBRDCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "brd",
		Short: "Manage Business Requirements Documents (the layer above features)",
	}
	cmd.AddCommand(
		newBRDListCommand(io),
		newBRDShowCommand(io),
		newBRDNewCommand(io),
		newBRDLinkCommand(io),
		newBRDApproveCommand(io),
		newBRDFromCodeCommand(io),
	)
	return cmd
}

func newBRDListCommand(io *IO) *cobra.Command {
	var statusFilter string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List Business Requirements Documents",
		RunE: func(cmd *cobra.Command, _ []string) error {
			kg, _, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			out := kg.BRDs
			if statusFilter != "" {
				filtered := out[:0]
				for _, b := range out {
					if string(b.Status) == statusFilter {
						filtered = append(filtered, b)
					}
				}
				out = filtered
			}
			if io.JSON {
				return io.printJSON(out)
			}
			if len(out) == 0 {
				io.printf("no BRDs (run `lattice brd new brd.<slug>` or `lattice brd from-code <feature-id>`)\n")
				return nil
			}
			featuresByBRD := brd.FeaturesByBRD(kg.BRDs, kg.Features)
			for _, b := range out {
				features := featuresByBRD[b.ID]
				io.printf("%-32s %-10s v%-3d  %d feature(s)  %s\n",
					b.ID, b.Status, b.Version, len(features), b.Title)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&statusFilter, "status", "", "filter by status (draft|proposed|approved|superseded)")
	return cmd
}

func newBRDShowCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show a BRD and its linked features",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kg, _, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			b := brd.Find(kg.BRDs, args[0])
			if b == nil {
				return io.fail("BRD_NOT_FOUND", "BRD not found: "+args[0], nil)
			}
			featuresByBRD := brd.FeaturesByBRD(kg.BRDs, kg.Features)
			if io.JSON {
				return io.printJSON(map[string]interface{}{
					"brd":      b,
					"features": featuresByBRD[b.ID],
				})
			}
			io.printf("%s  [%s v%d]\n%s\n\n", b.ID, b.Status, b.Version, b.Title)
			if b.BusinessProblem != "" {
				io.printf("Problem:\n  %s\n\n", schema.InlineText(b.BusinessProblem))
			}
			if len(b.BusinessGoals) > 0 {
				io.printf("Goals:\n")
				for _, g := range b.BusinessGoals {
					io.printf("  - %s\n", g)
				}
				io.printf("\n")
			}
			if len(b.SuccessCriteria) > 0 {
				io.printf("Success criteria:\n")
				for _, sc := range b.SuccessCriteria {
					io.printf("  %s — %s", sc.ID, sc.Statement)
					if sc.MapsToInvariant != "" {
						io.printf(" (maps to %s)", sc.MapsToInvariant)
					}
					io.printf("\n")
				}
				io.printf("\n")
			}
			fs := featuresByBRD[b.ID]
			io.printf("Implementing features (%d):\n", len(fs))
			for _, fid := range fs {
				io.printf("  - %s\n", fid)
			}
			if b.Provenance.Source == schema.BRDSourceLLMFromCode {
				io.printf("\nProvenance: LLM-regenerated %s — needs human review\n",
					b.Provenance.GeneratedAt)
			}
			if b.Approval != nil {
				io.printf("\nApproved by %s on %s (v%d)\n",
					b.Approval.ApprovedBy, b.Approval.ApprovedAt, b.Approval.ApprovedVersion)
			}
			return nil
		},
	}
}

func newBRDNewCommand(io *IO) *cobra.Command {
	var title string
	cmd := &cobra.Command{
		Use:   "new <id>",
		Short: "Scaffold a draft BRD on disk",
		Long: `Scaffold a new Business Requirements Document at lattice/brds/<id>.yaml.

The id must start with 'brd.' so it never collides with the feature-id
namespace. Use 'lattice brd from-code <feature-id>' for the brownfield
regeneration path instead.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			id := args[0]
			if !strings.HasPrefix(id, "brd.") {
				return io.fail("BRD_ID_FORMAT", "BRD id must start with `brd.` (got: "+id+")", nil)
			}
			b := schema.BRD{
				ID:              id,
				Version:         1,
				Status:          schema.BRDDraft,
				Title:           orPlaceholder(title, "TODO: short, business-readable title"),
				BusinessProblem: "TODO: describe the customer or business problem this BRD addresses.",
				BusinessGoals: []string{
					"TODO: a measurable outcome the business cares about",
				},
				SuccessCriteria: []schema.BRDCriterion{
					{ID: "SC-1", Statement: "TODO: how we know it worked"},
				},
				OutOfScope: []string{"TODO: what this BRD deliberately does not cover"},
				Provenance: schema.BRDProvenance{Source: schema.BRDSourceHuman},
			}
			path, err := brd.Save(ws.BRDsDir(), b)
			if err != nil {
				return io.fail("BRD_SAVE_FAILED", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(map[string]string{"created": path})
			}
			io.printf("created %s\n", path)
			io.printf("\nNext: edit the file, then `lattice brd link %s <feature-id>` to attach features.\n", id)
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "human-readable title to seed the draft with")
	return cmd
}

func newBRDLinkCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "link <brd-id> <feature-id>",
		Short: "Link a BRD to a feature (writes back to both files)",
		Long: `Records the BRD ↔ Feature relationship on both sides of the link:
adds the feature id to brd.implements_via and sets feature.implements_brd.

The double-write is intentional — either field is enough to drive the
validator and the UI, but having both on disk keeps a single-file grep
discoverable in either direction.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			brdID, featureID := args[0], args[1]

			kg, _, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			b := brd.Find(kg.BRDs, brdID)
			if b == nil {
				return io.fail("BRD_NOT_FOUND", "BRD not found: "+brdID, nil)
			}

			// Find the feature & confirm it exists. We need the source
			// path to write back to.
			var feat *schema.Manifest
			for i := range kg.Features {
				if kg.Features[i].ID == featureID {
					feat = &kg.Features[i]
					break
				}
			}
			if feat == nil {
				return io.fail("FEATURE_NOT_FOUND", "feature not found: "+featureID, nil)
			}

			// Forward link.
			if !containsString(b.ImplementsVia, featureID) {
				b.ImplementsVia = append(b.ImplementsVia, featureID)
				sort.Strings(b.ImplementsVia)
				if _, err := brd.SaveForce(ws.BRDsDir(), *b); err != nil {
					return io.fail("BRD_SAVE_FAILED", err.Error(), nil)
				}
			}

			// Reverse link — load the feature manifest fresh so we
			// preserve fields the graph builder normally strips.
			featPath := joinLattice(ws.LatticeDir, feat.SourcePath)
			loaded, lerr := schema.LoadManifest(featPath)
			if lerr != nil {
				return io.fail("FEATURE_LOAD_FAILED", lerr.Error(), nil)
			}
			if loaded.ImplementsBRD != brdID {
				loaded.ImplementsBRD = brdID
				if err := schema.SaveCanonical(featPath, *loaded); err != nil {
					return io.fail("FEATURE_SAVE_FAILED", err.Error(), nil)
				}
			}

			if io.JSON {
				return io.printJSON(map[string]string{
					"brd": brdID, "feature": featureID, "status": "linked",
				})
			}
			io.printf("linked %s ↔ %s\n", brdID, featureID)
			return nil
		},
	}
}

func newBRDApproveCommand(io *IO) *cobra.Command {
	var approver string
	cmd := &cobra.Command{
		Use:   "approve <id>",
		Short: "Approve a BRD at its current version",
		Long: `Records human sign-off in the BRD's approval block and flips
status to 'approved'. The approval pins the version the approver
signed; a subsequent edit that bumps version raises BRD_DRIFT until
re-approved.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			if strings.TrimSpace(approver) == "" {
				return io.fail("APPROVER_REQUIRED", "--by <email> is required", nil)
			}
			kg, _, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			b := brd.Find(kg.BRDs, args[0])
			if b == nil {
				return io.fail("BRD_NOT_FOUND", "BRD not found: "+args[0], nil)
			}
			b.Status = schema.BRDApproved
			b.Approval = &schema.BRDApproval{
				ApprovedBy:      approver,
				ApprovedAt:      time.Now().UTC().Format(time.RFC3339),
				ApprovedVersion: b.Version,
			}
			path, err := brd.SaveForce(ws.BRDsDir(), *b)
			if err != nil {
				return io.fail("BRD_SAVE_FAILED", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(map[string]string{"approved": path})
			}
			io.printf("approved %s by %s (v%d)\n", b.ID, approver, b.Version)
			return nil
		},
	}
	cmd.Flags().StringVar(&approver, "by", "", "email of the approver (required)")
	return cmd
}

func newBRDFromCodeCommand(io *IO) *cobra.Command {
	var allUnbrided, force, dryRun bool
	cmd := &cobra.Command{
		Use:   "from-code [feature-id]",
		Short: "Regenerate a draft BRD from a feature's manifest + entry points (LLM)",
		Long: `Generate a draft BRD for one feature, or every un-BRD'd feature, by
asking the configured LLM to reverse-engineer business intent from the
existing manifest and the entry points that reach the feature.

The generated BRD is always:
  - status: draft
  - provenance.source: llm_from_code
  - human_review_required: true
  - constraints: []  (the model is forbidden from inventing them)

A human must edit and run 'lattice brd approve' before downstream
features can rely on the BRD. The BRD_UNAPPROVED_LLM validation rule
keeps this honest.

Refuses to overwrite an existing BRD unless --force is set.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if allUnbrided {
				if len(args) > 0 {
					return fmt.Errorf("--all-unbrided takes no positional argument")
				}
				return nil
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			kg, _, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			cfg, _ := config.Load(ws.LatticeDir)
			provider := agentic.FromConfig(cfg.Agentic.LLM)
			if !agentic.Enabled(provider) {
				return io.fail("NO_LLM",
					"no LLM provider configured — `brd from-code` needs one (configure lattice/config.yaml `agentic.llm`)", nil)
			}

			// Pick the feature set to regenerate against.
			var targets []schema.Manifest
			if allUnbrided {
				existing := map[string]bool{}
				for _, b := range kg.BRDs {
					for _, fid := range b.ImplementsVia {
						existing[fid] = true
					}
				}
				for _, f := range kg.Features {
					if f.ImplementsBRD != "" || existing[f.ID] {
						continue
					}
					targets = append(targets, f)
				}
			} else {
				for _, f := range kg.Features {
					if f.ID == args[0] {
						targets = append(targets, f)
						break
					}
				}
				if len(targets) == 0 {
					return io.fail("FEATURE_NOT_FOUND", "feature not found: "+args[0], nil)
				}
			}
			if len(targets) == 0 {
				if io.JSON {
					return io.printJSON(map[string]interface{}{"regenerated": 0, "skipped": 0})
				}
				io.printf("nothing to do — every feature already has a BRD\n")
				return nil
			}

			opts := brd.FromCodeOptions{
				Provider:     agenticBRDAdapter{p: provider},
				SystemPrompt: agentic.ToneContract(cfg.Agentic.Tone) + "\n\n" + brd.FromCodePrompt,
				MaxTokens:    cfg.Agentic.LLM.MaxTokens,
				Model:        cfg.Agentic.LLM.Model,
			}

			type outcome struct {
				Feature string `json:"feature"`
				BRD     string `json:"brd,omitempty"`
				Path    string `json:"path,omitempty"`
				Skipped bool   `json:"skipped,omitempty"`
				Reason  string `json:"reason,omitempty"`
			}
			var results []outcome
			regenerated, skipped := 0, 0
			for _, f := range targets {
				brdID := "brd." + f.ID
				if !force {
					if existing := brd.Find(kg.BRDs, brdID); existing != nil {
						results = append(results, outcome{
							Feature: f.ID, BRD: brdID, Skipped: true,
							Reason: "BRD already exists (use --force to overwrite)",
						})
						skipped++
						if !io.JSON {
							io.printf("skip %s (BRD already exists; pass --force to overwrite)\n", f.ID)
						}
						continue
					}
				}
				reached := reachedBy(f.ID, kg.EntryPoints)
				b, _, ferr := brd.FromCode(cmd.Context(), f, reached, opts)
				if ferr != nil {
					results = append(results, outcome{
						Feature: f.ID, Skipped: true, Reason: ferr.Error(),
					})
					skipped++
					if !io.JSON {
						io.errorf("fail %s: %s\n", f.ID, ferr.Error())
					}
					continue
				}
				if dryRun {
					results = append(results, outcome{Feature: f.ID, BRD: b.ID})
					regenerated++
					if !io.JSON {
						io.printf("[dry-run] would write brds/%s.yaml (%d goal(s), %d scenario(s), %d criterion(a))\n",
							b.ID, len(b.BusinessGoals), len(b.UserScenarios), len(b.SuccessCriteria))
					}
					continue
				}
				path, serr := brd.SaveForce(ws.BRDsDir(), b)
				if serr != nil {
					results = append(results, outcome{
						Feature: f.ID, Skipped: true, Reason: serr.Error(),
					})
					skipped++
					continue
				}
				results = append(results, outcome{Feature: f.ID, BRD: b.ID, Path: path})
				regenerated++
				if !io.JSON {
					io.printf("regenerated %s -> %s (status: draft, review required)\n", f.ID, path)
				}
			}

			if io.JSON {
				return io.printJSON(map[string]interface{}{
					"regenerated": regenerated,
					"skipped":     skipped,
					"results":     results,
				})
			}
			io.printf("\nSummary: %d BRD(s) regenerated, %d skipped\n", regenerated, skipped)
			if regenerated > 0 {
				io.printf("Review the drafts; run `lattice brd approve <id> --by <email>` once each is signed off.\n")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&allUnbrided, "all-unbrided", false, "regenerate for every feature without an existing BRD")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing BRD")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be generated, without writing files")
	return cmd
}

// agenticBRDAdapter bridges an agentic.Provider into the brd package's
// minimal LLMProvider interface. The brd package can't import agentic
// (that would cycle through extract); the CLI sits above both, so the
// adapter lives here.
type agenticBRDAdapter struct{ p agentic.Provider }

func (a agenticBRDAdapter) Complete(ctx context.Context, req brd.LLMRequest) (brd.LLMResponse, error) {
	resp, err := a.p.Complete(ctx, agentic.CompletionRequest{
		SystemPrompt: req.SystemPrompt,
		UserMessage:  req.UserMessage,
		MaxTokens:    req.MaxTokens,
		Temperature:  req.Temperature,
	})
	return brd.LLMResponse{Text: resp.Text, TokensUsed: resp.TokensUsed}, err
}

// reachedBy returns every entry point whose flow visits the given feature.
// Matches the helper of the same name in pages.go for the UI feature page,
// but local-scoped so the CLI doesn't import the UI package.
func reachedBy(featureID string, eps []schema.EntryPoint) []schema.EntryPoint {
	var out []schema.EntryPoint
	for _, ep := range eps {
		for _, step := range ep.Flow {
			if step.Feature == featureID {
				out = append(out, ep)
				break
			}
		}
	}
	return out
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func orPlaceholder(s, placeholder string) string {
	if strings.TrimSpace(s) == "" {
		return placeholder
	}
	return s
}

// joinLattice resolves a lattice-relative source path (as recorded by
// the extractor) to an absolute path. The extractor stores
// `features/...yaml` relative to LatticeDir, never with a leading slash.
func joinLattice(latticeDir, relSourcePath string) string {
	return fmt.Sprintf("%s/%s", strings.TrimRight(latticeDir, "/"), relSourcePath)
}
