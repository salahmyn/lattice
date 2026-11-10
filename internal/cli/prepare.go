package cli

// @module-feature confirm

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/confirm"
	"github.com/salahmyn/lattice/pkg/lattice/prepare"
)

// newPrepareCommand is `lattice prepare`: validate a decision against the
// kernel and write a decision branch. It prints lattice.prepare/1 and never
// pushes, never opens a pull request and never touches the checkout.
func newPrepareCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prepare",
		Short: "Prepare a decision branch for a human to approve on the platform",
		Long: `Validates a decision against the kernel and writes a decision branch
(decision/<id>-v<N>-<packet8>) holding one decision file,
lattice/decisions/approvals/<id>-v<N>-<packet8>.yaml, where <packet8> is the
first 8 hex digits of the decision's packet hash (actions other than approve
and waiver add a suffix before it, e.g. <id>-v<N>.ack-<packet8>). The branch is
built from HEAD with git plumbing: your checkout, index and working tree are
never touched, and nothing is pushed or opened. The JSON it prints
(lattice.prepare/1) carries the 'gh pr create' command to run.

The name comes from the packet, so a decision file is never overwritten.
Preparing the same packet again reports the existing branch and exits 0.
After the BRD or a promise changes (for example to re-confirm a promise that
CONFIRMATION_VOIDED reports), the packet differs and prepare writes a new
branch and file beside the earlier ones. A branch of the same name that holds
something else is refused (BRANCH_EXISTS); a decision already on HEAD is
refused (DECISION_EXISTS).

Nothing here confirms anything. A decision counts only when a user other than
the pull request's author approves the merged head and 'lattice confirm' reads
that approval from the platform. A refusal prints {refused, params, text, next}
and exits 1.`,
	}
	brdCmd := &cobra.Command{Use: "brd", Short: "Prepare a BRD decision"}
	brdCmd.AddCommand(prepareBRD(io, confirm.ActionApprove, "approve <id>", "Prepare approval of a BRD version"),
		prepareBRD(io, confirm.ActionReturn, "return <id>", "Prepare a return of a BRD with changes"),
		prepareBRD(io, confirm.ActionAck, "ack <id>", "Prepare an acknowledgement of a council's approval"),
		prepareBRD(io, confirm.ActionOverturn, "overturn <id>", "Prepare an overturn of a council's approval"))

	var brdID string
	promise := &cobra.Command{Use: "promise", Short: "Prepare a decision on promises"}
	confirmCmd := &cobra.Command{
		Use:   "confirm <promise>...",
		Short: "Prepare the confirmation of promises (one BRD per batch)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPrepare(io, cmd, prepare.Request{Action: confirm.ActionPromiseConfirm, ID: brdID, Refs: args})
		},
	}
	confirmCmd.Flags().StringVar(&brdID, "brd", "", "the BRD, when the promises belong to several")
	promise.AddCommand(confirmCmd)

	var w confirm.WaiverEntry
	waiver := &cobra.Command{Use: "waiver", Short: "Prepare a waiver"}
	add := &cobra.Command{
		Use:   "add",
		Short: "Prepare a waiver (shown, never counted as proof)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ww := w
			return runPrepare(io, cmd, prepare.Request{Action: confirm.ActionWaiverAdd, Waiver: &ww})
		},
	}
	add.Flags().StringVar(&w.Code, "code", "", "the finding code being waived (required)")
	add.Flags().StringVar(&w.Subject, "subject", "", "the promise (<feature>:INV-n) or file it applies to (required)")
	add.Flags().StringVar(&w.Kind, "kind", "", "closed kind: no_tool, generated_code, out_of_reach, accepted_risk (required)")
	add.Flags().StringVar(&w.Reason, "reason", "", "why (required)")
	add.Flags().StringVar(&w.ADR, "adr", "", "the ADR behind it, where the kind needs one")
	waiver.AddCommand(add)

	cmd.AddCommand(brdCmd, promise, waiver)
	return cmd
}

func prepareBRD(io *IO, action, use, short string) *cobra.Command {
	var version int
	var reason string
	var rows []string
	c := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPrepare(io, cmd, prepare.Request{Action: action, ID: args[0], Version: version, Reason: reason, Refs: rows})
		},
	}
	c.Flags().IntVar(&version, "version", 0, "the BRD version you reviewed (required for approve)")
	if action == confirm.ActionReturn || action == confirm.ActionOverturn {
		c.Flags().StringVar(&reason, "reason", "", "why (required)")
	}
	if action == confirm.ActionReturn {
		c.Flags().StringSliceVar(&rows, "row", nil, "a promise the reason refers to (repeatable)")
	}
	return c
}

// runPrepare gathers the kernel's facts and hands them to prepare.Run.
func runPrepare(io *IO, cmd *cobra.Command, req prepare.Request) error {
	kg, ws, err := graphFor(io, cmd, false)
	if err != nil {
		return io.fail("EXTRACT_FAILED", err.Error(), nil)
	}
	cfg, _ := config.Load(ws.LatticeDir)
	dirty, _ := gitDirty(filepath.Dir(ws.LatticeDir))
	env := prepare.Env{
		Git: confirm.Git{Dir: filepath.Dir(ws.LatticeDir)}, LatticeDir: ws.LatticeDir, KG: kg,
		Report: audit.Compute(audit.Collect(ws.LatticeDir, kg, dirty)), Config: cfg,
		Actor: io.actor(), Cache: confirm.LoadCache(ws.LatticeDir),
	}
	res, ref := prepare.Run(env, req)
	if ref != nil {
		_ = io.printJSON(ref)
		return errExit
	}
	return io.printJSON(res)
}
