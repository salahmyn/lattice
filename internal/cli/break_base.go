package cli

// @module-feature breaktest

import (
	"context"
	"errors"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/all"
	"github.com/salahmyn/lattice/pkg/lattice/breaktest"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/impact"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// runFailsOnBase is `lattice break --fails-on-base <rev>`: the change comes
// from impact, the runs from breaktest.
func runFailsOnBase(ctx context.Context, io *IO, kg schema.KnowledgeGraph, ws *workspace.Workspace, rev string, opts breaktest.Options) error {
	adCfg, err := config.LoadAdapters(ws.LatticeDir)
	if err != nil {
		return io.fail("BAD_CONFIG", err.Error(), nil)
	}
	im, err := impact.Compute(ctx, impact.Input{Workspace: ws, Registry: all.Registry(adCfg), Graph: kg, Since: rev})
	if err != nil {
		return io.fail("BAD_BASE", err.Error(), nil)
	}
	rep, err := breaktest.RunFailsOnBase(ctx, kg, opts, changeOf(rev, im))
	switch {
	case errors.Is(err, breaktest.ErrDirty):
		return io.fail("DIRTY_TREE", err.Error(), nil)
	case err != nil:
		return io.fail("BREAK_FAILED", err.Error(), nil)
	}
	if err := breaktest.WriteFailsOnBase(ws.LatticeDir, rep); err != nil {
		return io.fail("WRITE_FAILED", err.Error(), nil)
	}
	if io.JSON {
		return io.printJSON(rep)
	}
	var shown []breaktest.BaseRun
	for _, v := range rep.Verifiers {
		for _, p := range v.Promises {
			if opts.Selects(p) {
				shown = append(shown, v)
				break
			}
		}
	}
	rep.Verifiers = shown
	printFailsOnBaseTable(io, rep)
	return nil
}

// changeOf reads what a fails-on-base run needs out of an impact report.
func changeOf(rev string, im impact.Report) breaktest.Change {
	ch := breaktest.Change{Rev: rev, Base: im.Base, Tests: map[string]string{}}
	for _, cs := range im.ChangedSymbols {
		if cs.Test && (cs.Change == impact.ChangeAdded || cs.Change == impact.ChangeModified) {
			ch.Tests[cs.Symbol] = cs.Change
		}
	}
	for _, f := range im.ChangedFiles {
		if f.NewPath != "" {
			ch.Files = append(ch.Files, f.NewPath)
		}
		if f.OldPath != "" && f.OldPath != f.NewPath && f.Status != "C" {
			ch.Files = append(ch.Files, f.OldPath) // deleted or renamed away
		}
	}
	return ch
}

func printFailsOnBaseTable(io *IO, rep breaktest.FailsOnBaseReport) {
	if len(rep.Verifiers) == 0 {
		io.printf("no verifier of a touched promise is new or changed since %s\n", short(rep.Base))
		return
	}
	io.printf("fails on base  %s..%s\n\n", short(rep.Base), short(rep.Head))
	io.printf("%-40s %-24s %-5s %-15s %s\n", "VERIFIER", "PROMISES", "HEAD", "OUTCOME", "REASON")
	counts := map[string]int{}
	for _, v := range rep.Verifiers {
		counts[v.Outcome]++
		head := v.Head
		if head == "" {
			head = "-"
		}
		io.printf("%-40s %-24s %-5s %-15s %s\n", truncate(v.Verifier, 40), truncate(strings.Join(v.Promises, ","), 24),
			head, v.Outcome, v.Reason)
	}
	io.printf("\n%d fail on base, %d pass on base, %d unknown",
		counts[breaktest.FailsOnBase], counts[breaktest.PassesOnBase], counts[breaktest.Unknown])
	if rep.Dirty {
		io.printf("  (dirty tree)")
	}
	io.printf("\n")
}

func short(sha string) string {
	if len(sha) > 10 {
		return sha[:10]
	}
	return sha
}
