package check

// @module-feature check

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/impact"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// lineScan is what the scan of the touched production symbols finds.
type lineScan struct {
	// untraced are the touched lines nothing traces (see untraced).
	untraced []UntracedLine
	// newUntraced counts the untraced lines that are new; only they fail
	// the merge rule.
	newUntraced int
	// unguarded are the touched symbols no promise guards that are not
	// untraced: an entry point reaches them (an entry point's handler in the
	// call graph, or, weaker, a flow through their feature), or the change
	// removes them.
	unguarded []UntracedLine
	// guarded counts the lines a promise guards: an enforcer span or a
	// checked reference covers them.
	guarded int
	// total counts every touched production line.
	total int
}

// unguardedLines counts the touched production lines no promise guards: the
// untraced ones and the ones only an entry point reaches.
func (s lineScan) unguardedLines() int {
	n := 0
	for _, u := range s.untraced {
		n += u.Count
	}
	for _, u := range s.unguarded {
		n += u.Count
	}
	return n
}

// untraced finds the touched production lines no promise covers.
//
// A touched symbol counts as traced only when an enforcer span covers it
// (the symbol carries @enforces), a checked reference names it (a direct-wire
// BRD criterion's enforced_by), or an entry point reaches it. A file-level
// @module-feature tag gives the symbol a feature and nothing else: it never
// makes a line traced.
//
// An entry point reaches a symbol in one of two ways, told apart in the
// reason. The strong one is per symbol: the call graph reaches it from the
// handler of a declared entry point (GraphSymbol.HandlerReach), whatever
// flows say. The weaker one is per feature, the older reading kept so that
// no line becomes untraced by this distinction: a root reaches the symbol
// and its feature is on an entry point's flow, but no handler is known to
// reach it. Only the strong one reads "an entry point reaches it"; a handler
// reach the graph cannot decide reads unknown, never reached.
//
// Traced is not guarded. Only an enforcer span or a checked reference, the
// lines impact attributes to a promise, guards a line. A symbol an entry
// point reaches and no promise guards is traced but unguarded, and so is a
// symbol the change removes (or moves out of the scan) that enforced nothing.
// Unguarded lines keep the verdict at UNPROVEN; they are reported and never
// fail the merge rule by themselves.
//
// Lines are counted by the touched symbol's extent, an upper bound on the
// lines the diff changed (a removed symbol by its base extent). A line is new
// when the change adds its symbol or removes its enforcement; only new
// untraced lines fail the merge rule.
// @enforces check:INV-5
// @enforces check:INV-9
// @enforces check:INV-13
func (c *computation) untraced() lineScan {
	kg := c.in.KG
	symbols := map[string][]schema.GraphSymbol{}
	for _, s := range kg.Symbols {
		symbols[s.FQN] = append(symbols[s.FQN], s)
	}
	referenced := map[string]bool{}
	for _, b := range kg.BRDs {
		for _, sc := range b.SuccessCriteria {
			for _, ref := range sc.EnforcedBy {
				referenced[ref] = true
			}
		}
	}
	reached := map[string]bool{} // feature -> an entry point's flow reaches it
	for _, ep := range kg.EntryPoints {
		for _, st := range ep.Flow {
			reached[st.Feature] = true
		}
	}
	lostEnforcement := map[string]bool{}
	for _, re := range c.in.Impact.RemovedEnforcement {
		lostEnforcement[re.Symbol] = true
	}

	var scan lineScan
	for _, cs := range c.in.Impact.ChangedSymbols {
		if cs.Test {
			continue
		}
		guarded := len(cs.Enforces) > 0 || referenced[cs.Symbol] || (cs.OldSymbol != "" && referenced[cs.OldSymbol])
		if cs.Change == impact.ChangeRemoved || cs.Change == impact.ChangeUnscanned {
			// Gone from the head tree: never untraced, but its lines were
			// touched. One that enforced a promise is guarded (and its
			// removal reads BROKEN); any other is unguarded.
			n := lineCount(cs.OldLines)
			scan.total += n
			if guarded {
				scan.guarded += n
				continue
			}
			why := "removed by the change; no promise guarded it"
			if cs.Change == impact.ChangeUnscanned {
				why = "its file left the scanned tree; no promise guarded it"
			}
			scan.unguarded = append(scan.unguarded, UntracedLine{File: cs.File, Symbol: cs.Symbol, Lines: cs.OldLines, Count: n, Reason: why})
			continue
		}
		n := lineCount(cs.Lines)
		scan.total += n
		if guarded {
			scan.guarded += n
			continue
		}
		sym, hasSym := pick(symbols[cs.Symbol], cs.File)
		hr := handlerReach(sym, hasSym)
		onFlow := hasSym && sym.Feature != "" && reached[sym.Feature] && sym.Reach == schema.ReachEntry
		traced := func(why string) {
			scan.unguarded = append(scan.unguarded, UntracedLine{File: cs.File, Symbol: cs.Symbol, Lines: cs.Lines, Count: n,
				HandlerReach: hr, Reason: why + ", but no promise guards it: no enforcer span or checked reference covers it"})
		}
		var why string
		switch {
		case hr == schema.HandlerReached:
			traced("an entry point reaches it")
			continue
		case onFlow && hr == schema.HandlerUnreached:
			traced("its feature is on an entry point's flow, but no entry point's handler reaches it in the call graph")
			continue
		case onFlow:
			traced("its feature is on an entry point's flow, but it is unknown whether an entry point's handler reaches it")
			continue
		case (cs.Feature != "" || (hasSym && sym.Feature != "")) && hr == schema.HandlerUnknown:
			why = "file or module tag only: no enforcer span or checked reference covers it, and it is unknown whether an entry point's handler reaches it"
		case cs.Feature != "" || (hasSym && sym.Feature != ""):
			why = "file or module tag only: no enforcer span, checked reference or entry point reaches it"
		default:
			why = "no feature or promise covers it"
		}
		isNew := cs.Change == impact.ChangeAdded || lostEnforcement[cs.Symbol] || (cs.OldSymbol != "" && lostEnforcement[cs.OldSymbol])
		scan.untraced = append(scan.untraced, UntracedLine{File: cs.File, Symbol: cs.Symbol, Lines: cs.Lines, Count: n, New: isNew,
			HandlerReach: hr, Reason: why})
		if isNew {
			scan.newUntraced += n
		}
	}
	for _, list := range [][]UntracedLine{scan.untraced, scan.unguarded} {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].File != list[j].File {
				return list[i].File < list[j].File
			}
			return list[i].Symbol < list[j].Symbol
		})
	}
	return scan
}

// handlerReach is the symbol's handler reach as the graph records it. A
// graph that classified the symbol's reach but not its handler reach (one
// written before handler reach existed) reads unknown, never reached; a
// symbol with no reach at all (a type alias, or no call graph) reads "".
func handlerReach(sym schema.GraphSymbol, ok bool) string {
	switch {
	case !ok:
		return ""
	case sym.HandlerReach != "":
		return sym.HandlerReach
	case sym.Reach != "":
		return schema.HandlerUnknown
	}
	return ""
}

// pick chooses the head symbol of an FQN, preferring the one in the file.
func pick(cands []schema.GraphSymbol, file string) (schema.GraphSymbol, bool) {
	for _, s := range cands {
		if s.File == file || strings.HasSuffix(file, "/"+s.File) || strings.HasSuffix(s.File, "/"+file) {
			return s, true
		}
	}
	if len(cands) > 0 {
		return cands[0], true
	}
	return schema.GraphSymbol{}, false
}

// lineCount reads a "12-30" extent; a symbol with none counts as one line.
func lineCount(extent string) int {
	a, b, ok := strings.Cut(extent, "-")
	from, err1 := strconv.Atoi(a)
	if !ok {
		return 1
	}
	to, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || to < from {
		return 1
	}
	return to - from + 1
}

// blindSpot is the honest edge of the other signals. It never fails the
// check by itself.
func blindSpot(scan lineScan, oor []OutOfReach) BlindSpot {
	n := 0
	for _, u := range scan.untraced {
		n += u.Count
	}
	unguarded := scan.unguardedLines()
	total := scan.total
	b := BlindSpot{
		State: "pass", Honesty: Fact, Code: CodeUntracedLines,
		Params: map[string]interface{}{"untraced": n, "of": total, "new": scan.newUntraced,
			"unguarded": unguarded, "guarded": scan.guarded},
		Untraced:       scan.untraced,
		UnguardedLines: unguarded,
		GuardedLines:   scan.guarded,
		Unguarded:      scan.unguarded,
		OutOfReach:     oor,
	}
	if b.Untraced == nil {
		b.Untraced = []UntracedLine{}
	}
	if b.Unguarded == nil {
		b.Unguarded = []UntracedLine{}
	}
	if b.OutOfReach == nil {
		b.OutOfReach = []OutOfReach{}
	}
	var parts []string
	switch {
	case total == 0:
		parts = append(parts, "the change touches no production line")
	case unguarded == 0 && len(oor) == 0:
		parts = append(parts, "every touched line is traced and guarded by a promise")
	}
	if n > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d touched lines untraced", n, total))
	}
	if len(scan.unguarded) > 0 {
		// Only said when an entry point or a removal hides lines beyond the
		// untraced ones; the untraced lines are unguarded too.
		parts = append(parts, fmt.Sprintf("%d of %d touched lines no promise guards (reported, not blocking)", unguarded, total))
	}
	if len(oor) > 0 {
		parts = append(parts, fmt.Sprintf("%d promise(s) marked out of reach", len(oor)))
	}
	if n > 0 || unguarded > 0 || len(oor) > 0 {
		b.State = "info"
	}
	b.Text = strings.Join(parts, "; ")
	return b
}
