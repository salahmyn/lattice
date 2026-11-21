package slice

// @module-feature reachability

import (
	"fmt"
	"io"
	"strings"
)

// WriteText prints a record: the reading first, the enforcers, then one line
// per path, root to enforcer, with the layer of each node in brackets and the
// edge kind between nodes. An unknown hop reads kind? instead of kind, and an
// exercised hop is drawn =kind=> instead of -kind->.
func WriteText(w io.Writer, r Record) {
	fmt.Fprintf(w, "slice %s\n", r.Invariant)
	fmt.Fprintf(w, "reading: %s", r.Reading)
	if r.Hop > 0 {
		fmt.Fprintf(w, " (hop %d)", r.Hop)
	}
	fmt.Fprintf(w, "\n  %s\n", r.Why)

	fmt.Fprintf(w, "\nenforcers (%d)\n", len(r.Enforcers))
	for _, e := range r.Enforcers {
		fmt.Fprintf(w, "  %s  [%s]  reach: %s", e.FQN, orDash(e.Layer), orDash(e.Reach))
		if e.AsyncHops > 0 {
			fmt.Fprintf(w, " (async: %d channel(s) crossed)", e.AsyncHops)
		}
		fmt.Fprintln(w)
	}

	shown := len(r.Paths)
	fmt.Fprintf(w, "\npaths (%d)\n", shown+r.Truncated)
	for _, p := range r.Paths {
		fmt.Fprintf(w, "  %s\n", PathLine(p))
		fmt.Fprintf(w, "      root: %s; carried by %s; %s", rootLabel(p.Root), strings.Join(p.CarriedBy, " -> "), hopCount(len(p.Hops)))
		if p.AsyncHops > 0 {
			fmt.Fprintf(w, "; %d async hop(s), through a channel", p.AsyncHops)
		}
		if p.ExercisedHops != nil {
			fmt.Fprintf(w, "; exercised %d of %d", *p.ExercisedHops, len(p.Hops))
		}
		fmt.Fprintln(w)
	}
	if r.Truncated > 0 {
		fmt.Fprintf(w, "  ... %d more path(s) not shown\n", r.Truncated)
	}
	if len(r.Paths) > 0 {
		fmt.Fprintln(w, "\nkind? marks an unknown hop: an ambiguous reference followed to one of its candidates.")
		fmt.Fprintln(w, "=kind=> marks an exercised hop: one verifier's own run executed symbols on both sides of it; -kind-> is a hop no verifier showed.")
		fmt.Fprintln(w, "--json lists every hop of every path.")
	}
	writeExercise(w, r)
}

// writeExercise prints the verifiers with the layers their own runs covered,
// or says once that no per-test coverage was ingested.
func writeExercise(w io.Writer, r Record) {
	switch r.Exercised {
	case ExercisedUnknown:
		fmt.Fprintf(w, "\nexercised: unknown, no per-test coverage (no hop is shown exercised)\n")
	case ExercisedMeasured:
		fmt.Fprintln(w)
	default:
		return
	}
	if len(r.Verifiers) > 0 {
		fmt.Fprintf(w, "verifiers (%d)\n", len(r.Verifiers))
		for _, v := range r.Verifiers {
			span := strings.Join(v.Layers, ", ")
			if span == "" {
				span = "ran no known symbol"
			}
			fmt.Fprintf(w, "  %s  [%s]\n", v.ID, span)
		}
	} else if r.Exercised == ExercisedMeasured {
		fmt.Fprintln(w, "verifiers (0)")
	}
}

// maxShownHops is the longest path printed in full; a longer one keeps its
// ends and every unknown hop, and counts the hops it leaves out.
const maxShownHops = 4

// PathLine is one path on a line: the root, then each node with its layer and
// the edge kind before it.
func PathLine(p Path) string {
	layer := ""
	if len(p.CarriedBy) > 0 {
		layer = p.CarriedBy[0]
	}
	if len(p.Hops) > 0 {
		layer = p.Hops[0].FromLayer
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s]", p.Root.FQN, layer)
	n := len(p.Hops)
	show := make([]bool, n+1) // node i is the start of hop i and the end of hop i-1
	for i := range show {
		show[i] = n <= maxShownHops || i == 0 || i == n
	}
	for i, h := range p.Hops {
		if h.Truth != TruthResolved {
			show[i], show[i+1] = true, true
		}
	}
	gap := 0
	for i, h := range p.Hops {
		if !show[i+1] {
			gap++
			continue
		}
		if gap > 0 {
			fmt.Fprintf(&b, " -> ... %d nodes ...", gap)
			gap = 0
		}
		kind := h.Kind
		if h.Truth != TruthResolved {
			kind += "?"
		}
		if i > 0 && !show[i] {
			fmt.Fprintf(&b, " -> %s [%s]", h.To, h.ToLayer)
			continue
		}
		if h.Exercised {
			fmt.Fprintf(&b, " =%s=> %s [%s]", kind, h.To, h.ToLayer)
			continue
		}
		fmt.Fprintf(&b, " -%s-> %s [%s]", kind, h.To, h.ToLayer)
	}
	return b.String()
}

func hopCount(n int) string {
	if n == 1 {
		return "1 hop"
	}
	return fmt.Sprintf("%d hops", n)
}

func rootLabel(r Root) string {
	if r.EntryPoint != "" {
		return "entry point " + r.EntryPoint
	}
	return r.Reason
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
