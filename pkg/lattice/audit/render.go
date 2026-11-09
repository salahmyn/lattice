package audit

// @module-feature audit

import (
	"fmt"
	"io"
	"strings"
)

// Filter narrows a report's rows; empty fields match everything. The summary
// is recomputed over the kept rows.
type Filter struct {
	Class   string
	Feature string
}

// Apply returns a copy of the report with only the matching rows.
func (f Filter) Apply(r Report) Report {
	if f.Class == "" && f.Feature == "" {
		return r
	}
	var rows []Row
	for _, row := range r.Rows {
		if f.Class != "" && row.Class != f.Class {
			continue
		}
		if f.Feature != "" && row.Feature != f.Feature {
			continue
		}
		rows = append(rows, row)
	}
	r.Rows = rows
	r.Summary = summarize(rows)
	return r
}

func (r Report) head() string {
	h := short(r.Commit)
	if r.Dirty {
		h += " (dirty)"
	}
	return h
}

// InputLines says which evidence the audit read, one line each.
func (r Report) InputLines() []string { return r.inputLines() }

func (r Report) inputLines() []string {
	in := r.Inputs
	var out []string
	switch {
	case !in.Results.Present:
		out = append(out, "results:  none ingested (lattice results ingest)")
	case !in.Results.MatchesHead:
		out = append(out, "results:  commit "+short(in.Results.Commit)+" (not head)")
	default:
		out = append(out, "results:  ingested, commit "+short(in.Results.Commit))
	}
	switch {
	case !in.Coverage.Present:
		out = append(out, "coverage: none ingested")
	default:
		line := fmt.Sprintf("coverage: %d per-test, commit %s", in.Coverage.PerTest, short(in.Coverage.Commit))
		if !in.Coverage.MatchesHead {
			line += " (not head)"
		}
		out = append(out, line)
	}
	if in.Reach.Present {
		out = append(out, "reach:    classified")
	} else {
		out = append(out, "reach:    no symbol classified")
	}
	switch {
	case in.Breaks.Ignored != "":
		out = append(out, "breaks:   ignored: "+in.Breaks.Ignored)
	case in.Breaks.Present:
		out = append(out, fmt.Sprintf("breaks:   %d runs, commit %s", in.Breaks.Runs, short(in.Breaks.Commit)))
	default:
		out = append(out, "breaks:   none (lattice break)")
	}
	if in.Notes.Present {
		out = append(out, fmt.Sprintf("notes:    %d second-reader notes", in.Notes.Count))
	}
	for _, e := range in.Notes.Errors {
		out = append(out, "notes:    "+e)
	}
	return out
}

// WriteText prints the summary by class, then one row per promise.
func WriteText(w io.Writer, r Report) {
	fmt.Fprintf(w, "lattice audit @ %s: %d promises\n\n", r.head(), r.Summary.Total)
	for _, l := range r.inputLines() {
		fmt.Fprintf(w, "  %s\n", l)
	}
	fmt.Fprintf(w, "\nBy class (only break_tested and exercised are positive; unknown is never a pass):\n")
	for _, c := range Classes {
		if n := r.Summary.ByClass[c]; n > 0 {
			fmt.Fprintf(w, "  %-18s %d\n", c, n)
		}
	}
	if r.Summary.Total == 0 {
		fmt.Fprintf(w, "  (no invariants)\n")
		return
	}
	fmt.Fprintf(w, "\n%-18s %-26s %-20s %-9s %s\n", "class", "promise", "kind", "confirmed", "reason")
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 110))
	for _, row := range r.Rows {
		fmt.Fprintf(w, "%-18s %-26s %-20s %-9s %s\n", row.Class, trunc(row.Promise, 26), row.Kind,
			row.Signals.Confirmed.Value, row.Reason)
		for _, n := range row.Notes {
			fmt.Fprintf(w, "%-18s second reader %s: %s %s\n", "", n.Reader, n.Verdict, n.Note)
		}
	}
	fmt.Fprintf(w, "\nconfirmed is a fact about the approval record, not proof the intent is right.\n")
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// WriteMarkdown prints the report for a PR comment or a written report.
func WriteMarkdown(w io.Writer, r Report) {
	fmt.Fprintf(w, "## Lattice audit\n\n")
	fmt.Fprintf(w, "Commit `%s`, %d promises. Only `break_tested` and `exercised` are positive; `unknown` is never a pass.\n\n", r.head(), r.Summary.Total)

	fmt.Fprintf(w, "| Class | Promises |\n|---|---|\n")
	for _, c := range Classes {
		if n := r.Summary.ByClass[c]; n > 0 {
			fmt.Fprintf(w, "| %s | %d |\n", c, n)
		}
	}
	fmt.Fprintf(w, "\n**Evidence read**\n\n")
	for _, l := range r.inputLines() {
		fmt.Fprintf(w, "- %s\n", strings.Replace(l, ":  ", ": ", 1))
	}

	if r.Summary.Total > 0 {
		fmt.Fprintf(w, "\n| Promise | Class | Kind | Confirmed | Reason |\n|---|---|---|---|---|\n")
		for _, row := range r.Rows {
			fmt.Fprintf(w, "| `%s` | %s | %s | %s | %s |\n", row.Promise, row.Class, row.Kind,
				row.Signals.Confirmed.Value, mdCell(row.Reason))
		}
	}

	var fakes []Row
	for _, row := range r.Rows {
		if row.Class == ClassFake {
			fakes = append(fakes, row)
		}
	}
	if len(fakes) > 0 {
		fmt.Fprintf(w, "\n### Fakes\n")
		for _, row := range fakes {
			fmt.Fprintf(w, "\n**`%s`** (%s): %s\n\n", row.Promise, row.Kind, mdCell(row.Statement))
			fmt.Fprintf(w, "- %s\n", row.Reason)
			for _, e := range row.Evidence.Enforcers {
				fmt.Fprintf(w, "- enforcer `%s` (%s:%d, reach %s)\n", e.FQN, e.File, e.Line, orDash(e.Reach))
			}
			for _, v := range row.Evidence.Verifiers {
				fmt.Fprintf(w, "- verifier `%s`: result %s, coverage %s, ran enforcer %t\n", v.FQN, v.Result, orDash(v.Coverage), v.RanEnforcer)
			}
			for _, b := range row.Evidence.Breaks {
				fmt.Fprintf(w, "- break of `%s` (%s): %s\n", b.Enforcer, b.Method, b.Outcome)
			}
			for _, n := range row.Notes {
				fmt.Fprintf(w, "- second reader %s: %s. %s\n", n.Reader, n.Verdict, n.Note)
			}
		}
	}
	fmt.Fprintf(w, "\n_%s_\n", r.Note)
}

func mdCell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", "\\|"), "\n", " ")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
