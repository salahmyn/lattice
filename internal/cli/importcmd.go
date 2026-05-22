package cli

import (
	"context"
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
		newImportStatusCommand(io),
	)
	return cmd
}

type importStatus struct {
	Status                string  `json:"status"`
	Scope                 string  `json:"scope,omitempty"`
	Candidates            int     `json:"candidates"`
	Accepted              int     `json:"accepted"`
	Rejected              int     `json:"rejected"`
	DiscoveryCoverage     float64 `json:"discovery_coverage"`
	DocumentationCoverage float64 `json:"documentation_coverage"`
}

func newImportScanCommand(io *IO) *cobra.Command {
	var scope string
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
				Scope:               scope,
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
			if scope != "" {
				io.printf("  scope:    %s\n", scope)
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
	cmd.Flags().StringVar(&scope, "scope", "", "restrict discovery to a code-root-relative subtree")
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
				Scope:                 sess.Scope,
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
			if st.Scope != "" {
				io.printf("  scope:                  %s\n", st.Scope)
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
			// Verification coverage needs the graph and the validation engine.
			if kg, gerr := buildGraph(cmd.Context(), ws, false); gerr == nil {
				cfg, _ := config.Load(ws.LatticeDir)
				viol := validate.Validate(kg, cfg, validate.Options{ReviewMode: ws.Review})
				report.Verification = importer.ComputeVerification(kg.Features, viol)
			}
			if io.JSON {
				return io.printJSON(report)
			}
			d, doc, ver := report.Discovery, report.Documentation, report.Verification
			io.printf("Discovery coverage:     %.1f%%  (%d/%d production symbols clustered into candidates)\n",
				d.Ratio*100, d.ClusteredSymbols, d.TotalSymbols)
			io.printf("Documentation coverage: %.1f%%  (%d/%d symbols attached to an accepted feature)\n",
				doc.Ratio*100, doc.DocumentedSymbols, doc.TotalSymbols)
			io.printf("Verification coverage:  %.1f%%  (%d/%d invariants enforced and verified)\n",
				ver.Ratio*100, ver.VerifiedInvariants, ver.TotalInvariants)
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

			cfg, _ := config.Load(ws.LatticeDir)
			provider := agentic.FromConfig(cfg.Agentic.LLM)
			mode := "deterministic"
			var drafts []importer.Draft
			if !noLLM && agentic.Enabled(provider) {
				drafts = importer.LabelWithLLM(cmd.Context(), cf, provider, importer.LLMLabelOptions{
					MaxTokens: cfg.Agentic.LLM.MaxTokens,
					CacheDir:  ws.CacheDir(),
				})
				mode = "llm (" + provider.Name() + ")"
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
			if io.JSON {
				return io.printJSON(drafts)
			}
			io.printf("Drafted %d manifest(s) [%s] -> %s\n", len(drafts), mode, draftsDir)
			for _, d := range drafts {
				io.printf("  %s  ->  %s\n", d.CandidateID, d.Manifest.ID)
			}
			io.printf("\nReview them: lattice import review <candidate-id>\n")
			return nil
		},
	}
	cmd.Flags().BoolVar(&noLLM, "no-llm", false, "force the deterministic labeler even when an LLM is configured")
	return cmd
}

func newImportReviewCommand(io *IO) *cobra.Command {
	var accept, reject bool
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
			sessPath := filepath.Join(ws.ImportDir(), importer.SessionFileName)
			sess, _ := importer.LoadSession(sessPath)

			if len(args) == 0 {
				if accept || reject {
					return io.fail("NO_CANDIDATE", "--accept/--reject needs a candidate id", nil)
				}
				return reviewList(io, cf, sess)
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

	if io.JSON {
		out := map[string]string{"candidate": cand.ID, "decision": decision}
		if manifestPath != "" {
			out["manifest"] = manifestPath
		}
		return io.printJSON(out)
	}
	if manifestPath != "" {
		io.printf("accepted %s -> %s\n", cand.ID, manifestPath)
		io.printf("verify the generated substrate: lattice import verify\n")
	} else {
		io.printf("rejected %s\n", cand.ID)
	}
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
			for _, a := range accepted {
				am.Features = append(am.Features, importer.AnnotationMapFeature{ID: a.Feature, Symbols: a.Symbols})
			}
			amPath := filepath.Join(ws.ImportDir(), importer.AnnotationMapFileName)
			if err := importer.SaveAnnotationMap(amPath, am); err != nil {
				return io.fail("INSCRIBE_FAILED", err.Error(), nil)
			}
			markInscribed(sessPath, sess)
			if io.JSON {
				return io.printJSON(am)
			}
			io.printf("Wrote sidecar map (%d feature(s)) -> %s\n", len(am.Features), amPath)
			io.printf("The graph now links these features to code — no source was modified.\n")
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
		draftPath := filepath.Join(ws.ImportDir(), importer.DraftsDirName, c.ID+".yaml")
		m, derr := schema.LoadManifest(draftPath)
		if derr != nil {
			return nil, io.fail("NO_DRAFT",
				"accepted candidate "+c.ID+" has no draft; run `lattice import draft`", nil)
		}
		out = append(out, importer.FeatureSymbols{Feature: m.ID, Symbols: c.Symbols})
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
