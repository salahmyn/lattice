package cli

// @module-feature breaktest

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/breaktest"
	"github.com/salahmyn/lattice/pkg/lattice/config"
)

func newBreakCommand(io *IO) *cobra.Command {
	var promise, feature string
	var timeout time.Duration
	var allowDirty bool
	cmd := &cobra.Command{
		Use:   "break",
		Short: "Break each promise's enforcer and see whether its verifiers notice",
		Long: "For each promise (invariant) with an enforcer, break the enforcer on a temporary\n" +
			"git worktree at HEAD, run only that promise's verifiers, and record whether they\n" +
			"notice. A verifier that passes before and after the break cannot tell the code is\n" +
			"broken (survived); one that passes before and fails after is killed; anything\n" +
			"else (no generator for the language, a break that does not compile, a failing\n" +
			"baseline, a timeout, no runner configured) is unknown, which is never a pass.\n" +
			"\n" +
			"The generated break returns early at the top of the enforcer's body (Go,\n" +
			"TypeScript/JavaScript, Python). A patch at lattice/breaks/<feature>/<INV>.patch\n" +
			"replaces it for that promise (use one for SQL). Go verifiers run with `go test\n" +
			"-run`; other languages need break.commands.<lang> in config.yaml, a command with\n" +
			"{files} and {filter} placeholders.\n" +
			"\n" +
			"The working tree is never edited. A tree with uncommitted changes is refused\n" +
			"unless --allow-dirty, which tests the tree as it stands and records dirty: true.\n" +
			"The result is written to lattice/.cache/results/breaks.json.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			kg, ws, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			cfg, err := config.Load(ws.LatticeDir)
			if err != nil {
				return io.fail("BAD_CONFIG", err.Error(), nil)
			}

			// An interrupt stops the run; the worktree is removed on the way out.
			ctx, stop := signal.NotifyContext(commandContext(cmd), os.Interrupt)
			defer stop()

			rep, err := breaktest.Execute(ctx, kg, breaktest.Options{
				Promise:    promise,
				Feature:    feature,
				Timeout:    timeout,
				AllowDirty: allowDirty,
				Config:     cfg,
				LatticeDir: ws.LatticeDir,
				CodeRoot:   ws.PrimaryCodeRoot().Abs,
			})
			switch {
			case errors.Is(err, breaktest.ErrDirty):
				return io.fail("DIRTY_TREE", err.Error(), nil)
			case err != nil:
				return io.fail("BREAK_FAILED", err.Error(), nil)
			}
			if err := breaktest.Write(ws.LatticeDir, rep); err != nil {
				return io.fail("WRITE_FAILED", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(rep)
			}
			printBreakTable(io, rep)
			return nil
		},
	}
	cmd.Flags().StringVar(&promise, "promise", "", "break one promise, as <feature>:INV-n")
	cmd.Flags().StringVar(&feature, "feature", "", "break the promises of one feature")
	cmd.Flags().DurationVar(&timeout, "timeout", 120*time.Second, "timeout for each test run (a timeout reads unknown)")
	cmd.Flags().BoolVar(&allowDirty, "allow-dirty", false, "test a working tree with uncommitted changes")
	return cmd
}

func commandContext(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}

func printBreakTable(io *IO, rep breaktest.Report) {
	if len(rep.Runs) == 0 {
		io.printf("no promise has an enforcer to break\n")
		return
	}
	io.printf("%-26s %-34s %-12s %-9s %s\n", "PROMISE", "ENFORCER", "METHOD", "OUTCOME", "REASON")
	counts := map[string]int{}
	for _, r := range rep.Runs {
		counts[r.Outcome]++
		reason := r.Reason
		if r.Cached {
			reason = "(cached) " + reason
		}
		io.printf("%-26s %-34s %-12s %-9s %s\n",
			truncate(r.Promise, 26), truncate(r.Enforcer, 34), r.Method, r.Outcome, reason)
	}
	io.printf("\n%d killed, %d survived, %d unknown", counts[breaktest.Killed], counts[breaktest.Survived], counts[breaktest.Unknown])
	if rep.Dirty {
		io.printf("  (dirty tree)")
	}
	io.printf("\n")
}
