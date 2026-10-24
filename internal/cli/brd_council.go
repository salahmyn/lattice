package cli

// @module-feature cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/brd"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/ledger"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// councilMandate resolves the brd_approve mandate a council acts under: the
// one named, or the only one pinned.
func councilMandate(cfg config.Config, id string) (config.Mandate, error) {
	if id != "" {
		m, ok := cfg.Autonomy.FindMandate(id)
		if !ok {
			return m, fmt.Errorf("mandate %q is not pinned in lattice/config.yaml; agents never create their own mandates", id)
		}
		return m, nil
	}
	var found []config.Mandate
	for _, m := range cfg.Autonomy.Mandates {
		if m.Grants == config.GrantBRDApprove {
			found = append(found, m)
		}
	}
	switch len(found) {
	case 0:
		return config.Mandate{}, fmt.Errorf("no brd_approve mandate is pinned in lattice/config.yaml; BRD approval stays with a human")
	case 1:
		return found[0], nil
	}
	return config.Mandate{}, fmt.Errorf("%d brd_approve mandates are pinned; name one with --mandate", len(found))
}

func newBRDVoteCommand(io *IO) *cobra.Command {
	var seat, verdict, reason, mandateID string
	cmd := &cobra.Command{
		Use:   "vote <id>",
		Short: "Record one council seat's vote on a BRD's current version",
		Long: `In council mode, each seat of the brd_approve mandate's council reviews
the BRD's current version and votes approve or changes, with a reason.
The voting agent is --actor (or LATTICE_ACTOR); each seat needs its own
agent, and the agent that wrote the version may not hold a seat.
Votes are kept in lattice/decisions/brd-council/<id>-v<N>.votes.yaml.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			cfg, _ := config.Load(ws.LatticeDir)
			m, err := councilMandate(cfg, mandateID)
			if err != nil {
				return io.fail("COUNCIL_NO_MANDATE", err.Error(), nil)
			}
			if m.Council == nil {
				return io.fail("COUNCIL_NO_SEATS", "mandate "+m.ID+" declares no council seats", nil)
			}
			b, err := schema.LoadBRD(brd.PathFor(ws.BRDsDir(), args[0]))
			if err != nil {
				return io.fail("BRD_NOT_FOUND", err.Error(), nil)
			}
			v := schema.BRDCouncilVote{
				Seat: seat, Actor: io.actor(), Verdict: verdict, Reason: reason,
				At: time.Now().UTC().Format(time.RFC3339),
			}
			if err := brd.RecordVote(ws.LatticeDir, *b, *m.Council, v); err != nil {
				return io.fail("COUNCIL_VOTE", err.Error(), nil)
			}
			appendLedgerEvent(io, ws, ledger.EventGate, b.ID,
				fmt.Sprintf("council vote v%d %s: %s (%s)", b.Version, seat, verdict, reason))
			if io.JSON {
				return io.printJSON(map[string]string{"brd": b.ID, "seat": seat, "verdict": verdict})
			}
			io.printf("%s v%d: seat %s votes %s\n", b.ID, b.Version, seat, verdict)
			return nil
		},
	}
	cmd.Flags().StringVar(&seat, "seat", "", "the council seat voting (required)")
	cmd.Flags().StringVar(&verdict, "verdict", "", "approve or changes (required)")
	cmd.Flags().StringVar(&reason, "reason", "", "why, for the human who reads the decision (required)")
	cmd.Flags().StringVar(&mandateID, "mandate", "", "brd_approve mandate (default: the only one pinned)")
	return cmd
}

func newBRDDecideCommand(io *IO) *cobra.Command {
	var summary, why, mandateID string
	cmd := &cobra.Command{
		Use:   "decide <id>",
		Short: "Turn the council's votes into a decision on a BRD's current version",
		Long: `Checks that a council may decide the BRD and counts the votes:

  - an autonomy mode without a brd_approval human gate (default_mode: council)
  - a human-pinned brd_approve mandate that covers the BRD's tiers and is live
  - no tier-3 criterion and no regulatory, legal or financial constraint:
    those stay with a human whatever the mandate says
  - every seat voted on this version, each by its own agent, none of them the
    agent that wrote the version

If every seat approves, the BRD is approved at once and the decision, its
reasons and the engine's evidence are written on the BRD and to
lattice/decisions/brd-council/<id>-v<N>.md for the human it acts for, who
may 'lattice brd ack' it or 'lattice brd overturn' it. One "changes" vote
sends the BRD back and the summary lists what to change.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			if strings.TrimSpace(summary) == "" {
				return io.fail("COUNCIL_SUMMARY_REQUIRED", "--summary is required: the human reads it first", nil)
			}
			cfg, _ := config.Load(ws.LatticeDir)
			m, err := councilMandate(cfg, mandateID)
			if err != nil {
				return io.fail("COUNCIL_NO_MANDATE", err.Error(), nil)
			}
			kg, _, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			b := brd.Find(kg.BRDs, args[0])
			if b == nil {
				return io.fail("BRD_NOT_FOUND", "BRD not found: "+args[0], nil)
			}
			if b.Status != schema.BRDDraft && b.Status != schema.BRDProposed {
				return io.fail("BRD_WRONG_STATE", fmt.Sprintf("%s is %s; a council decides draft or proposed BRDs", b.ID, b.Status), nil)
			}
			rec, err := brd.LoadRecord(ws.LatticeDir, b.ID, b.Version)
			if err != nil {
				return io.fail("COUNCIL_VOTES", err.Error(), nil)
			}
			if o := rec.Overturned; o != nil {
				return io.fail("COUNCIL_OVERTURNED", fmt.Sprintf("%s v%d was overturned by %s (%s); revise the BRD before the council decides again",
					b.ID, b.Version, o.By, o.Reason), nil)
			}
			votes := rec.Votes
			now := time.Now().UTC()
			verdict := brd.Assess(*b, cfg.Autonomy, m.ID, votes, now.Format("2006-01-02"))
			if len(verdict.Blockers) > 0 {
				return io.fail("COUNCIL_BLOCKED", "the council cannot decide "+b.ID+": "+strings.Join(verdict.Blockers, "; "), nil)
			}

			d := schema.BRDCouncilDecision{
				Outcome: "approved", Summary: summary, Why: why,
				Evidence: councilEvidence(kg, ws.LatticeDir, cfg, b.ID), Votes: votes,
			}
			if !verdict.Approved {
				d.Outcome = "changes_requested"
			}
			if d.Why == "" {
				d.Why = votesWhy(votes)
			}
			at := now.Format(time.RFC3339)
			if verdict.Approved {
				b.Status = schema.BRDApproved
				b.Approval = &schema.BRDApproval{
					ApprovedBy: "council:" + m.ID, ApprovedAt: at, ApprovedVersion: b.Version,
					Mandate: m.ID, DelegatedBy: m.DelegatedBy, Decision: &d,
					HumanReview: schema.HumanReviewPending,
				}
				if _, err := brd.SaveForce(ws.BRDsDir(), *b); err != nil {
					return io.fail("BRD_SAVE_FAILED", err.Error(), nil)
				}
			}
			path, err := brd.WriteSummary(ws.LatticeDir, *b, brd.RenderSummary(*b, d, m, at))
			if err != nil {
				return io.fail("COUNCIL_SUMMARY", err.Error(), nil)
			}
			appendLedgerEvent(io, ws, ledger.EventGate, b.ID,
				fmt.Sprintf("council %s v%d under %s for %s: %s", d.Outcome, b.Version, m.ID, m.DelegatedBy, summary))
			if io.JSON {
				return io.printJSON(map[string]string{"brd": b.ID, "outcome": d.Outcome, "summary": path})
			}
			io.printf("%s v%d: %s by the council under %s\n", b.ID, b.Version, strings.ReplaceAll(d.Outcome, "_", " "), m.ID)
			for _, c := range verdict.Changes {
				io.printf("  %s (%s): %s\n", c.Seat, c.Actor, c.Reason)
			}
			io.printf("decision for %s: %s\n", m.DelegatedBy, path)
			return nil
		},
	}
	cmd.Flags().StringVar(&summary, "summary", "", "one-paragraph summary of the decision for the human (required)")
	cmd.Flags().StringVar(&why, "why", "", "why the council decided so (default: the seats' reasons)")
	cmd.Flags().StringVar(&mandateID, "mandate", "", "brd_approve mandate (default: the only one pinned)")
	return cmd
}

// councilEvidence is what the engine itself says about the BRD: how many of
// its criteria and scenarios reached demonstrated on the ingested results.
func councilEvidence(kg schema.KnowledgeGraph, latticeDir string, cfg config.Config, id string) string {
	m := rtm.Build(kg, rtm.Options{
		MutationThreshold: cfg.MutationTesting.Thresholds.Default,
		ResultOf:          resultOfFrom(results.Load(latticeDir)),
	})
	crit, critDone, scen, scenDone := 0, 0, 0, 0
	for _, r := range m.Rows {
		if r.BRDID == id {
			crit++
			if r.Status.IsDemonstrated() {
				critDone++
			}
		}
	}
	for _, s := range m.Scenarios {
		if s.BRDID == id {
			scen++
			if s.Status.IsDemonstrated() {
				scenDone++
			}
		}
	}
	return fmt.Sprintf("criteria %d/%d demonstrated; scenarios %d/%d demonstrated", critDone, crit, scenDone, scen)
}

func votesWhy(votes []schema.BRDCouncilVote) string {
	parts := make([]string, 0, len(votes))
	for _, v := range votes {
		parts = append(parts, v.Seat+": "+v.Reason)
	}
	return strings.Join(parts, "; ")
}

// councilApproved loads a BRD that a council approved and checks that by is
// the human the council acted for.
func councilApproved(brdsDir, id, by string) (*schema.BRD, error) {
	if strings.TrimSpace(by) == "" {
		return nil, fmt.Errorf("--by <email> is required")
	}
	b, err := schema.LoadBRD(brd.PathFor(brdsDir, id))
	if err != nil {
		return nil, err
	}
	if b.Status != schema.BRDApproved || b.Approval == nil || b.Approval.Mandate == "" {
		return nil, fmt.Errorf("%s has no council approval to review", id)
	}
	if !strings.EqualFold(by, b.Approval.DelegatedBy) {
		return nil, fmt.Errorf("the council acted for %s; only they review its decision", b.Approval.DelegatedBy)
	}
	return b, nil
}

func newBRDAckCommand(io *IO) *cobra.Command {
	var by string
	cmd := &cobra.Command{
		Use:   "ack <id>",
		Short: "Acknowledge a council's approval of a BRD (the delegating human)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			b, err := councilApproved(ws.BRDsDir(), args[0], by)
			if err != nil {
				return io.fail("COUNCIL_REVIEW", err.Error(), nil)
			}
			b.Approval.HumanReview = schema.HumanReviewAcknowledged
			b.Approval.ReviewedBy = by
			b.Approval.ReviewedAt = time.Now().UTC().Format(time.RFC3339)
			if _, err := brd.SaveForce(ws.BRDsDir(), *b); err != nil {
				return io.fail("BRD_SAVE_FAILED", err.Error(), nil)
			}
			_ = brd.AppendSummary(ws.LatticeDir, b.ID, b.Version,
				fmt.Sprintf("## Acknowledged\n\nAcknowledged by %s on %s.\n", by, b.Approval.ReviewedAt))
			appendLedgerEvent(io, ws, ledger.EventGate, b.ID, fmt.Sprintf("council decision v%d acknowledged by %s", b.Version, by))
			if io.JSON {
				return io.printJSON(map[string]string{"acknowledged": b.ID})
			}
			io.printf("acknowledged the council's approval of %s v%d\n", b.ID, b.Version)
			return nil
		},
	}
	cmd.Flags().StringVar(&by, "by", "", "email of the human the council acted for (required)")
	return cmd
}

func newBRDOverturnCommand(io *IO) *cobra.Command {
	var by, reason string
	cmd := &cobra.Command{
		Use:   "overturn <id>",
		Short: "Overturn a council's approval: the BRD goes back to proposed",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			if strings.TrimSpace(reason) == "" {
				return io.fail("COUNCIL_REVIEW", "--reason is required: the council and the authors need to know why", nil)
			}
			b, err := councilApproved(ws.BRDsDir(), args[0], by)
			if err != nil {
				return io.fail("COUNCIL_REVIEW", err.Error(), nil)
			}
			version := b.Version
			b.Status = schema.BRDProposed
			b.Approval = nil
			if _, err := brd.SaveForce(ws.BRDsDir(), *b); err != nil {
				return io.fail("BRD_SAVE_FAILED", err.Error(), nil)
			}
			now := time.Now().UTC().Format(time.RFC3339)
			if err := brd.MarkOverturned(ws.LatticeDir, b.ID, version, brd.Overturn{By: by, At: now, Reason: reason}); err != nil {
				return io.fail("COUNCIL_REVIEW", err.Error(), nil)
			}
			// The summary keeps the decision and now says it was overturned.
			_ = brd.AppendSummary(ws.LatticeDir, b.ID, version,
				fmt.Sprintf("## Overturned\n\nOverturned by %s on %s: %s\n", by, now, reason))
			appendLedgerEvent(io, ws, ledger.EventGate, b.ID, fmt.Sprintf("council decision v%d overturned by %s: %s", version, by, reason))
			if io.JSON {
				return io.printJSON(map[string]string{"overturned": b.ID})
			}
			io.printf("overturned the council's approval of %s v%d: it is proposed again\n", b.ID, version)
			return nil
		},
	}
	cmd.Flags().StringVar(&by, "by", "", "email of the human the council acted for (required)")
	cmd.Flags().StringVar(&reason, "reason", "", "why the decision is overturned (required)")
	return cmd
}
