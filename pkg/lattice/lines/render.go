package lines

// @module-feature audit

import (
	"fmt"
	"io"
	"strings"
)

// percent says the verified share of a set of lines, always with the count
// of lines that have no data beside it. With no known line there is nothing
// to divide, and it says so.
func percent(t Totals) string {
	known, unknown := t.WithIntent.Known(), t.WithIntent.Unknown
	if t.Verified == nil {
		return fmt.Sprintf("no line data (%d unknown)", unknown)
	}
	return fmt.Sprintf("%.1f%% (%d of %d known lines, %d unknown)", *t.Verified, t.WithIntent.Verified(), known, unknown)
}

func intentText(i Intent) string {
	if i.Kind == IntentNone {
		return "none"
	}
	var refs []string
	for _, r := range i.Invariants {
		refs = append(refs, r.Invariant+" ("+r.Required+")")
	}
	return i.Kind + " " + strings.Join(refs, ", ")
}

func levelsText(l Levels) string {
	return fmt.Sprintf("checked %d, run %d, run_shared %d, other %d, not_run %d, unknown %d",
		l.Checked, l.Run, l.RunShared, l.Other, l.NotRun, l.Unknown)
}

func dash(s string) string { return strings.ReplaceAll(s, "-", "–") }

// WriteText prints the lines section: the workspace, then each feature with
// its blocks, by file and line.
func WriteText(w io.Writer, r Report) {
	fmt.Fprintf(w, "lines: what each block is for, and how far its verifiers reach into it\n")
	fmt.Fprintf(w, "  verifier lines: %s\n  suite coverage: %s\n  breaks:         %s\n  call graph:     %s\n\n",
		r.Inputs.Verifier, r.Inputs.Suite, r.Inputs.Breaks, r.Inputs.Graph)

	fmt.Fprintf(w, "workspace  verified lines %s\n", percent(r.Workspace))
	writeNoIntent(w, "  ", r.Workspace.NoIntent)
	if !r.Narrowed {
		fmt.Fprintf(w, "  outside any block: %d instrumented lines (%d run by something)\n", r.Outside.Lines, r.Outside.Hit)
	}

	for _, f := range r.Features {
		name := f.Feature
		if name == "" {
			name = "(no feature)"
		}
		fmt.Fprintf(w, "\nfeature %s  verified lines %s\n", name, percent(f.Totals))
		writeNoIntent(w, "  ", f.NoIntent)
		for _, b := range r.Blocks {
			if b.Feature != f.Feature {
				continue
			}
			fmt.Fprintf(w, "  %s:%d  %s  %s\n", b.File, b.Line, b.FQN, intentText(b.Intent))
			fmt.Fprintf(w, "      %s\n", levelsText(b.Levels))
			for _, rg := range []struct{ name, v string }{{"not_run", b.NotRun}, {"other", b.Other}, {"run_shared", b.RunShared}} {
				if rg.v != "" {
					fmt.Fprintf(w, "      %s: %s\n", rg.name, dash(rg.v))
				}
			}
			if b.Whole != "" {
				fmt.Fprintf(w, "      %s\n", b.Whole)
			}
			if b.Unknown != "" {
				fmt.Fprintf(w, "      unknown: %s\n", b.Unknown)
			}
			if b.Note != "" {
				fmt.Fprintf(w, "      %s\n", b.Note)
			}
		}
	}
}

func writeNoIntent(w io.Writer, indent string, l Levels) {
	if l.Known()+l.Unknown == 0 {
		return
	}
	fmt.Fprintf(w, "%sblocks with no intent: other %d, not_run %d, unknown %d\n", indent, l.Other, l.NotRun, l.Unknown)
}

// WriteMarkdown prints the same as a table per feature, for a PR comment.
func WriteMarkdown(w io.Writer, r Report) {
	fmt.Fprintf(w, "## Lines\n\n")
	fmt.Fprintf(w, "Verified lines: **%s**.", percent(r.Workspace))
	if !r.Narrowed {
		fmt.Fprintf(w, " Outside any block: %d instrumented lines.", r.Outside.Lines)
	}
	fmt.Fprintf(w, "\n")
	for _, f := range r.Features {
		name := f.Feature
		if name == "" {
			name = "(no feature)"
		}
		fmt.Fprintf(w, "\n### %s: %s\n\n", name, percent(f.Totals))
		fmt.Fprintf(w, "| Block | Intent | Levels | not_run | other | run_shared |\n|---|---|---|---|---|---|\n")
		for _, b := range r.Blocks {
			if b.Feature != f.Feature {
				continue
			}
			lv := levelsText(b.Levels)
			if b.Whole != "" {
				lv += " (" + b.Whole + ")"
			}
			if b.Unknown != "" {
				lv += " (" + b.Unknown + ")"
			}
			fmt.Fprintf(w, "| `%s:%d` %s | %s | %s | %s | %s | %s |\n", b.File, b.Line, b.FQN, intentText(b.Intent), lv,
				dash(b.NotRun), dash(b.Other), dash(b.RunShared))
		}
		if l := f.NoIntent; l.Known()+l.Unknown > 0 {
			fmt.Fprintf(w, "\nBlocks with no intent: other %d, not_run %d, unknown %d.\n", l.Other, l.NotRun, l.Unknown)
		}
	}
}
