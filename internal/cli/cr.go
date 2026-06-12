package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/brd"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/flags"
	"github.com/salahmyn/lattice/pkg/lattice/lease"
	"github.com/salahmyn/lattice/pkg/lattice/ledger"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/revision"
	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// newCRCommand is the change-request flow (v0.8.1) — the only
// legitimate path for changing grounded business intent. Grounded
// artifacts are never edited in place:
//
//	cr propose  — capture the change (CR-1); the live graph is untouched
//	cr price    — compute the blast radius + classify (CR-2); cost before commitment
//	cr decide   — the human gate (CR-3) + atomic propagation (CR-4):
//	              apply text, demote affected criteria to flagged
//	              (stale green never rides), spawn work/retirement items
//	cr reconverge — close the loop (CR-5) once every demotion is cleared
func newCRCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cr",
		Short: "Change requests against grounded intent (propose → price → decide → reconverge)",
	}
	cmd.AddCommand(
		newCRProposeCommand(io), newCRPriceCommand(io), newCRDecideCommand(io),
		newCRReconvergeCommand(io), newCRListCommand(io), newCRShowCommand(io),
	)
	return cmd
}

func newCRProposeCommand(io *IO) *cobra.Command {
	var text string
	cmd := &cobra.Command{
		Use:   "propose <target> [<target>...]",
		Short: "CR-1: materialize a proposed change (targets: brd.x.y or brd.x.y/SC-1)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kg, ws, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			revs, _ := revision.LoadAll(ws.LatticeDir)
			r := schema.Revision{
				ID:           revision.NextID(revs),
				Status:       schema.RevisionProposed,
				Targets:      args,
				ProposedText: text,
				PreviousText: previousTextFor(kg, args),
			}
			path, err := revision.Save(ws.LatticeDir, r)
			if err != nil {
				return io.fail("CR_FAILED", err.Error(), nil)
			}
			appendLedgerEvent(io, ws, ledger.EventCR, r.ID, "proposed: "+strings.Join(args, ", "))
			if io.JSON {
				return io.printJSON(r)
			}
			io.printf("%s proposed → %s\n", r.ID, path)
			io.printf("next: lattice cr price %s --class wording|widening|narrowing|contradiction\n", r.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&text, "text", "", "the proposed replacement text (required)")
	_ = cmd.MarkFlagRequired("text")
	return cmd
}

func newCRPriceCommand(io *IO) *cobra.Command {
	var class string
	cmd := &cobra.Command{
		Use:   "price <CR-n>",
		Short: "CR-2: compute the blast radius and classify — cost before commitment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !validClass(class) {
				return io.fail("CR_BAD_CLASS",
					"--class must be wording, widening, narrowing, or contradiction (strictest wins on a mixed change)", nil)
			}
			kg, ws, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			revs, _ := revision.LoadAll(ws.LatticeDir)
			r := revision.Find(revs, args[0])
			if r == nil {
				return io.fail("CR_NOT_FOUND", args[0]+" not found under lattice/revisions/", nil)
			}
			if r.Status != schema.RevisionProposed {
				return io.fail("CR_WRONG_STATE", r.ID+" is "+string(r.Status)+", not proposed", nil)
			}

			cfg, _ := config.Load(ws.LatticeDir)
			set := results.Load(ws.LatticeDir)
			m := rtm.Build(kg, rtm.Options{
				MutationThreshold: cfg.MutationTesting.Thresholds.Default,
				ResultOf:          resultOfFrom(set),
			})
			activeLeases, _ := lease.Active(ws.LatticeDir, time.Now())

			r.Class = schema.RevisionClass(class)
			r.Impact = revision.ComputeImpact(m, kg, activeLeases, r.Targets)
			if _, err := revision.Save(ws.LatticeDir, *r); err != nil {
				return io.fail("CR_FAILED", err.Error(), nil)
			}
			appendLedgerEvent(io, ws, ledger.EventCR, r.ID,
				fmt.Sprintf("priced: class=%s tier<=%d, %d invariants, %d tests, %d scenarios, %d conflicts",
					class, r.Impact.MaxTier, len(r.Impact.AffectedInvariants),
					len(r.Impact.AffectedTests), len(r.Impact.AffectedScenarios),
					len(r.Impact.InFlightConflicts)))
			if io.JSON {
				return io.printJSON(r)
			}
			renderImpactCard(io, *r)
			return nil
		},
	}
	cmd.Flags().StringVar(&class, "class", "",
		"wording | widening | narrowing | contradiction (required; strictest wins)")
	_ = cmd.MarkFlagRequired("class")
	return cmd
}

func newCRDecideCommand(io *IO) *cobra.Command {
	var approve, reject bool
	var by, mandateID, supersedes, note string
	cmd := &cobra.Command{
		Use:   "decide <CR-n>",
		Short: "CR-3 human gate + CR-4 propagation (apply, demote, spawn items)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if approve == reject {
				return io.fail("CR_BAD_DECISION", "pass exactly one of --approve / --reject", nil)
			}
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("WORKSPACE_FAILED", err.Error(), nil)
			}
			revs, _ := revision.LoadAll(ws.LatticeDir)
			r := revision.Find(revs, args[0])
			if r == nil {
				return io.fail("CR_NOT_FOUND", args[0]+" not found", nil)
			}
			if r.Status != schema.RevisionProposed {
				return io.fail("CR_WRONG_STATE", r.ID+" is already "+string(r.Status), nil)
			}
			if r.Class == "" {
				return io.fail("CR_UNPRICED",
					"price the CR first: lattice cr price "+r.ID+" --class …  — decisions need cost-before-commitment", nil)
			}

			cfg, _ := config.Load(ws.LatticeDir)
			now := time.Now()
			if err := authorizeDecision(cfg, *r, by, mandateID, now); err != nil {
				return io.fail("CR_GATE", err.Error(), nil)
			}

			r.Decision = &schema.RevisionDecision{
				By: by, At: now.UTC().Format(time.RFC3339), Mandate: mandateID, Note: note,
			}

			if reject {
				r.Status = schema.RevisionRejected
				r.Decision.Outcome = "rejected"
				if _, err := revision.Save(ws.LatticeDir, *r); err != nil {
					return io.fail("CR_FAILED", err.Error(), nil)
				}
				appendLedgerEvent(io, ws, ledger.EventCR, r.ID, "rejected by "+by+": "+note)
				io.printf("%s rejected — archived in place so the idea isn't re-litigated from zero\n", r.ID)
				return nil
			}

			// Contradiction approvals must explicitly retire the conflicting
			// Decision record — never silently override the written record.
			if r.Class == schema.RevisionContradiction && supersedes == "" {
				return io.fail("CR_NEEDS_SUPERSESSION",
					"a contradiction-class CR is blocked until the conflicting Decision is superseded — pass --supersedes-decision <id>", nil)
			}
			r.SupersedesDecision = supersedes

			// --- CR-4: propagate ---
			r.Status = schema.RevisionApproved
			r.Decision.Outcome = "approved"

			applied, applyErr := applyProposedText(ws.LatticeDir, *r)

			// Demote honestly: code proven against the old requirement is
			// unproven against the new one. Stale green never rides.
			r.Demotions = expandTargets(ws.LatticeDir, *r)
			for _, unit := range r.Demotions {
				_, _ = flags.Raise(ws.LatticeDir, unit,
					"demoted by "+r.ID+": requirement revised, re-verify against the new text",
					by, "cr:"+r.ID, now)
				appendLedgerEvent(io, ws, ledger.EventCR, unit, "demoted to flagged by "+r.ID)
			}
			r.WorkItems, r.RetirementItems = revision.SpawnItems(r.Class, r.Impact, r.Demotions)

			if _, err := revision.Save(ws.LatticeDir, *r); err != nil {
				return io.fail("CR_FAILED", err.Error(), nil)
			}
			appendLedgerEvent(io, ws, ledger.EventCR, r.ID, fmt.Sprintf(
				"approved by %s (mandate=%s): %d demotions, %d work items, %d retirement items",
				by, orDash(mandateID), len(r.Demotions), len(r.WorkItems), len(r.RetirementItems)))

			if io.JSON {
				return io.printJSON(r)
			}
			io.printf("%s approved by %s\n", r.ID, by)
			if applyErr != nil {
				io.printf("  ! proposed text NOT auto-applied: %v — apply it manually\n", applyErr)
			} else if applied > 0 {
				io.printf("  applied proposed text to %d criterion/criteria (re-grounded)\n", applied)
			}
			io.printf("  demoted to flagged: %s\n", strings.Join(r.Demotions, ", "))
			for _, w := range r.WorkItems {
				io.printf("  work: %s\n", w)
			}
			for _, ri := range r.RetirementItems {
				io.printf("  retire (only legal against this item): %s\n", ri)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&approve, "approve", false, "approve the CR")
	cmd.Flags().BoolVar(&reject, "reject", false, "reject the CR (archived, never deleted)")
	cmd.Flags().StringVar(&by, "by", "", "who decides (required; human unless a mandate covers it)")
	cmd.Flags().StringVar(&mandateID, "mandate", "", "mandate id pre-authorizing this decision (wording-class only)")
	cmd.Flags().StringVar(&supersedes, "supersedes-decision", "", "Decision id this contradiction-class CR retires")
	cmd.Flags().StringVar(&note, "note", "", "decision rationale")
	_ = cmd.MarkFlagRequired("by")
	return cmd
}

func newCRReconvergeCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "reconverge <CR-n>",
		Short: "CR-5: close the loop once every demoted unit's flag is cleared",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("WORKSPACE_FAILED", err.Error(), nil)
			}
			revs, _ := revision.LoadAll(ws.LatticeDir)
			r := revision.Find(revs, args[0])
			if r == nil {
				return io.fail("CR_NOT_FOUND", args[0]+" not found", nil)
			}
			if r.Status != schema.RevisionApproved {
				return io.fail("CR_WRONG_STATE", r.ID+" is "+string(r.Status)+", not approved", nil)
			}
			open := flags.OpenByUnit(ws.LatticeDir)
			var stale []string
			for _, unit := range r.Demotions {
				if len(open[unit]) > 0 {
					stale = append(stale, unit)
				}
			}
			if len(stale) > 0 {
				return io.fail("CR_NOT_RECONVERGED",
					"still demoted (a human clears each flag after re-verification): "+strings.Join(stale, ", "),
					map[string]interface{}{"stale_units": stale})
			}
			r.Status = schema.RevisionReconverged
			if _, err := revision.Save(ws.LatticeDir, *r); err != nil {
				return io.fail("CR_FAILED", err.Error(), nil)
			}
			appendLedgerEvent(io, ws, ledger.EventCR, r.ID, "reconverged: every demoted unit re-verified")
			io.printf("%s reconverged — every demoted unit re-verified against the revised intent\n", r.ID)
			return nil
		},
	}
}

func newCRListCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List change requests",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("WORKSPACE_FAILED", err.Error(), nil)
			}
			revs, viol := revision.LoadAll(ws.LatticeDir)
			if io.JSON {
				return io.printJSON(map[string]interface{}{"revisions": revs, "violations": viol})
			}
			if len(revs) == 0 {
				io.printf("no change requests — `lattice cr propose <target> --text …`\n")
				return nil
			}
			io.printf("%-8s %-13s %-14s %s\n", "id", "status", "class", "targets")
			io.printf("%s\n", strRepeat("-", 70))
			for _, r := range revs {
				io.printf("%-8s %-13s %-14s %s\n",
					r.ID, string(r.Status), orDash(string(r.Class)), strings.Join(r.Targets, ", "))
			}
			return nil
		},
	}
}

func newCRShowCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "show <CR-n>",
		Short: "Show one change request as an impact card",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("WORKSPACE_FAILED", err.Error(), nil)
			}
			revs, _ := revision.LoadAll(ws.LatticeDir)
			r := revision.Find(revs, args[0])
			if r == nil {
				return io.fail("CR_NOT_FOUND", args[0]+" not found", nil)
			}
			if io.JSON {
				return io.printJSON(r)
			}
			renderImpactCard(io, *r)
			return nil
		},
	}
}

// authorizeDecision enforces the CR-3 gate rules: a human decides,
// unless a valid mandate covers exactly this case. The non-delegable
// floor — narrowings, tier-2+ radii — holds regardless of mandates.
func authorizeDecision(cfg config.Config, r schema.Revision, by, mandateID string, now time.Time) error {
	isAgent := strings.HasPrefix(strings.ToLower(by), "agent")
	if !isAgent && mandateID == "" {
		return nil // a human deciding directly is always legitimate
	}
	if mandateID == "" {
		return fmt.Errorf("an agent cannot decide a CR without a mandate — escalate to a human or pass --mandate")
	}
	m, ok := cfg.Autonomy.FindMandate(mandateID)
	if !ok {
		return fmt.Errorf("mandate %s is not pinned in lattice/config.yaml — agents never create their own mandates", mandateID)
	}
	if !m.Covers("cr_decide", string(r.Class), r.Impact.MaxTier, now.UTC().Format("2006-01-02")) {
		return fmt.Errorf(
			"mandate %s does not cover this case (class=%s, tier<=%d) — narrowings and tier-2+ radii are never delegable; escalate to a human",
			mandateID, r.Class, r.Impact.MaxTier)
	}
	return nil
}

// applyProposedText applies the CR's proposed text to single-criterion
// targets — the CR approval IS the re-grounding. Multi-target or
// BRD-level CRs are left for a manual edit (reported, not silent).
func applyProposedText(latticeDir string, r schema.Revision) (int, error) {
	if strings.TrimSpace(r.ProposedText) == "" {
		return 0, nil
	}
	applied := 0
	for _, t := range r.Targets {
		i := strings.Index(t, "/")
		if i <= 0 {
			return applied, fmt.Errorf("target %s is BRD-level; edit the BRD per the approved text", t)
		}
		brdID, scID := t[:i], t[i+1:]
		dir := latticeDir + "/brds"
		b, err := schema.LoadBRD(brd.PathFor(dir, brdID))
		if err != nil {
			return applied, err
		}
		found := false
		for j := range b.SuccessCriteria {
			if b.SuccessCriteria[j].ID == scID {
				b.SuccessCriteria[j].Statement = r.ProposedText
				found = true
				break
			}
		}
		if !found {
			return applied, fmt.Errorf("%s has no criterion %s", brdID, scID)
		}
		b.Version++
		if _, err := brd.SaveForce(dir, *b); err != nil {
			return applied, err
		}
		applied++
	}
	return applied, nil
}

// expandTargets resolves CR targets to demotable units: criterion refs
// stay as-is; a BRD-level target demotes every criterion on it.
func expandTargets(latticeDir string, r schema.Revision) []string {
	var units []string
	for _, t := range r.Targets {
		if strings.Contains(t, "/") {
			units = append(units, t)
			continue
		}
		if b, err := schema.LoadBRD(brd.PathFor(latticeDir+"/brds", t)); err == nil {
			for _, sc := range b.SuccessCriteria {
				units = append(units, t+"/"+sc.ID)
			}
		}
	}
	return units
}

func renderImpactCard(io *IO, r schema.Revision) {
	io.printf("%s  [%s]  class=%s\n", r.ID, string(r.Status), orDash(string(r.Class)))
	io.printf("targets:   %s\n", strings.Join(r.Targets, ", "))
	if r.PreviousText != "" {
		io.printf("previous:  %s\n", truncate(r.PreviousText, 100))
	}
	io.printf("proposed:  %s\n", truncate(r.ProposedText, 100))
	imp := r.Impact
	io.printf("impact:    tier<=%d · %d invariants · %d symbols · %d tests · %d scenarios · %d entry points\n",
		imp.MaxTier, len(imp.AffectedInvariants), len(imp.AffectedSymbols),
		len(imp.AffectedTests), len(imp.AffectedScenarios), len(imp.AffectedEntryPoints))
	for _, c := range imp.InFlightConflicts {
		io.printf("  ! in-flight conflict: %s — hold the CR or attach it to that work\n", c)
	}
	if r.Class == schema.RevisionContradiction {
		io.printf("  ! contradiction: approval requires --supersedes-decision <id>\n")
	}
	if r.Decision != nil {
		io.printf("decision:  %s by %s at %s %s\n",
			r.Decision.Outcome, r.Decision.By, r.Decision.At, r.Decision.Note)
	}
	if len(r.Demotions) > 0 {
		io.printf("demotions: %s\n", strings.Join(r.Demotions, ", "))
	}
	for _, w := range r.WorkItems {
		io.printf("  work: %s\n", w)
	}
	for _, ri := range r.RetirementItems {
		io.printf("  retirement: %s\n", ri)
	}
}

// previousTextFor copies the current criterion statement(s) verbatim so
// the decision gate sees an exact diff.
func previousTextFor(kg schema.KnowledgeGraph, targets []string) string {
	var parts []string
	for _, t := range targets {
		i := strings.Index(t, "/")
		if i <= 0 {
			continue
		}
		for _, b := range kg.BRDs {
			if b.ID != t[:i] {
				continue
			}
			for _, sc := range b.SuccessCriteria {
				if sc.ID == t[i+1:] {
					parts = append(parts, sc.Statement)
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}

func validClass(c string) bool {
	for _, v := range schema.ValidRevisionClasses {
		if string(v) == c {
			return true
		}
	}
	return false
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
