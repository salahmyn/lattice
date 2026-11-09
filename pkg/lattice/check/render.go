package check

// @module-feature check

import (
	"fmt"
	"io"
	"strings"
)

// Marker starts the Markdown form so a CI job can find and update its own
// pull-request comment.
const Marker = "<!-- lattice-check -->"

// badge is the verdict's mark. UNPROVEN is never drawn as a pass: it has no
// check mark, and the text says it is not one.
// @enforces check:INV-2
func badge(state string) string {
	switch state {
	case Assured:
		return "✅ ASSURED"
	case Broken:
		return "❌ BROKEN"
	}
	return "⚠️ UNPROVEN"
}

func mark(state string) string {
	switch state {
	case Pass:
		return "pass"
	case Fail:
		return "FAIL"
	}
	return "unknown"
}

func shortRef(r Ref) string {
	s := short(r.SHA)
	if r.Dirty {
		s += "+uncommitted"
	}
	return s
}

// WriteText prints the report for a terminal.
func WriteText(w io.Writer, r Report) {
	fmt.Fprintf(w, "lattice check  %s..%s%s\n\n", short(r.Base.SHA), shortRef(r.Head), ciNote(r))
	fmt.Fprintf(w, "%s: %s\n", r.Verdict.State, r.Verdict.Text)
	fmt.Fprintf(w, "merge: %s (%s)\n", yesNo(r.MergeAllowed), r.Merge.Text)
	if r.Verdict.State == Unproven {
		fmt.Fprintf(w, "UNPROVEN is not a pass.\n")
	}
	fmt.Fprintln(w)
	writeEvidenceText(w, r.Evidence)

	if len(r.Weakenings) > 0 {
		fmt.Fprintln(w, "\nWHAT FELL")
		for _, k := range r.Weakenings {
			fmt.Fprintf(w, "  %-20s %s\n", k.Code, k.Text)
			if k.Next != "" {
				fmt.Fprintf(w, "  %-20s next: %s\n", "", k.Next)
			}
		}
	}

	if len(r.Promises) > 0 {
		fmt.Fprintln(w, "\nTOUCHED PROMISES")
		for _, p := range r.Promises {
			fmt.Fprintf(w, "  %s  tier %d  %s  [%s]\n", p.Promise, p.Tier, p.Class, strings.Join(p.Touched, ", "))
			for _, name := range signalOrder[:6] {
				a := p.Signals.get(name)
				fmt.Fprintf(w, "      %-13s %-8s %-11s %s\n", name, mark(a.State), a.Honesty, oneLine(a.Text))
			}
			fmt.Fprintf(w, "      next: %s\n", p.NextAction)
		}
	}

	if open := openGaps(r.Gaps); len(open) > 0 {
		fmt.Fprintln(w, "\nGAPS (promise / signal, against base)")
		for _, g := range open {
			extra := ""
			if g.Waived {
				extra = "  waived (still counts)"
			}
			blocks := ""
			if g.Blocks {
				blocks = "  BLOCKS MERGE"
			}
			fmt.Fprintf(w, "  %-34s %-8s %-8s%s%s\n", g.Promise+" / "+g.Signal, mark(g.State), g.Delta, blocks, extra)
		}
	}

	fmt.Fprintf(w, "\nBLIND SPOT  %s\n", r.BlindSpot.Text)
	for _, u := range r.BlindSpot.Untraced {
		n := ""
		if u.New {
			n = "  (new)"
		}
		fmt.Fprintf(w, "  %s:%s  %s%s\n      %s\n", u.File, u.Lines, u.Symbol, n, u.Reason)
	}
	for _, o := range r.BlindSpot.OutOfReach {
		fmt.Fprintf(w, "  out of reach: %s  %s\n", o.Promise, o.Reason)
	}
	for _, wv := range r.Waivers {
		fmt.Fprintf(w, "WAIVER  %s  %s  (shown, never counted as proof)\n", wv.Subject, wv.Kind)
	}
}

func ciNote(r Report) string {
	if r.CI {
		return "  (--ci: only evidence tied to head and a CI run counts)"
	}
	return "  (local run: advice, not CI's verdict)"
}

func yesNo(b bool) string {
	if b {
		return "allowed"
	}
	return "NOT allowed"
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 100 {
		s = s[:99] + "…"
	}
	return s
}

func openGaps(gaps []Gap) []Gap {
	var out []Gap
	for _, g := range gaps {
		if g.State != Pass {
			out = append(out, g)
		}
	}
	return out
}

func writeEvidenceText(w io.Writer, e Evidence) {
	fmt.Fprintln(w, "EVIDENCE")
	for _, it := range []struct {
		name string
		e    EvidenceItem
	}{{"results", e.Results}, {"coverage", e.Coverage}, {"breaks", e.Breaks}} {
		switch {
		case !it.e.Present:
			fmt.Fprintf(w, "  %-9s none (reads unknown)\n", it.name)
		case !it.e.Counted:
			fmt.Fprintf(w, "  %-9s set aside: %s [%s]\n", it.name, it.e.Text, it.e.Code)
		default:
			fmt.Fprintf(w, "  %-9s counted (commit %s, ci_run %s, tied to head: %s)\n", it.name, short(it.e.Commit), orNone(it.e.CIRun), yesNo2(it.e.Tied))
		}
	}
	if e.BaseAudit.Present {
		fmt.Fprintf(w, "  base audit  found (%s)\n", short(e.BaseAudit.Commit))
	} else {
		fmt.Fprintf(w, "  base audit  none: evidence is not compared with base (%s)\n", e.BaseAudit.Text)
	}
}

func yesNo2(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// WriteMarkdown prints the report as a pull-request comment.
func WriteMarkdown(w io.Writer, r Report) {
	fmt.Fprintln(w, Marker)
	fmt.Fprintf(w, "## Lattice check: %s\n\n", badge(r.Verdict.State))
	fmt.Fprintf(w, "%s\n\n", r.Verdict.Text)
	fmt.Fprintf(w, "- **Change:** `%s..%s`%s\n", short(r.Base.SHA), shortRef(r.Head), ciNote(r))
	fmt.Fprintf(w, "- **Merge rule:** %s. %s\n", yesNo(r.MergeAllowed), r.Merge.Text)
	if r.Verdict.State == Unproven {
		fmt.Fprintln(w, "- **UNPROVEN is not a pass.** Nothing fell, and something is not shown. Read the gaps below.")
	}
	if !r.Evidence.BaseAudit.Present {
		fmt.Fprintf(w, "- **No base audit:** evidence is not compared with base (%s).\n", r.Evidence.BaseAudit.Text)
	}
	for _, it := range []struct {
		name string
		e    EvidenceItem
	}{{"results", r.Evidence.Results}, {"coverage", r.Evidence.Coverage}, {"breaks", r.Evidence.Breaks}} {
		if it.e.Present && !it.e.Counted {
			fmt.Fprintf(w, "- **Evidence set aside (%s):** %s `%s`\n", it.name, it.e.Text, it.e.Code)
		}
	}
	fmt.Fprintln(w)

	if len(r.Weakenings) > 0 {
		fmt.Fprintln(w, "### What fell")
		fmt.Fprintln(w)
		for _, k := range r.Weakenings {
			fmt.Fprintf(w, "- `%s` %s", k.Code, k.Text)
			if k.Next != "" {
				fmt.Fprintf(w, " Next: %s.", strings.TrimSuffix(k.Next, "."))
			}
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w)
	}

	if open := openGaps(r.Gaps); len(open) > 0 {
		fmt.Fprintln(w, "### Gaps")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "| Promise | Signal | State | Label | Against base | Next |")
		fmt.Fprintln(w, "|---|---|---|---|---|---|")
		for _, g := range open {
			delta := g.Delta
			if g.Blocks {
				delta += " (blocks merge)"
			}
			if g.Waived {
				delta += ", waived (still counts)"
			}
			fmt.Fprintf(w, "| `%s` | %s | %s | %s | %s | %s |\n", g.Promise, g.Signal, mark(g.State), g.Honesty, delta, cell(g.NextAction))
		}
		fmt.Fprintln(w)
	}

	if len(r.Promises) > 0 {
		fmt.Fprintln(w, "<details><summary>Touched promises (signals)</summary>")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "| Promise | Tier | traced | confirmed | live | exercised | break_tested | nothing_fell | Next |")
		fmt.Fprintln(w, "|---|---|---|---|---|---|---|---|---|")
		for _, p := range r.Promises {
			s := p.Signals
			fmt.Fprintf(w, "| `%s` | %d | %s | %s | %s | %s | %s | %s | %s |\n", p.Promise, p.Tier,
				mark(s.Traced.State), mark(s.Confirmed.State), mark(s.Live.State), mark(s.Exercised.State),
				mark(s.BreakTested.State), mark(s.NothingFell.State), cell(p.NextAction))
		}
		fmt.Fprintln(w)
		fmt.Fprintln(w, "</details>")
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "### Blind spot\n\n%s\n", r.BlindSpot.Text)
	for _, u := range r.BlindSpot.Untraced {
		n := ""
		if u.New {
			n = " (new)"
		}
		fmt.Fprintf(w, "- `%s:%s` `%s`%s: %s\n", u.File, u.Lines, u.Symbol, n, u.Reason)
	}
	for _, o := range r.BlindSpot.OutOfReach {
		fmt.Fprintf(w, "- `%s` is marked out of reach: %s\n", o.Promise, o.Reason)
	}
	if len(r.Waivers) > 0 {
		fmt.Fprintln(w, "\nWaivers are shown and never counted as proof:")
		for _, wv := range r.Waivers {
			fmt.Fprintf(w, "- `%s` (%s)\n", wv.Subject, wv.Kind)
		}
	}
	fmt.Fprintf(w, "\n<sub>Only CI's verdict counts. Unknown is never a pass.</sub>\n")
}

func cell(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}
