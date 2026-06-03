package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/adapters"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/all"
	"github.com/salahmyn/lattice/pkg/lattice/agentic"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/extract"
	"github.com/salahmyn/lattice/pkg/lattice/importer"
	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
	"github.com/salahmyn/lattice/pkg/lattice/validate"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

func newImportCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Adopt an existing codebase: discover feature candidates",
	}
	cmd.AddCommand(
		newImportScanCommand(io),
		newImportDraftCommand(io),
		newImportReviewCommand(io),
		newImportVerifyCommand(io),
		newImportInscribeCommand(io),
		newImportUninscribeCommand(io),
		newImportPromoteParentsCommand(io),
		newImportResetCommand(io),
		newImportUndoCommand(io),
		newImportStatusCommand(io),
	)
	return cmd
}

type importStatus struct {
	Status                string  `json:"status"`
	Scopes                []string `json:"scopes,omitempty"`
	Candidates            int     `json:"candidates"`
	Accepted              int     `json:"accepted"`
	Rejected              int     `json:"rejected"`
	DiscoveryCoverage     float64 `json:"discovery_coverage"`
	DocumentationCoverage float64 `json:"documentation_coverage"`
}

func newImportScanCommand(io *IO) *cobra.Command {
	var scopes []string
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Stage 1 — discover feature candidates by static analysis (no LLM)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			if ws.Review {
				return io.fail("NO_CODE_ACCESS",
					"import scan needs source access, but no code root is available", nil)
			}

			modules, err := parseModules(cmd.Context(), ws)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}

			cfg, _ := config.Load(ws.LatticeDir)
			cf := importer.Discover(modules, importer.Options{
				Scopes:              scopes,
				MinCandidateSymbols: cfg.Import.MinCandidateSymbols,
				Exclude:             cfg.Import.Coverage.Exclude,
			})

			if err := os.MkdirAll(ws.ImportDir(), 0o755); err != nil {
				return io.fail("IMPORT_WRITE_FAILED", err.Error(), nil)
			}
			candPath := filepath.Join(ws.ImportDir(), importer.CandidatesFileName)
			if err := importer.Write(candPath, cf); err != nil {
				return io.fail("IMPORT_WRITE_FAILED", err.Error(), nil)
			}
			sessPath := filepath.Join(ws.ImportDir(), importer.SessionFileName)
			prior, _ := importer.LoadSession(sessPath)
			if err := importer.SaveSession(sessPath, importer.NewScannedSession(prior, cf)); err != nil {
				return io.fail("IMPORT_WRITE_FAILED", err.Error(), nil)
			}

			if io.JSON {
				return io.printJSON(cf)
			}
			d := cf.Coverage.Discovery
			io.printf("Discovered %d feature candidates -> %s\n", len(cf.Candidates), candPath)
			if len(scopes) > 0 {
				io.printf("  scopes:   %s\n", strings.Join(scopes, ", "))
			}
			io.printf("  coverage: %.1f%% (%d/%d production symbols clustered)\n",
				d.Ratio*100, d.ClusteredSymbols, d.TotalSymbols)
			for _, c := range cf.Candidates {
				io.printf("  - %s  %-32s  conf %.1f  %d symbols\n",
					c.ID, c.Package, c.Confidence, len(c.Symbols))
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&scopes, "scope", nil,
		"restrict discovery to one or more code-root-relative subtrees (repeatable, or comma-separated)")
	return cmd
}

func newImportStatusCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show import session progress and discovery coverage",
		RunE: func(_ *cobra.Command, _ []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			sess, err := importer.LoadSession(filepath.Join(ws.ImportDir(), importer.SessionFileName))
			if err != nil {
				return io.fail("NO_IMPORT_SESSION",
					"no import session; run `lattice import scan` first", nil)
			}
			cf, _ := importer.LoadCandidates(filepath.Join(ws.ImportDir(), importer.CandidatesFileName))
			doc := importer.ComputeDocumentation(cf, sess.Decisions)
			st := importStatus{
				Status:                sess.Status,
				Scopes:                sess.Scopes,
				Candidates:            sess.Candidates,
				Accepted:              countDecisions(sess.Decisions, importer.DecisionAccepted),
				Rejected:              countDecisions(sess.Decisions, importer.DecisionRejected),
				DiscoveryCoverage:     cf.Coverage.Discovery.Ratio,
				DocumentationCoverage: doc.Ratio,
			}
			if io.JSON {
				return io.printJSON(st)
			}
			io.printf("Import session: %s\n", st.Status)
			if len(st.Scopes) > 0 {
				io.printf("  scopes:                 %s\n", strings.Join(st.Scopes, ", "))
			}
			io.printf("  candidates:             %d\n", st.Candidates)
			io.printf("  accepted / rejected:    %d / %d\n", st.Accepted, st.Rejected)
			io.printf("  discovery coverage:     %.1f%%\n", st.DiscoveryCoverage*100)
			io.printf("  documentation coverage: %.1f%%\n", st.DocumentationCoverage*100)
			return nil
		},
	}
}

type coverageReport struct {
	Discovery     importer.DiscoveryCoverage     `json:"discovery"`
	Documentation importer.DocumentationCoverage `json:"documentation"`
	Verification  importer.VerificationCoverage  `json:"verification"`
	BRD           importer.BRDCoverage           `json:"brd"`
	RTM           rtm.Coverage                   `json:"rtm"`
}

func newCoverageCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "coverage",
		Short: "Report adoption coverage: how much of the code the import describes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			cf, err := importer.LoadCandidates(filepath.Join(ws.ImportDir(), importer.CandidatesFileName))
			if err != nil {
				return io.fail("NO_IMPORT_SESSION",
					"no candidates; run `lattice import scan` first", nil)
			}
			sess, _ := importer.LoadSession(filepath.Join(ws.ImportDir(), importer.SessionFileName))
			report := coverageReport{
				Discovery:     cf.Coverage.Discovery,
				Documentation: importer.ComputeDocumentation(cf, sess.Decisions),
			}
			// Verification + BRD + RTM coverage need the graph and the validation engine.
			if kg, gerr := buildGraph(cmd.Context(), ws, false); gerr == nil {
				cfg, _ := config.Load(ws.LatticeDir)
				viol := validate.Validate(kg, cfg, validate.Options{ReviewMode: ws.Review})
				report.Verification = importer.ComputeVerification(kg.Features, viol)
				report.BRD = importer.ComputeBRD(kg.Features, kg.BRDs)
				report.RTM = rtm.ComputeCoverage(rtm.Build(kg, rtm.Options{
					MutationThreshold: cfg.MutationTesting.Thresholds.Default,
				}))
			}
			if io.JSON {
				return io.printJSON(report)
			}
			d, doc, ver, brdc, rtmc := report.Discovery, report.Documentation, report.Verification, report.BRD, report.RTM
			io.printf("Discovery coverage:     %.1f%%  (%d/%d production symbols clustered into candidates)\n",
				d.Ratio*100, d.ClusteredSymbols, d.TotalSymbols)
			io.printf("Documentation coverage: %.1f%%  (%d/%d symbols attached to an accepted feature)\n",
				doc.Ratio*100, doc.DocumentedSymbols, doc.TotalSymbols)
			io.printf("Verification coverage:  %.1f%%  (%d/%d invariants enforced and verified)\n",
				ver.Ratio*100, ver.VerifiedInvariants, ver.TotalInvariants)
			io.printf("BRD coverage:           %.1f%%  (%d/%d features with an approved upstream BRD; %d/%d BRDs approved)\n",
				brdc.Ratio*100, brdc.CoveredFeatures, brdc.TotalFeatures, brdc.ApprovedBRDs, brdc.TotalBRDs)
			io.printf("BRD goal coverage:      %.1f%%  (%d/%d success criteria traced to a verified invariant)\n",
				rtmc.Ratio*100, rtmc.VerifiedCriteria, rtmc.TotalCriteria)
			io.printf("\nDiscovery by package:\n")
			for _, p := range d.ByPackage {
				io.printf("  %5.1f%%  %4d/%-4d  %s\n",
					p.Ratio*100, p.ClusteredSymbols, p.TotalSymbols, p.Package)
			}
			return nil
		},
	}
}

// countDecisions counts session decisions equal to want.
// newImportResetCommand clears review state — decisions + drafts — without
// re-running the (expensive) scan. By default it leaves accepted feature
// manifests in place because humans may have edited them; --also-features
// removes those too for a clean-slate redo.
func newImportResetCommand(io *IO) *cobra.Command {
	var alsoFeatures bool
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Clear draft manifests and review decisions (keeps the scan)",
		RunE: func(_ *cobra.Command, _ []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			sessPath := filepath.Join(ws.ImportDir(), importer.SessionFileName)
			sess, _ := importer.LoadSession(sessPath)
			cleared := len(sess.Decisions)
			sess.Decisions = nil
			if sess.Status == importer.StatusReviewing || sess.Status == importer.StatusDrafted {
				sess.Status = importer.StatusScanned
			}
			if err := importer.SaveSession(sessPath, sess); err != nil {
				return io.fail("IMPORT_WRITE_FAILED", err.Error(), nil)
			}
			draftsRemoved := 0
			draftsDir := filepath.Join(ws.ImportDir(), importer.DraftsDirName)
			if entries, derr := os.ReadDir(draftsDir); derr == nil {
				for _, e := range entries {
					if !e.IsDir() {
						if err := os.Remove(filepath.Join(draftsDir, e.Name())); err == nil {
							draftsRemoved++
						}
					}
				}
			}
			featuresRemoved := 0
			if alsoFeatures {
				_ = os.RemoveAll(ws.FeaturesDir())
				_ = os.MkdirAll(ws.FeaturesDir(), 0o755)
				featuresRemoved = -1 // signal "all"
			}
			if io.JSON {
				return io.printJSON(map[string]interface{}{
					"cleared_decisions": cleared,
					"removed_drafts":    draftsRemoved,
					"removed_features":  featuresRemoved == -1,
				})
			}
			io.printf("Reset import session: cleared %d decision(s), removed %d draft(s)\n",
				cleared, draftsRemoved)
			if alsoFeatures {
				io.printf("  --also-features: wiped %s\n", ws.FeaturesDir())
			}
			io.printf("Candidates kept. Next: `lattice import draft`.\n")
			return nil
		},
	}
	cmd.Flags().BoolVar(&alsoFeatures, "also-features", false,
		"also delete every manifest under features/ (destructive — accepts can have been hand-edited)")
	return cmd
}

// newImportUndoCommand reverts one per-candidate decision. If the
// decision was an accept, the generated manifest file is removed. The
// candidate stays in candidates.json so it can be re-reviewed.
func newImportUndoCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "undo <candidate-id>",
		Short: "Revert one review decision (deletes the manifest if it was an accept)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			candID := args[0]
			sessPath := filepath.Join(ws.ImportDir(), importer.SessionFileName)
			sess, _ := importer.LoadSession(sessPath)
			prev, ok := sess.Decisions[candID]
			if !ok {
				return io.fail("NO_DECISION", "no recorded decision for "+candID, nil)
			}
			removed := ""
			if prev == importer.DecisionAccepted {
				draftPath := filepath.Join(ws.ImportDir(), importer.DraftsDirName, candID+".yaml")
				if m, derr := schema.LoadManifest(draftPath); derr == nil {
					mp := filepath.Join(ws.FeaturesDir(),
						filepath.FromSlash(strings.ReplaceAll(m.ID, ".", "/"))+".yaml")
					if rerr := os.Remove(mp); rerr == nil {
						removed = mp
					}
				}
			}
			delete(sess.Decisions, candID)
			if err := importer.SaveSession(sessPath, sess); err != nil {
				return io.fail("IMPORT_WRITE_FAILED", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(map[string]interface{}{
					"candidate":        candID,
					"previous_decision": prev,
					"removed_manifest":  removed,
				})
			}
			io.printf("Undid %s decision for %s\n", prev, candID)
			if removed != "" {
				io.printf("  - removed manifest %s\n", removed)
			}
			io.printf("Re-review with: lattice import review %s\n", candID)
			return nil
		},
	}
}

// newImportPromoteParentsCommand backfills missing umbrella manifests for
// every dotted feature id under features/. Most accepts already auto-promote
// during review; this command is the bulk fix-up for repos imported before
// auto-promote existed, or after hand-editing.
func newImportPromoteParentsCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "promote-parents",
		Short: "Create umbrella manifests for every missing ancestor of a dotted feature id",
		RunE: func(_ *cobra.Command, _ []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			created, err := importer.PromoteParents(ws.FeaturesDir())
			if err != nil {
				return io.fail("IMPORT_WRITE_FAILED", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(map[string]interface{}{
					"created": created, "count": len(created),
				})
			}
			if len(created) == 0 {
				io.printf("No missing parents — every dotted feature id already has its ancestors.\n")
				return nil
			}
			io.printf("Promoted %d umbrella manifest(s) under %s\n", len(created), ws.FeaturesDir())
			for _, fid := range created {
				io.printf("  + %s\n", fid)
			}
			io.printf("\nVerify the result: lattice import verify\n")
			return nil
		},
	}
}

// providerLabel describes the labeling provider for the summary line.
func providerLabel(active bool, provider agentic.Provider) string {
	if active {
		return "llm (" + provider.Name() + ")"
	}
	return "deterministic"
}

// formatModeCounts renders the honest per-mode breakdown — replaces the old
// single-mode line that hid silent fallbacks behind a misleading "[llm]" tag.
func formatModeCounts(counts map[importer.Mode]int, provider string) string {
	total := 0
	for _, n := range counts {
		total += n
	}
	parts := []string{}
	add := func(m importer.Mode, label string) {
		if n := counts[m]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, label))
		}
	}
	add(importer.ModeLLM, "LLM")
	add(importer.ModeCached, "cached")
	add(importer.ModeFallback, "fallback")
	add(importer.ModeDeterministic, "deterministic")
	if len(parts) == 0 {
		return fmt.Sprintf("provider: %s", provider)
	}
	return fmt.Sprintf("provider: %s   outcomes: %s", provider, strings.Join(parts, ", "))
}

func countDecisions(decisions map[string]string, want string) int {
	n := 0
	for _, d := range decisions {
		if d == want {
			n++
		}
	}
	return n
}

func newImportDraftCommand(io *IO) *cobra.Command {
	var noLLM bool
	var scopes []string
	var audienceOverride string
	cmd := &cobra.Command{
		Use:   "draft",
		Short: "Stage 2 — draft a manifest for each candidate (grounded LLM, deterministic fallback)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			cf, err := importer.LoadCandidates(filepath.Join(ws.ImportDir(), importer.CandidatesFileName))
			if err != nil {
				return io.fail("NO_IMPORT_SESSION",
					"no candidates; run `lattice import scan` first", nil)
			}
			// Late scope filter — replaces the post-scan Python hand-filter
			// I needed during dogfooding to target a handful of modules.
			cf = importer.FilterByScopes(cf, scopes)
			if len(scopes) > 0 && len(cf.Candidates) == 0 {
				return io.fail("EMPTY_SCOPE",
					"no candidates match --scope "+strings.Join(scopes, ", "), nil)
			}

			cfg, _ := config.Load(ws.LatticeDir)
			provider := agentic.FromConfig(cfg.Agentic.LLM)
			var drafts []importer.Draft
			llmActive := !noLLM && agentic.Enabled(provider)
			if llmActive {
				// Per-candidate progress: emit a line to stderr as each
				// candidate finishes labeling, so a long LLM run is never
				// silent and silent fallbacks are immediately visible.
				// Skipped in JSON mode to keep stderr clean for tooling.
				var progress func(done, total int, candID string, mode importer.Mode)
				if !io.JSON {
					progress = func(done, total int, candID string, mode importer.Mode) {
						io.errorf("  [%3d/%d] %s  %s\n", done, total, candID, mode)
					}
				}
				// Compose the tone contract from config, with a CLI
				// --audience override for a one-shot voice change.
				tone := cfg.Agentic.Tone
				if audienceOverride != "" {
					tone.Audience = audienceOverride
				}
				sysPrompt := agentic.ToneContract(tone) + importer.LabelSystemPromptDefault()
				drafts = importer.LabelWithLLM(cmd.Context(), cf, provider, importer.LLMLabelOptions{
					MaxTokens:    cfg.Agentic.LLM.MaxTokens,
					CacheDir:     ws.CacheDir(),
					Progress:     progress,
					SystemPrompt: sysPrompt,
				})
			} else {
				drafts = importer.Label(cf)
			}

			draftsDir := filepath.Join(ws.ImportDir(), importer.DraftsDirName)
			if err := os.MkdirAll(draftsDir, 0o755); err != nil {
				return io.fail("IMPORT_WRITE_FAILED", err.Error(), nil)
			}
			for _, d := range drafts {
				p := filepath.Join(draftsDir, d.CandidateID+".yaml")
				if err := schema.SaveCanonical(p, d.Manifest); err != nil {
					return io.fail("IMPORT_WRITE_FAILED", err.Error(), nil)
				}
			}
			sessPath := filepath.Join(ws.ImportDir(), importer.SessionFileName)
			if sess, serr := importer.LoadSession(sessPath); serr == nil && sess.Status == importer.StatusScanned {
				sess.Status = importer.StatusDrafted
				_ = importer.SaveSession(sessPath, sess)
			}

			// Honest mode breakdown. Replaces the misleading single
			// "[llm (openai)]" label that hid every silent fallback.
			counts := map[importer.Mode]int{}
			for _, d := range drafts {
				counts[d.Mode]++
			}
			if io.JSON {
				return io.printJSON(map[string]interface{}{
					"drafts":      drafts,
					"mode_counts": counts,
					"provider":    providerLabel(llmActive, provider),
				})
			}
			io.printf("Drafted %d manifest(s) -> %s\n", len(drafts), draftsDir)
			io.printf("  %s\n", formatModeCounts(counts, providerLabel(llmActive, provider)))
			for _, d := range drafts {
				io.printf("  %s  [%s]  ->  %s\n", d.CandidateID, d.Mode, d.Manifest.ID)
			}
			io.printf("\nReview them: lattice import review <candidate-id>\n")
			return nil
		},
	}
	cmd.Flags().BoolVar(&noLLM, "no-llm", false, "force the deterministic labeler even when an LLM is configured")
	cmd.Flags().StringSliceVar(&scopes, "scope", nil,
		"only draft candidates whose files lie under any of these subtrees (repeatable)")
	cmd.Flags().StringVar(&audienceOverride, "audience", "",
		"override agentic.tone.audience for this run (business|product|engineering|mixed or free-form)")
	return cmd
}

func newImportReviewCommand(io *IO) *cobra.Command {
	var accept, reject, acceptAll, rejectAll bool
	var scopes, wheres []string
	var fromFile string
	cmd := &cobra.Command{
		Use:   "review [candidate-id]",
		Short: "Stage 3 — review a candidate's bundle and accept or reject it",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			cf, err := importer.LoadCandidates(filepath.Join(ws.ImportDir(), importer.CandidatesFileName))
			if err != nil {
				return io.fail("NO_IMPORT_SESSION",
					"no candidates; run `lattice import scan` first", nil)
			}
			cfFiltered := importer.FilterByScopes(cf, scopes)
			sessPath := filepath.Join(ws.ImportDir(), importer.SessionFileName)
			sess, _ := importer.LoadSession(sessPath)

			// Bulk paths: --from-file and --accept-all/--reject-all are
			// exclusive with the single-candidate flow.
			if fromFile != "" {
				return reviewFromFile(io, ws, cf, sess, sessPath, fromFile)
			}
			if acceptAll || rejectAll {
				if acceptAll && rejectAll {
					return io.fail("BAD_FLAGS", "choose --accept-all or --reject-all, not both", nil)
				}
				action := importer.DecisionAccepted
				if rejectAll {
					action = importer.DecisionRejected
				}
				return reviewBulk(io, ws, cfFiltered, sess, sessPath, wheres, action)
			}

			if len(args) == 0 {
				if accept || reject {
					return io.fail("NO_CANDIDATE", "--accept/--reject needs a candidate id", nil)
				}
				return reviewList(io, cfFiltered, sess)
			}
			if accept && reject {
				return io.fail("BAD_FLAGS", "choose --accept or --reject, not both", nil)
			}
			cand, ok := findCandidate(cf, args[0])
			if !ok {
				return io.fail("UNKNOWN_CANDIDATE", "no candidate with id "+args[0], nil)
			}
			switch {
			case accept:
				return reviewDecide(io, ws, sess, sessPath, cand, importer.DecisionAccepted)
			case reject:
				return reviewDecide(io, ws, sess, sessPath, cand, importer.DecisionRejected)
			default:
				return reviewShow(io, ws, cand, sess)
			}
		},
	}
	cmd.Flags().BoolVar(&accept, "accept", false, "accept the candidate: write its manifest under features/")
	cmd.Flags().BoolVar(&reject, "reject", false, "reject the candidate")
	cmd.Flags().BoolVar(&acceptAll, "accept-all", false,
		"bulk-accept every candidate matching --scope and --where filters")
	cmd.Flags().BoolVar(&rejectAll, "reject-all", false,
		"bulk-reject every candidate matching --scope and --where filters")
	cmd.Flags().StringVar(&fromFile, "from-file", "",
		"apply a decisions.yaml batch (atomic; PR-friendly)")
	cmd.Flags().StringSliceVar(&scopes, "scope", nil,
		"restrict to candidates under any of these subtrees (repeatable)")
	cmd.Flags().StringSliceVar(&wheres, "where", nil,
		"predicate filter for bulk ops; e.g. package=modules/X, confidence>=0.7, symbols<5 (repeatable, AND-ed)")
	return cmd
}

// reviewDecide records an accept/reject decision; an accept also writes the
// candidate's draft into features/ as a real proposal manifest.
func reviewDecide(io *IO, ws *workspace.Workspace, sess importer.Session, sessPath string,
	cand importer.Candidate, decision string) error {

	manifestPath := ""
	if decision == importer.DecisionAccepted {
		draftPath := filepath.Join(ws.ImportDir(), importer.DraftsDirName, cand.ID+".yaml")
		m, derr := schema.LoadManifest(draftPath)
		if derr != nil {
			return io.fail("NO_DRAFT",
				"no draft for "+cand.ID+"; run `lattice import draft` first", nil)
		}
		manifestPath = filepath.Join(ws.FeaturesDir(),
			filepath.FromSlash(strings.ReplaceAll(m.ID, ".", "/"))+".yaml")
		if exists(manifestPath) {
			return io.fail("ALREADY_EXISTS", "feature manifest already exists: "+manifestPath, nil)
		}
		if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
			return io.fail("IMPORT_WRITE_FAILED", err.Error(), nil)
		}
		if err := schema.SaveCanonical(manifestPath, m); err != nil {
			return io.fail("IMPORT_WRITE_FAILED", err.Error(), nil)
		}
	}

	if sess.Decisions == nil {
		sess.Decisions = map[string]string{}
	}
	sess.Decisions[cand.ID] = decision
	sess.Status = importer.StatusReviewing
	if err := importer.SaveSession(sessPath, sess); err != nil {
		return io.fail("IMPORT_WRITE_FAILED", err.Error(), nil)
	}

	// Auto-promote ancestors: a dotted id like accounts.api.wrappers.subscription
	// needs accounts.api.wrappers / accounts.api / accounts to exist or verify
	// cascades with SUBFEATURE_PARENT_MISSING. PromoteParents is idempotent
	// and only writes manifests that don't already exist.
	var promoted []string
	if decision == importer.DecisionAccepted {
		var perr error
		promoted, perr = importer.PromoteParents(ws.FeaturesDir())
		if perr != nil {
			return io.fail("IMPORT_WRITE_FAILED", perr.Error(), nil)
		}
	}

	if io.JSON {
		out := map[string]interface{}{"candidate": cand.ID, "decision": decision}
		if manifestPath != "" {
			out["manifest"] = manifestPath
		}
		if len(promoted) > 0 {
			out["promoted_parents"] = promoted
		}
		return io.printJSON(out)
	}
	if manifestPath != "" {
		io.printf("accepted %s -> %s\n", cand.ID, manifestPath)
		for _, p := range promoted {
			io.printf("  + parent  %s  (auto-created umbrella manifest)\n", p)
		}
		io.printf("verify the generated substrate: lattice import verify\n")
	} else {
		io.printf("rejected %s\n", cand.ID)
	}
	return nil
}

// reviewFromFile applies an entire decisions.yaml batch atomically. It does
// the per-candidate work in one pass and runs PromoteParents once at the
// end — for a 50-candidate batch that's one filesystem walk instead of 50.
func reviewFromFile(io *IO, ws *workspace.Workspace, cf importer.CandidatesFile,
	sess importer.Session, sessPath, path string) error {

	decisions, err := importer.LoadDecisionsFile(path)
	if err != nil {
		return io.fail("BAD_DECISIONS_FILE", err.Error(), nil)
	}
	return applyBatch(io, ws, cf, sess, sessPath, decisions, "from-file: "+path)
}

// reviewBulk applies --accept-all / --reject-all with optional --where
// predicates. The scope filter is already applied to cf by the caller.
func reviewBulk(io *IO, ws *workspace.Workspace, cf importer.CandidatesFile,
	sess importer.Session, sessPath string, wheres []string, action string) error {

	preds := []importer.CandidatePredicate{}
	for _, w := range wheres {
		p, err := importer.ParseWhere(w)
		if err != nil {
			return io.fail("BAD_WHERE", err.Error(), nil)
		}
		preds = append(preds, p)
	}
	match := importer.AndPredicates(preds)
	decisions := map[string]string{}
	for _, c := range cf.Candidates {
		if match(c) {
			decisions[c.ID] = action
		}
	}
	if len(decisions) == 0 {
		return io.fail("EMPTY_BULK", "no candidates match the given filters", nil)
	}
	return applyBatch(io, ws, cf, sess, sessPath, decisions, fmt.Sprintf("bulk: %d matched", len(decisions)))
}

// applyBatch is the shared driver: write manifests for accepts, record
// every decision in the session, run PromoteParents once at the end.
func applyBatch(io *IO, ws *workspace.Workspace, cf importer.CandidatesFile,
	sess importer.Session, sessPath string, decisions map[string]string, source string) error {

	if sess.Decisions == nil {
		sess.Decisions = map[string]string{}
	}
	candByID := map[string]importer.Candidate{}
	for _, c := range cf.Candidates {
		candByID[c.ID] = c
	}
	type result struct {
		Candidate string `json:"candidate"`
		Decision  string `json:"decision"`
		Manifest  string `json:"manifest,omitempty"`
		Skipped   string `json:"skipped,omitempty"`
	}
	results := make([]result, 0, len(decisions))
	wroteAccept := false

	for candID, decision := range decisions {
		cand, ok := candByID[candID]
		if !ok {
			results = append(results, result{Candidate: candID, Decision: decision,
				Skipped: "unknown candidate (not in current scan)"})
			continue
		}
		r := result{Candidate: candID, Decision: decision}
		if decision == importer.DecisionAccepted {
			draftPath := filepath.Join(ws.ImportDir(), importer.DraftsDirName, cand.ID+".yaml")
			m, derr := schema.LoadManifest(draftPath)
			if derr != nil {
				r.Skipped = "no draft (run `lattice import draft` first)"
				results = append(results, r)
				continue
			}
			mp := filepath.Join(ws.FeaturesDir(),
				filepath.FromSlash(strings.ReplaceAll(m.ID, ".", "/"))+".yaml")
			if exists(mp) {
				r.Skipped = "manifest already exists: " + mp
				results = append(results, r)
				continue
			}
			if err := os.MkdirAll(filepath.Dir(mp), 0o755); err != nil {
				return io.fail("IMPORT_WRITE_FAILED", err.Error(), nil)
			}
			if err := schema.SaveCanonical(mp, m); err != nil {
				return io.fail("IMPORT_WRITE_FAILED", err.Error(), nil)
			}
			r.Manifest = mp
			wroteAccept = true
		}
		sess.Decisions[candID] = decision
		results = append(results, r)
	}
	sess.Status = importer.StatusReviewing
	if err := importer.SaveSession(sessPath, sess); err != nil {
		return io.fail("IMPORT_WRITE_FAILED", err.Error(), nil)
	}

	// One PromoteParents pass at the end is far cheaper than one per
	// accept when the batch is large.
	var promoted []string
	if wroteAccept {
		var perr error
		promoted, perr = importer.PromoteParents(ws.FeaturesDir())
		if perr != nil {
			return io.fail("IMPORT_WRITE_FAILED", perr.Error(), nil)
		}
	}

	if io.JSON {
		return io.printJSON(map[string]interface{}{
			"source":           source,
			"results":          results,
			"promoted_parents": promoted,
		})
	}
	accepted, rejected, skipped := 0, 0, 0
	for _, r := range results {
		switch {
		case r.Skipped != "":
			skipped++
		case r.Decision == importer.DecisionAccepted:
			accepted++
		case r.Decision == importer.DecisionRejected:
			rejected++
		}
	}
	io.printf("Batch review applied (%s)\n", source)
	io.printf("  accepted: %d   rejected: %d   skipped: %d\n", accepted, rejected, skipped)
	if len(promoted) > 0 {
		io.printf("  auto-created %d umbrella parent manifest(s)\n", len(promoted))
	}
	for _, r := range results {
		if r.Skipped != "" {
			io.printf("  ~ %s  [%s]  skipped: %s\n", r.Candidate, r.Decision, r.Skipped)
		}
	}
	io.printf("\nVerify the result: lattice import verify\n")
	return nil
}

func reviewList(io *IO, cf importer.CandidatesFile, sess importer.Session) error {
	type row struct {
		Candidate  string  `json:"candidate"`
		Package    string  `json:"package"`
		Confidence float64 `json:"confidence"`
		Symbols    int     `json:"symbols"`
		Decision   string  `json:"decision"`
	}
	rows := make([]row, 0, len(cf.Candidates))
	for _, c := range cf.Candidates {
		d := sess.Decisions[c.ID]
		if d == "" {
			d = "pending"
		}
		rows = append(rows, row{c.ID, c.Package, c.Confidence, len(c.Symbols), d})
	}
	if io.JSON {
		return io.printJSON(rows)
	}
	io.printf("Candidates (%d):\n", len(rows))
	for _, r := range rows {
		io.printf("  %-16s %-28s conf %.1f  %2d sym  [%s]\n",
			r.Candidate, r.Package, r.Confidence, r.Symbols, r.Decision)
	}
	return nil
}

func reviewShow(io *IO, ws *workspace.Workspace, cand importer.Candidate, sess importer.Session) error {
	draftPath := filepath.Join(ws.ImportDir(), importer.DraftsDirName, cand.ID+".yaml")
	draft, derr := schema.LoadManifest(draftPath)

	if io.JSON {
		bundle := struct {
			Candidate importer.Candidate `json:"candidate"`
			Draft     *schema.Manifest   `json:"draft_manifest,omitempty"`
			Decision  string             `json:"decision,omitempty"`
		}{Candidate: cand, Decision: sess.Decisions[cand.ID]}
		if derr == nil {
			bundle.Draft = draft
		}
		return io.printJSON(bundle)
	}

	io.printf("Candidate %s  (%s)\n", cand.ID, cand.Package)
	io.printf("  language:   %s\n", cand.Language)
	io.printf("  confidence: %.1f\n", cand.Confidence)
	io.printf("  symbols:\n")
	for _, s := range cand.Symbols {
		io.printf("    - %s\n", s)
	}
	io.printf("  files:\n")
	for _, f := range cand.Files {
		io.printf("    - %s\n", f)
	}
	io.printf("  evidence:\n")
	for _, e := range cand.Evidence {
		io.printf("    [%s] %s\n", e.Signal, e.Detail)
	}
	if derr == nil {
		data, _ := schema.MarshalCanonical(draft)
		io.printf("\n  draft manifest (%s):\n", draftPath)
		for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			io.printf("    %s\n", line)
		}
	} else {
		io.printf("\n  (no draft yet — run `lattice import draft`)\n")
	}
	if d := sess.Decisions[cand.ID]; d != "" {
		io.printf("\n  decision: %s\n", d)
	}
	io.printf("\n  accept:  lattice import review %s --accept\n", cand.ID)
	io.printf("  reject:  lattice import review %s --reject\n", cand.ID)
	return nil
}

type verifyReport struct {
	Validation   validateReport                `json:"validation"`
	Verification importer.VerificationCoverage `json:"verification_coverage"`
}

func newImportVerifyCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "verify",
		Short: "Stage 4 — validate the substrate the import generated",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			kg, err := buildGraph(cmd.Context(), ws, false)
			if err != nil {
				return io.fail("VERIFY_FAILED", err.Error(), nil)
			}
			cfg, _ := config.Load(ws.LatticeDir)
			violations := validate.Validate(kg, cfg, validate.Options{ReviewMode: ws.Review})
			report := validateReport{Violations: violations, ReviewMode: ws.Review}
			for _, v := range violations {
				if v.IsError() {
					report.Errors++
				} else {
					report.Warnings++
				}
			}
			report.OK = report.Errors == 0
			vc := importer.ComputeVerification(kg.Features, violations)

			if io.JSON {
				_ = io.printJSON(verifyReport{Validation: report, Verification: vc})
			} else {
				renderValidate(io, report)
				io.printf("verification coverage: %.1f%% (%d/%d invariants enforced and verified)\n",
					vc.Ratio*100, vc.VerifiedInvariants, vc.TotalInvariants)
			}
			if !report.OK {
				return errExit
			}
			return nil
		},
	}
}

// findCandidate looks up a candidate by id.
func findCandidate(cf importer.CandidatesFile, id string) (importer.Candidate, bool) {
	for _, c := range cf.Candidates {
		if c.ID == id {
			return c, true
		}
	}
	return importer.Candidate{}, false
}

func newImportInscribeCommand(io *IO) *cobra.Command {
	var inline, apply bool
	cmd := &cobra.Command{
		Use:   "inscribe",
		Short: "Stage 5 — attach accepted features to code (sidecar map; --inline writes annotations)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			cf, err := importer.LoadCandidates(filepath.Join(ws.ImportDir(), importer.CandidatesFileName))
			if err != nil {
				return io.fail("NO_IMPORT_SESSION",
					"no candidates; run `lattice import scan` first", nil)
			}
			sessPath := filepath.Join(ws.ImportDir(), importer.SessionFileName)
			sess, _ := importer.LoadSession(sessPath)

			accepted, err := acceptedFeatures(io, ws, cf, sess)
			if err != nil {
				return err
			}
			if len(accepted) == 0 {
				return io.fail("NOTHING_ACCEPTED",
					"no accepted candidates; run `lattice import review <id> --accept` first", nil)
			}

			if inline {
				return inscribeInline(cmd.Context(), io, ws, sess, sessPath, accepted, apply)
			}

			am := importer.AnnotationMap{}
			capEdges := 0
			for _, a := range accepted {
				// Heuristic capability matching — populates per-cap
				// symbol links so verify doesn't fire UNIMPLEMENTED_CAPABILITY
				// for every cap on every accepted feature.
				capLinks := importer.MatchCapabilities(a.Symbols, a.Capabilities)
				for _, fqns := range capLinks {
					capEdges += len(fqns)
				}
				am.Features = append(am.Features, importer.AnnotationMapFeature{
					ID:           a.Feature,
					Symbols:      a.Symbols,
					Capabilities: capLinks,
				})
			}
			amPath := filepath.Join(ws.ImportDir(), importer.AnnotationMapFileName)
			if err := importer.SaveAnnotationMap(amPath, am); err != nil {
				return io.fail("INSCRIBE_FAILED", err.Error(), nil)
			}
			markInscribed(sessPath, sess)
			if io.JSON {
				return io.printJSON(am)
			}
			io.printf("Wrote sidecar map (%d feature(s), %d capability edge(s)) -> %s\n",
				len(am.Features), capEdges, amPath)
			io.printf("Sidecar mode: NO source files were modified.\n")
			io.printf("To write real annotations into source instead: lattice import inscribe --inline --apply\n")
			io.printf("Run `lattice extract` to rebuild lattice.json.\n")
			return nil
		},
	}
	cmd.Flags().BoolVar(&inline, "inline", false, "write real annotations into source instead of the sidecar map")
	cmd.Flags().BoolVar(&apply, "apply", false, "with --inline: write the changes (default previews them)")
	return cmd
}

// inscribeInline plans, and with --apply writes, real source annotations.
func inscribeInline(ctx context.Context, io *IO, ws *workspace.Workspace, sess importer.Session,
	sessPath string, accepted []importer.FeatureSymbols, apply bool) error {

	modules, reg, err := modulesAndRegistry(ctx, ws)
	if err != nil {
		return io.fail("EXTRACT_FAILED", err.Error(), nil)
	}
	plan, err := importer.PlanInscribe(modules, reg, accepted)
	if err != nil {
		return io.fail("INSCRIBE_FAILED", err.Error(), nil)
	}

	if !apply {
		if io.JSON {
			return io.printJSON(plan)
		}
		io.printf("Inline inscribe plan: %d annotation(s) to insert\n", len(plan.Edits))
		for _, e := range plan.Edits {
			io.printf("  %s:%d  @feature %s  (%s)\n", e.File, e.Line, e.Feature, e.Symbol)
		}
		if len(plan.AlreadyMarked) > 0 {
			io.printf("  (%d symbol(s) already annotated — skipped)\n", len(plan.AlreadyMarked))
		}
		io.printf("\nApply with: lattice import inscribe --inline --apply\n")
		return nil
	}

	res := importer.ApplyInscribe(ctx, plan, reg, absResolver(ws))
	if len(res.FilesChanged) > 0 {
		markInscribed(sessPath, sess)
	}
	if len(res.Applied) > 0 {
		recPath := filepath.Join(ws.ImportDir(), importer.InscribedRecordFileName)
		prior, _ := importer.LoadInscribeRecord(recPath)
		_ = importer.SaveInscribeRecord(recPath, append(prior, res.Applied...))
	}
	if io.JSON {
		_ = io.printJSON(res)
	} else {
		io.printf("Inscribed %d annotation(s) into %d file(s)\n",
			res.AnnotationsInserted, len(res.FilesChanged))
		for _, f := range res.FilesChanged {
			io.printf("  %s\n", f)
		}
		for _, f := range res.Failed {
			io.printf("  FAILED %s: %s\n", f.File, f.Reason)
		}
	}
	if len(res.Failed) > 0 {
		return errExit
	}
	return nil
}

// acceptedFeatures resolves every accepted candidate to its feature id (from
// the draft manifest) and the symbol set the import discovered for it.
func acceptedFeatures(io *IO, ws *workspace.Workspace, cf importer.CandidatesFile,
	sess importer.Session) ([]importer.FeatureSymbols, error) {

	var out []importer.FeatureSymbols
	for _, c := range cf.Candidates {
		if sess.Decisions[c.ID] != importer.DecisionAccepted {
			continue
		}
		// Prefer the accepted feature manifest in features/ — a human may
		// have edited capabilities there. Fall back to the draft when the
		// feature file has been renamed or removed.
		draftPath := filepath.Join(ws.ImportDir(), importer.DraftsDirName, c.ID+".yaml")
		m, derr := schema.LoadManifest(draftPath)
		if derr != nil {
			return nil, io.fail("NO_DRAFT",
				"accepted candidate "+c.ID+" has no draft; run `lattice import draft`", nil)
		}
		acceptedPath := filepath.Join(ws.FeaturesDir(),
			filepath.FromSlash(strings.ReplaceAll(m.ID, ".", "/"))+".yaml")
		if real, err := schema.LoadManifest(acceptedPath); err == nil {
			m = real
		}
		out = append(out, importer.FeatureSymbols{
			Feature:      m.ID,
			Symbols:      c.Symbols,
			Capabilities: m.Capabilities,
		})
	}
	return out, nil
}

// markInscribed advances the session to the inscribed state.
func markInscribed(sessPath string, sess importer.Session) {
	sess.Status = importer.StatusInscribed
	_ = importer.SaveSession(sessPath, sess)
}

// absResolver maps a code-root-relative IR path to an absolute path.
func absResolver(ws *workspace.Workspace) func(string) string {
	var avail []workspace.CodeRoot
	for _, r := range ws.CodeRoots {
		if r.Available {
			avail = append(avail, r)
		}
	}
	multi := len(avail) > 1
	byName := map[string]string{}
	for _, r := range avail {
		byName[r.Name] = r.Abs
	}
	return func(file string) string {
		if multi {
			if i := strings.IndexByte(file, '/'); i > 0 {
				if abs, ok := byName[file[:i]]; ok {
					return filepath.Join(abs, file[i+1:])
				}
			}
		}
		return filepath.Join(ws.PrimaryCodeRoot().Abs, file)
	}
}

func newImportUninscribeCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "uninscribe",
		Short: "Reverse an inline inscribe — remove the annotations it wrote into source",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			recPath := filepath.Join(ws.ImportDir(), importer.InscribedRecordFileName)
			edits, err := importer.LoadInscribeRecord(recPath)
			if err != nil {
				return io.fail("NO_INSCRIBE_RECORD",
					"nothing to uninscribe; no inline inscribe has been applied", nil)
			}
			reg, err := registryFor(ws)
			if err != nil {
				return io.fail("UNINSCRIBE_FAILED", err.Error(), nil)
			}
			res := importer.Uninscribe(cmd.Context(), edits, reg, absResolver(ws))
			if len(res.Failed) == 0 {
				_ = os.Remove(recPath)
			}
			if io.JSON {
				_ = io.printJSON(res)
			} else {
				io.printf("Removed %d annotation(s) from %d file(s)\n",
					res.AnnotationsRemoved, len(res.FilesChanged))
				for _, f := range res.FilesChanged {
					io.printf("  %s\n", f)
				}
				for _, f := range res.Failed {
					io.printf("  FAILED %s: %s\n", f.File, f.Reason)
				}
			}
			if len(res.Failed) > 0 {
				return errExit
			}
			return nil
		},
	}
}

// registryFor builds the adapter registry for a workspace.
func registryFor(ws *workspace.Workspace) (*adapters.Registry, error) {
	adCfg, err := config.LoadAdapters(ws.LatticeDir)
	if err != nil {
		return nil, err
	}
	return all.Registry(adCfg), nil
}

// modulesAndRegistry parses the workspace's code roots into IR modules and
// returns the adapter registry used to parse them.
func modulesAndRegistry(ctx context.Context, ws *workspace.Workspace) ([]ir.Module, *adapters.Registry, error) {
	reg, err := registryFor(ws)
	if err != nil {
		return nil, nil, err
	}
	res, err := extract.Extract(ctx, ws, reg, extract.Options{})
	if err != nil {
		return nil, nil, err
	}
	return res.Modules, reg, nil
}

// parseModules parses the workspace's code roots into IR modules.
func parseModules(ctx context.Context, ws *workspace.Workspace) ([]ir.Module, error) {
	modules, _, err := modulesAndRegistry(ctx, ws)
	return modules, err
}
