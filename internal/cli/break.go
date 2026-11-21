package cli

// @module-feature breaktest

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/internal/preflight"
	"github.com/salahmyn/lattice/pkg/lattice/breaktest"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func newBreakCommand(io *IO) *cobra.Command {
	var promise, feature, failsOnBase string
	var failOn []string
	var timeout time.Duration
	var allowDirty, noPreflight bool
	cmd := &cobra.Command{
		Use:   "break",
		Short: "Break each promise's enforcer and see whether its verifiers notice",
		Long: "For each promise (invariant) with an enforcer, break the enforcer on a temporary\n" +
			"git worktree at HEAD, run only that promise's verifiers, and record whether they\n" +
			"notice. A verifier that passes before and after the break cannot tell the code is\n" +
			"broken (survived, when every verifier ran); one that passes before and fails\n" +
			"after is killed; anything else (no generator for the language, a break that\n" +
			"does not compile, a failing baseline, a timeout, no runner configured, or a\n" +
			"verifier that did not run) is unknown, which is never a pass.\n" +
			"\n" +
			"The generated break returns early at the top of the enforcer's body (Go,\n" +
			"TypeScript/JavaScript, Python). A patch at lattice/breaks/<feature>/<INV>.patch\n" +
			"replaces it for that promise (use one for SQL). Go verifiers run with `go test\n" +
			"-run`; other frameworks need break.runners in config.yaml (a glob over the test\n" +
			"files and a command with {files}, {filter} and {junit}; a SQL break also has\n" +
			"{patched_sql} and {original_sql}), or break.commands.<lang>\n" +
			"as a fallback. break.setup runs commands in the worktree first, and node_modules\n" +
			"directories are linked from the main checkout unless break.link_node_modules is\n" +
			"false. A verifier that did not run is listed in not_run_verifiers.\n" +
			"\n" +
			"--promise <feature>:INV-n#<case> breaks one acceptance case: its own enforcer\n" +
			"(else each enforcer of the invariant) against only the tests tagged\n" +
			"@verifies <feature>:INV-n#<case>. The runs are recorded under the case ref, so\n" +
			"they count for the case's coverage and never for the promise's break signal.\n" +
			"\n" +
			"The working tree is never edited. A tree with uncommitted changes is refused\n" +
			"unless --allow-dirty, which tests the tree as it stands and records dirty: true.\n" +
			"The result is written to lattice/.cache/results/breaks.json.\n" +
			"\n" +
			"--fails-on-base <rev> proves something else: that a new test fails on the old\n" +
			"code. It takes the verifiers that the change from merge-base(<rev>, HEAD) to the\n" +
			"working tree adds or edits (as lattice impact finds them), runs each at head,\n" +
			"then runs it on the base commit's production code with the head's test files\n" +
			"copied on top (the verifier files, the changed test files of their directories\n" +
			"and their changed testdata/), with the same runners. fails_on_base: the test\n" +
			"detects the old behaviour. passes_on_base: it does not tell old from new.\n" +
			"unknown: it does not pass at head, does not compile on base (an API it calls is\n" +
			"new), has no runner, timed out or did not run; unknown is never a pass. The\n" +
			"result is written to lattice/.cache/results/fails-on-base.json, and lattice\n" +
			"check --base adds a note (never a gap) for a verifier that passes on base.\n" +
			"\n" +
			"A preflight runs first, before the graph is built, and takes seconds: the\n" +
			"executables of break.setup, break.runners and break.commands are on PATH,\n" +
			"each runner's dir exists, a Node minimum declared in package.json\n" +
			"engines.node is met, the Docker daemon answers for a command that runs\n" +
			"docker, each break.services address answers on TCP, and the tree is clean\n" +
			"(unless --allow-dirty). It reports every problem at once (code, config\n" +
			"key, fix) and exits 2 before anything runs. --no-preflight skips it.\n" +
			"\n" +
			"Exit codes: 0 once the report is written, whatever the outcomes; 1 with\n" +
			"--fail-on survived,unknown (either or both) when a shown run (the --promise or\n" +
			"--feature selection) has a listed outcome (BREAK_OUTCOME); 2 for a usage or\n" +
			"setup error. With --fails-on-base only --fail-on unknown applies. When no\n" +
			"shown run was killed or survived, the output ends with a \"no break ran\" line\n" +
			"naming the top reasons; --json adds summary (counts and ran) for the shown runs.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			want, err := parseFailOn(failOn, failsOnBase != "")
			if err != nil {
				return usageError(io, "BAD_FLAG", err.Error())
			}
			ws, err := openWorkspace(io)
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

			if !noPreflight {
				var dirty []preflight.Problem
				if !allowDirty {
					dirty = dirtyProblem(ctx, ws)
				}
				reqs := preflight.Break(cfg.Break, filepath.Dir(ws.LatticeDir))
				if err := runPreflight(ctx, io, "break", reqs, dirty...); err != nil {
					return err
				}
			}
			kg, err := buildGraph(ctx, ws, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			if _, _, isCase := schema.SplitCaseRef(promise); isCase {
				if _, ok := breaktest.FindCase(kg, promise); !ok {
					return io.fail("CASE_NOT_FOUND", "no acceptance case "+promise+" (use <feature>:INV-n#<case>)", nil)
				}
			}

			opts := breaktest.Options{
				Promise:    promise,
				Feature:    feature,
				Timeout:    timeout,
				AllowDirty: allowDirty,
				Config:     cfg,
				LatticeDir: ws.LatticeDir,
				CodeRoot:   ws.PrimaryCodeRoot().Abs,
			}
			if failsOnBase != "" {
				return runFailsOnBase(ctx, io, kg, ws, failsOnBase, opts, want)
			}
			rep, err := breaktest.Execute(ctx, kg, opts)
			switch {
			case errors.Is(err, breaktest.ErrDirty):
				return io.fail("DIRTY_TREE", err.Error(), nil)
			case err != nil:
				return io.fail("BREAK_FAILED", err.Error(), nil)
			}
			if err := breaktest.Write(ws.LatticeDir, rep); err != nil {
				return io.fail("WRITE_FAILED", err.Error(), nil)
			}
			// The file keeps every run of the commit; the table, the summary
			// and --fail-on see the ones this invocation asked for.
			var shown []breaktest.Run
			for _, r := range rep.Runs {
				if opts.Selects(r.Promise) {
					shown = append(shown, r)
				}
			}
			sum := summarize(shown)
			sum.Baselines = rep.Baselines
			failMsg := failOnMessage(want, map[string]int{breaktest.Survived: sum.Survived, breaktest.Unknown: sum.Unknown})
			if io.JSON {
				out := breakOutput{Report: rep, Summary: sum}
				if failMsg != "" {
					out.Error, out.Code = failMsg, failOnCode
				}
				if err := io.printJSON(out); err != nil {
					return err
				}
				if failMsg != "" {
					return errExit
				}
				return nil
			}
			rep.Runs = shown
			printBreakTable(io, rep, sum)
			if failMsg != "" {
				return io.fail(failOnCode, failMsg, nil)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&promise, "promise", "", "break one promise, as <feature>:INV-n, or one case, as <feature>:INV-n#<case>")
	cmd.Flags().StringVar(&feature, "feature", "", "break the promises of one feature")
	cmd.Flags().DurationVar(&timeout, "timeout", 120*time.Second, "timeout for each test run (a timeout reads unknown)")
	cmd.Flags().BoolVar(&allowDirty, "allow-dirty", false, "test a working tree with uncommitted changes")
	cmd.Flags().BoolVar(&noPreflight, "no-preflight", false, noPreflightUsage)
	cmd.Flags().StringSliceVar(&failOn, "fail-on", nil, "exit 1 when a shown run has one of these outcomes: survived, unknown (repeatable, or a comma list)")
	cmd.Flags().StringVar(&failsOnBase, "fails-on-base", "", "run the new and changed verifiers on the code of merge-base(<rev>, HEAD) instead of breaking")
	return cmd
}

func commandContext(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}

func printBreakTable(io *IO, rep breaktest.Report, sum breakSummary) {
	if len(rep.Runs) == 0 {
		io.printf("no promise has an enforcer to break\n")
		return
	}
	io.printf("%-26s %-34s %-12s %-9s %s\n", "PROMISE", "ENFORCER", "METHOD", "OUTCOME", "REASON")
	for _, r := range rep.Runs {
		reason := r.Reason
		if r.Cached {
			reason = "(cached) " + reason
		}
		io.printf("%-26s %-34s %-12s %-9s %s\n",
			truncate(r.Promise, 26), truncate(r.Enforcer, 34), r.Method, r.Outcome, reason)
	}
	io.printf("\n%d killed, %d survived, %d unknown", sum.Killed, sum.Survived, sum.Unknown)
	if b := sum.Baselines; b.Run+b.Reused > 0 {
		io.printf("; baselines: %d run, %d reused", b.Run, b.Reused)
	}
	if rep.Dirty {
		io.printf("  (dirty tree)")
	}
	io.printf("\n")
	if line := nothingRanLine(sum); line != "" {
		io.printf("%s\n", line)
	}
}
