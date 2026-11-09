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

// untraced finds the touched production lines no promise covers.
//
// A touched symbol counts as traced only when an enforcer span covers it
// (the symbol carries @enforces), a checked reference names it (a direct-wire
// BRD criterion's enforced_by), or an entry point of its feature reaches it.
// A file-level @module-feature tag gives the symbol a feature and nothing
// else: it never makes a line traced.
//
// Lines are counted by the touched symbol's extent, an upper bound on the
// lines the diff changed. A line is new when the change adds its symbol or
// removes its enforcement; only new untraced lines fail the merge rule.
// @enforces check:INV-5
func (c *computation) untraced() (list []UntracedLine, newCount int) {
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

	for _, cs := range c.in.Impact.ChangedSymbols {
		if cs.Test || cs.Change == impact.ChangeRemoved || cs.Change == impact.ChangeUnscanned {
			continue
		}
		n := lineCount(cs.Lines)
		c.touchedLines += n
		sym, hasSym := pick(symbols[cs.Symbol], cs.File)
		var why string
		switch {
		case len(cs.Enforces) > 0:
			continue
		case referenced[cs.Symbol]:
			continue
		case hasSym && sym.Reach == schema.ReachEntry && reached[sym.Feature] && sym.Feature != "":
			continue
		case cs.Feature != "" || (hasSym && sym.Feature != ""):
			why = "file or module tag only: no enforcer span, checked reference or entry point reaches it"
		default:
			why = "no feature or promise covers it"
		}
		isNew := cs.Change == impact.ChangeAdded || lostEnforcement[cs.Symbol] || (cs.OldSymbol != "" && lostEnforcement[cs.OldSymbol])
		list = append(list, UntracedLine{File: cs.File, Symbol: cs.Symbol, Lines: cs.Lines, Count: n, New: isNew, Reason: why})
		if isNew {
			newCount += n
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].File != list[j].File {
			return list[i].File < list[j].File
		}
		return list[i].Symbol < list[j].Symbol
	})
	return list, newCount
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
func blindSpot(list []UntracedLine, newCount, total int, oor []OutOfReach) BlindSpot {
	n := 0
	for _, u := range list {
		n += u.Count
	}
	b := BlindSpot{
		State: "pass", Honesty: Fact, Code: CodeUntracedLines,
		Params:     map[string]interface{}{"untraced": n, "of": total, "new": newCount},
		Text:       "every touched line is traced",
		Untraced:   list,
		OutOfReach: oor,
	}
	if b.Untraced == nil {
		b.Untraced = []UntracedLine{}
	}
	if b.OutOfReach == nil {
		b.OutOfReach = []OutOfReach{}
	}
	if total == 0 {
		b.Text = "the change touches no production line"
	}
	if n > 0 {
		b.State = "info"
		b.Text = fmt.Sprintf("%d of %d touched lines untraced", n, total)
	}
	if len(oor) > 0 {
		b.State = "info"
		b.Text += fmt.Sprintf("; %d promise(s) marked out of reach", len(oor))
		b.Text = strings.TrimPrefix(b.Text, "every touched line is traced; ")
	}
	return b
}
