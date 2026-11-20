package check

// @module-feature check

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/impact"
)

// An enforcer moved within one change.
//
// A refactor that moves enforcement from one symbol to another (a helper
// moved to a shared package, a function renamed) removes a base enforcer.
// Read as a removal, that is BROKEN, and the only way through is to keep a
// dead wrapper, which is worse than an honest move. A move must never weaken
// enforcement silently either: an agent can tag any trivial function
// @enforces. So a removal reads as a move only when the same change adds an
// enforcer of the promise that did not enforce it at base, and the move opens
// a break_tested gap that only a killed break of each added enforcer at head
// closes.

// move is the enforcement of one promise that moved within the change.
type move struct {
	// removed are the base enforcers that no longer enforce the promise.
	removed []impact.RemovedEnforcement
	// added are the enforcers the change adds, sorted.
	added []string
}

// findMoves decides which removed enforcement moved. A base enforcer of a
// tier 1 or 2 promise that no longer enforces it reads ENFORCER_MOVED, not
// BROKEN, only when the same change adds an enforcer of that promise that
// did not enforce it at base (impact's added_enforcement). A kept enforcer
// alone is not a move. A tier-3 promise, or one the head audit no longer
// has, never moves: its removal stays ENFORCER_REMOVED.
// @enforces check:INV-14
func (c *computation) findMoves() {
	c.moves = map[string]*move{}
	c.movedPair = map[string]bool{}
	addedBy := map[string][]string{}
	for _, a := range c.in.Impact.AddedEnforcement {
		if !slices.Contains(addedBy[a.Invariant], a.Symbol) {
			addedBy[a.Invariant] = append(addedBy[a.Invariant], a.Symbol)
		}
	}
	for _, re := range c.in.Impact.RemovedEnforcement {
		p := re.Invariant
		if len(addedBy[p]) == 0 || !c.movable(p) {
			continue
		}
		m := c.moves[p]
		if m == nil {
			m = &move{added: append([]string(nil), addedBy[p]...)}
			sort.Strings(m.added)
			c.moves[p] = m
		}
		m.removed = append(m.removed, re)
		c.movedPair[re.Symbol+"\x00"+p] = true
	}
}

// movable says whether a promise's enforcement may move: the head audit
// still has it, and its tier, at head and at base, is below 3.
func (c *computation) movable(p string) bool {
	h, ok := c.headRows[p]
	if !ok {
		return false
	}
	tier := h.Tier
	if b, ok := c.baseRows[p]; ok && b.Tier > tier {
		tier = b.Tier
	}
	return tier < 3
}

// moved says whether a removed enforcer is a move.
func (c *computation) moved(re impact.RemovedEnforcement) bool {
	return c.movedPair[re.Symbol+"\x00"+re.Invariant]
}

// moveGap reads the (promise, break_tested) gap a move opens, and whether it
// is still open. It closes only when the break results counted at head show
// a killed break of each added enforcer. A kill of a kept enforcer never
// counts, nor does a waiver; break results set aside (stale, or not from CI
// under --ci) or never run leave it open.
// @enforces check:INV-14
func (c *computation) moveGap(p string) (Assurance, bool) {
	m := c.moves[p]
	if m == nil {
		return Assurance{}, false
	}
	outcome := map[string]string{}
	for _, o := range c.headRows[p].Enforcers {
		outcome[o.Symbol] = o.Outcome
	}
	ev := c.in.Evidence.Breaks
	counted := ev.Present && ev.Counted
	var pending, survived []string
	for _, a := range m.added {
		switch {
		case !counted:
			pending = append(pending, a)
		case outcome[a] == audit.OutcomeKilled:
		case outcome[a] == audit.OutcomeSurvived:
			survived = append(survived, a)
			pending = append(pending, a)
		default:
			pending = append(pending, a)
		}
	}
	if len(pending) == 0 {
		return Assurance{}, false
	}
	a := Assurance{State: Unknown, Honesty: Fact, Code: CodeMoveNotBreakTested,
		Params: map[string]interface{}{"moved": m.removedSymbols(), "added": m.added, "pending": pending}}
	from := listN(m.removedSymbols(), 3)
	switch {
	case len(survived) > 0:
		a.State = Fail
		a.Text = fmt.Sprintf("enforcement moved from %s: the verifiers passed with %s broken", from, strings.Join(survived, ", "))
	case !ev.Present:
		a.Text = fmt.Sprintf("enforcement moved from %s: no break run at head, so no killed break of %s", from, strings.Join(pending, ", "))
	case !ev.Counted:
		a.Text = fmt.Sprintf("enforcement moved from %s: break results are set aside (%s), so no killed break of %s counts", from, ev.Code, strings.Join(pending, ", "))
	default:
		a.Text = fmt.Sprintf("enforcement moved from %s: no killed break of %s at head", from, strings.Join(pending, ", "))
	}
	return a, true
}

func (m *move) removedSymbols() []string {
	var out []string
	for _, re := range m.removed {
		if !slices.Contains(out, re.Symbol) {
			out = append(out, re.Symbol)
		}
	}
	sort.Strings(out)
	return out
}

// moveNext is what to do about an open move: run the breaks at head.
func moveNext(added []string) string {
	what := "a break of " + added[0] + " is"
	if len(added) > 1 {
		what = "a break of each of " + strings.Join(added, ", ") + " is"
	}
	return "run lattice break at head so " + what + " killed, then re-run the check; no human step is needed"
}

// moveNotes are the ENFORCER_MOVED notes of a promise, one per moved
// enforcer. A note never changes the verdict; the move's gap does.
func (c *computation) moveNotes(p string) []Note {
	m := c.moves[p]
	if m == nil {
		return nil
	}
	verb := "does"
	if len(m.added) > 1 {
		verb = "do"
	}
	_, open := c.moveGap(p)
	var out []Note
	for _, sym := range m.removedSymbols() {
		n := Note{Code: CodeEnforcerMoved,
			Text: fmt.Sprintf("%s no longer enforces %s; %s now %s", sym, p, strings.Join(m.added, ", "), verb)}
		if open {
			n.Next = moveNext(m.added)
		}
		out = append(out, n)
	}
	return out
}
