package check

// @module-feature check

import (
	"fmt"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
)

// This file reads the check's rules at head, for a scope that is not a change:
// a promise, a feature, a BRD. It adds no rule of its own. The signals of a
// row are rowSignals, the signals a tier needs are needs, the pairs that are
// not a pass are the gaps gapStates lists, and the words are gapNext. What a
// scope cannot say is left unknown: nothing_fell and the blind spot are
// measured against a base, so a scope view never reads ASSURED.

// Extra codes of the scope view.
const (
	CodeNoPromises      = "NO_PROMISES"
	CodeNotComparedBase = "NOT_COMPARED_TO_BASE"
)

// Needs lists the signals a tier requires.
func Needs(tier int) []string { return needs(tier) }

// RowSignals is the signal map of one audit row: unknown is never a pass.
func RowSignals(r audit.Row) map[string]Assurance { return rowSignals(r) }

// NextFor is the next action that closes a (promise, signal) gap.
func NextFor(promise, signal string) string { return gapNext(promise, signal) }

// HeadGaps lists the (promise, signal) pairs of one row that its tier needs
// and that are not a pass, in signal order. nothing_fell is not listed: it
// needs a base. A killed break shows the verifiers ran the enforcer, so
// exercised is not a gap then, as in the check.
func HeadGaps(r audit.Row) []Gap {
	sig := rowSignals(r)
	var out []Gap
	for _, name := range needs(r.Tier) {
		if name == SigNothingFell {
			continue
		}
		if name == SigExercised && sig[SigBreakTested].State == Pass {
			continue
		}
		a := sig[name]
		if a.State == Pass {
			continue
		}
		out = append(out, Gap{Promise: r.Promise, Signal: name, State: a.State, Honesty: a.Honesty, Code: a.Code,
			Text: a.Text, NextAction: gapNext(r.Promise, name)})
	}
	return out
}

// HeadVerdict is the verdict of one promise at head, with no base: UNPROVEN
// while a required signal is not a pass, and unknown otherwise (every
// required signal is present, but whether anything fell is a per-change
// question). It is never ASSURED.
func HeadVerdict(r audit.Row) Verdict {
	gaps := HeadGaps(r)
	if len(gaps) > 0 {
		return Verdict{State: Unproven, Code: CodeGaps, Params: map[string]interface{}{"n": len(gaps)}, Text: plural(len(gaps), "gap")}
	}
	return notCompared()
}

func notCompared() Verdict {
	return Verdict{State: Unknown, Code: CodeNotComparedBase,
		Text: "every required signal is present at head; whether anything fell is measured against a base (lattice check)"}
}

// RollUp is the verdict of a scope: the promises of a feature or a BRD. It is
// UNPROVEN while any promise has a gap, with the number of gaps and of
// promises that carry them; unknown when none does, or when there is no
// promise. Never ASSURED.
func RollUp(rows []audit.Row) Verdict {
	if len(rows) == 0 {
		return Verdict{State: Unknown, Code: CodeNoPromises, Text: "no promise is in this scope"}
	}
	gaps, withGaps := 0, 0
	for _, r := range rows {
		if n := len(HeadGaps(r)); n > 0 {
			gaps += n
			withGaps++
		}
	}
	if gaps == 0 {
		return notCompared()
	}
	return Verdict{State: Unproven, Code: CodeGaps,
		Params: map[string]interface{}{"n": gaps, "promises": withGaps, "of": len(rows)},
		Text:   fmt.Sprintf("%s in %d of %d promises", plural(gaps, "gap"), withGaps, len(rows))}
}
