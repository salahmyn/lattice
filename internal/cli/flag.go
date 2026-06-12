package cli

import (
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/flags"
	"github.com/salahmyn/lattice/pkg/lattice/ledger"
)

// newFlagCommand manages open meaning flags (v0.8.1). A flag marks a
// unit whose meaning is in question — a suspected criterion↔invariant
// mismatch, or a demotion from an approved CR. Flags ride alongside the
// computed RTM status and are never hidden behind a green row. Anyone
// may raise a flag; only a human clears one.
func newFlagCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "flag",
		Short: "Raise, list, and (humans only) clear meaning flags on units",
	}
	cmd.AddCommand(newFlagRaiseCommand(io), newFlagListCommand(io), newFlagClearCommand(io))
	return cmd
}

func newFlagRaiseCommand(io *IO) *cobra.Command {
	var reason, source string
	cmd := &cobra.Command{
		Use:   "raise <unit>",
		Short: "Raise a meaning flag on a unit (brd.x.y/SC-1, brd.x.y/US-2)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("WORKSPACE_FAILED", err.Error(), nil)
			}
			by := io.actor()
			if by == "" {
				by = "unattributed"
			}
			f, err := flags.Raise(ws.LatticeDir, args[0], reason, by, source, time.Now())
			if err != nil {
				return io.fail("FLAG_FAILED", err.Error(), nil)
			}
			appendLedgerEvent(io, ws, ledger.EventFlag, args[0], "raised: "+reason)
			if io.JSON {
				return io.printJSON(f)
			}
			io.printf("flagged %s — %s\n", f.Unit, f.Reason)
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "one line: why the meaning is in question (required)")
	cmd.Flags().StringVar(&source, "source", "manual", "origin: manual, cr:CR-<n>, or a rule code")
	_ = cmd.MarkFlagRequired("reason")
	return cmd
}

func newFlagListCommand(io *IO) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List open meaning flags (--all includes cleared history)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("WORKSPACE_FAILED", err.Error(), nil)
			}
			fl := flags.Load(ws.LatticeDir)
			if !all {
				open := fl[:0]
				for _, f := range fl {
					if f.Open() {
						open = append(open, f)
					}
				}
				fl = open
			}
			if io.JSON {
				return io.printJSON(fl)
			}
			if len(fl) == 0 {
				io.printf("no open flags\n")
				return nil
			}
			io.printf("%-34s %-22s %-14s %s\n", "unit", "by", "source", "reason")
			io.printf("%s\n", strRepeat("-", 100))
			for _, f := range fl {
				state := ""
				if !f.Open() {
					state = " (cleared by " + f.ClearedBy + ")"
				}
				io.printf("%-34s %-22s %-14s %s%s\n",
					truncate(f.Unit, 34), truncate(f.By, 22), truncate(f.Source, 14), f.Reason, state)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include cleared flags")
	return cmd
}

func newFlagClearCommand(io *IO) *cobra.Command {
	var by string
	cmd := &cobra.Command{
		Use:   "clear <unit>",
		Short: "Clear open flags on a unit — a human act, the meaning gate",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("WORKSPACE_FAILED", err.Error(), nil)
			}
			// Clearing is the human meaning gate. An agent actor naming
			// itself here is exactly the self-approval the flag exists to
			// prevent.
			if strings.HasPrefix(strings.ToLower(by), "agent") {
				return io.fail("FLAG_CLEAR_HUMAN_ONLY",
					"flags are cleared by humans — pass --by <human>, not an agent actor", nil)
			}
			n, err := flags.Clear(ws.LatticeDir, args[0], by, time.Now())
			if err != nil {
				return io.fail("FLAG_FAILED", err.Error(), nil)
			}
			appendLedgerEvent(io, ws, ledger.EventFlag, args[0], "cleared by "+by)
			if io.JSON {
				return io.printJSON(map[string]interface{}{"unit": args[0], "cleared": n, "by": by})
			}
			io.printf("cleared %d flag(s) on %s\n", n, args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&by, "by", "", "the human clearing the flag, e.g. sal@example.com (required)")
	_ = cmd.MarkFlagRequired("by")
	return cmd
}
