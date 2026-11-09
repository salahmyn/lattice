package check

// @module-feature check

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// signalOrder is the order signals are listed in.
var signalOrder = []string{SigTraced, SigConfirmed, SigLive, SigExercised, SigBreakTested, SigNothingFell, SigSuiteGreen}

func signalRank(s string) int {
	for i, k := range signalOrder {
		if k == s {
			return i
		}
	}
	return len(signalOrder)
}

// needs lists the signals a tier requires. Tier defaults until the owner
// decides: tier 1 needs exercised or break_tested; tier 2 and 3 need
// break_tested. Every tier needs the promise traced, confirmed, live, and
// nothing fallen.
// @enforces check:INV-2
func needs(tier int) []string {
	req := []string{SigTraced, SigConfirmed, SigLive}
	if tier >= 2 {
		req = append(req, SigBreakTested)
	} else {
		req = append(req, SigExercised)
	}
	return append(req, SigNothingFell)
}

// --- signals of one audit row ---

// rowSignals reads the signals an audit row carries. Unknown is never a pass.
func rowSignals(r audit.Row) map[string]Assurance {
	out := map[string]Assurance{}

	tr := Assurance{State: Pass, Honesty: Fact, Text: "an enforcer is anchored"}
	if r.Signals.Enforced.Value != "yes" {
		tr = Assurance{State: Fail, Honesty: Fact, Code: "NO_ENFORCER", Text: "no enforcer is anchored to this promise"}
	}
	out[SigTraced] = tr

	cf := r.Signals.Confirmed
	switch cf.Value {
	case audit.ConfirmedHuman:
		out[SigConfirmed] = Assurance{State: Pass, Honesty: cf.Honesty, Text: cf.Detail}
	case "unknown":
		out[SigConfirmed] = Assurance{State: Unknown, Honesty: cf.Honesty, Code: "CONFIRMED_UNKNOWN", Text: "no confirmation data: " + cf.Detail}
	default:
		out[SigConfirmed] = Assurance{State: Fail, Honesty: cf.Honesty, Code: "CONFIRMED_" + strings.ToUpper(cf.Value), Text: confirmedText(cf)}
	}

	lv := r.Signals.Live
	switch lv.Value {
	case schema.ReachEntry:
		out[SigLive] = Assurance{State: Pass, Honesty: lv.Honesty, Text: "an entry point reaches an enforcer"}
	case schema.ReachUnknown:
		out[SigLive] = Assurance{State: Unknown, Honesty: lv.Honesty, Code: "LIVE_UNKNOWN", Text: orText(lv.Detail, "reach is unknown")}
	default:
		out[SigLive] = Assurance{State: Fail, Honesty: lv.Honesty, Code: "LIVE_" + strings.ToUpper(lv.Value), Text: "no entry point reaches an enforcer (" + lv.Value + ")"}
	}

	ex := r.Signals.Exercised
	switch ex.Value {
	case "yes":
		out[SigExercised] = Assurance{State: Pass, Honesty: ex.Honesty, Text: ex.Detail}
	case "no":
		out[SigExercised] = Assurance{State: Fail, Honesty: ex.Honesty, Code: "NEVER_RUNS_ENFORCER", Text: ex.Detail}
	default:
		out[SigExercised] = Assurance{State: Unknown, Honesty: ex.Honesty, Code: "NO_COVERAGE", Text: orText(ex.Detail, "no per-test coverage")}
	}

	bt := r.Signals.BreakTested
	switch bt.Value {
	case "killed":
		out[SigBreakTested] = Assurance{State: Pass, Honesty: bt.Honesty, Text: "breaking " + strings.Join(bt.Items, ", ") + " made the verifiers fail"}
	case "survived":
		out[SigBreakTested] = Assurance{State: Fail, Honesty: bt.Honesty, Code: "BREAK_SURVIVED", Text: "the verifiers passed with " + strings.Join(bt.Items, ", ") + " broken"}
	default:
		code := "NO_BREAK_RUN"
		if bt.Value == "partial_run" {
			code = "PARTIAL_RUN"
		}
		out[SigBreakTested] = Assurance{State: Unknown, Honesty: bt.Honesty, Code: code, Text: orText(bt.Detail, "no break run")}
	}
	return out
}

func confirmedText(s audit.Signal) string {
	switch s.Value {
	case audit.ConfirmedAgent:
		return "reviewed by an agent or council, awaiting a human: " + s.Detail
	case audit.ConfirmedDrifted:
		return "confirmed for an earlier version: " + s.Detail
	case audit.ConfirmedDraft:
		return "not confirmed: " + s.Detail
	case audit.ConfirmedNone:
		return "no BRD criterion maps to this promise"
	}
	return s.Value + ": " + s.Detail
}

func orText(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// gapStates lists the required signals of a row that are not a pass.
func gapStates(r audit.Row) map[string]string {
	sig := rowSignals(r)
	out := map[string]string{}
	for _, s := range needs(r.Tier) {
		if s == SigNothingFell {
			continue
		}
		a := sig[s]
		if s == SigExercised && sig[SigBreakTested].State == Pass {
			continue // a killed break shows the verifiers ran the enforcer
		}
		if a.State != Pass {
			out[s] = a.State
		}
	}
	return out
}

// --- per-promise assurance at head ---

func (c *computation) suiteAssurance() Assurance {
	s := c.in.Evidence.Suite
	switch {
	case !s.Present && !c.in.Evidence.Results.Counted:
		return Assurance{State: Unknown, Honesty: Fact, Code: CodeEvidenceNotCI, Text: c.in.Evidence.Results.Text}
	case !s.Present:
		return Assurance{State: Unknown, Honesty: Fact, Code: CodeNoResults, Text: "no test results ingested (lattice results ingest)"}
	case s.Failed > 0:
		return Assurance{State: Fail, Honesty: Fact, Code: CodeSuiteRed,
			Params: map[string]interface{}{"failed": s.Failed, "cases": s.Cases},
			Text:   fmt.Sprintf("%d of %d cases failed", s.Failed, s.Cases)}
	}
	return Assurance{State: Pass, Honesty: Fact, Text: fmt.Sprintf("%d cases passed", s.Cases-s.Failed)}
}

// signals is every signal of a head row, with nothing_fell and suite_green.
func (c *computation) signals(r audit.Row) Signals {
	m := rowSignals(r)
	s := Signals{
		Traced: m[SigTraced], Confirmed: m[SigConfirmed], Live: m[SigLive],
		Exercised: m[SigExercised], BreakTested: m[SigBreakTested],
		SuiteGreen: c.suiteAssurance(),
	}
	switch {
	case len(c.weakBy[r.Promise]) > 0:
		w := c.weakBy[r.Promise][0]
		s.NothingFell = Assurance{State: Fail, Honesty: Fact, Code: w.Code, Text: w.Text}
	case c.in.Base == nil:
		s.NothingFell = Assurance{State: Unknown, Honesty: Fact, Code: CodeNoBaseAudit,
			Text: "no base audit: evidence is not compared with base (structural checks found nothing)"}
	default:
		s.NothingFell = Assurance{State: Pass, Honesty: Fact, Text: "no level dropped against base"}
	}
	return s
}

func (s Signals) get(name string) Assurance {
	switch name {
	case SigTraced:
		return s.Traced
	case SigConfirmed:
		return s.Confirmed
	case SigLive:
		return s.Live
	case SigExercised:
		return s.Exercised
	case SigBreakTested:
		return s.BreakTested
	case SigNothingFell:
		return s.NothingFell
	}
	return s.SuiteGreen
}

// --- gaps ---

// suiteSubject is the promise of the one global suite_green pair.
const suiteSubject = "*"

// gaps lists the (promise, signal) pairs that are not a pass at head, with
// their delta against the base, and the pairs the change closed.
//
// A promise the change adds has no base to compare with; its unconfirmed gap
// cannot have existed before and does not block a merge by itself. Every other
// new gap does. With no base audit nothing can be shown to be old, so each gap
// reads delta unknown, and a gap that is not exempt blocks.
// @enforces check:INV-3
func (c *computation) gaps(touchedAny bool) []Gap {
	var out []Gap
	var considered []string
	for p := range c.headRows {
		if c.touched[p] != nil || c.in.Base != nil {
			considered = append(considered, p)
		}
	}
	sort.Strings(considered)

	for _, p := range considered {
		r := c.headRows[p]
		sig := c.signals(r)
		baseRow, hadBase := c.baseRows[p]
		var baseGaps map[string]string
		if hadBase {
			baseGaps = gapStates(baseRow)
		}
		wasTouched := c.touched[p] != nil
		var mine []Gap
		for _, name := range needs(r.Tier) {
			a := sig.get(name)
			if name == SigExercised && sig.BreakTested.State == Pass {
				continue
			}
			if a.State == Pass {
				if _, was := baseGaps[name]; was {
					mine = append(mine, Gap{Promise: p, Signal: name, State: Pass, Honesty: a.Honesty, Delta: DeltaClosed, Text: "closed since base"})
				}
				continue
			}
			g := Gap{Promise: p, Signal: name, State: a.State, Honesty: a.Honesty, Code: a.Code, Text: a.Text,
				NewPromise: c.newPromise[p], Waived: c.waivedPair(p, name)}
			switch {
			case c.in.Base == nil:
				g.Delta = DeltaUnknown
			case !hadBase:
				g.Delta = DeltaNew
			default:
				if _, was := baseGaps[name]; was {
					g.Delta = DeltaSame
				} else {
					g.Delta = DeltaNew
				}
			}
			g.Blocks = (g.Delta == DeltaNew || g.Delta == DeltaUnknown) && !(g.NewPromise && name == SigConfirmed)
			g.NextAction = gapNext(p, name)
			mine = append(mine, g)
		}
		if !wasTouched {
			// An untouched promise is on the page only when its gaps grew.
			var kept []Gap
			for _, g := range mine {
				if g.Delta == DeltaNew {
					kept = append(kept, g)
				}
			}
			if len(kept) == 0 {
				continue
			}
			mine = kept
			c.touch(p, "gap_grew")
		}
		out = append(out, mine...)
	}

	// The suite is one global pair, counted when the change touches anything.
	if touchedAny || len(c.touched) > 0 {
		if a := c.suiteAssurance(); a.State == Unknown {
			g := Gap{Promise: suiteSubject, Signal: SigSuiteGreen, State: a.State, Honesty: a.Honesty, Code: a.Code, Text: a.Text,
				Waived: c.waivedPair(suiteSubject, SigSuiteGreen), NextAction: gapNext(suiteSubject, SigSuiteGreen)}
			switch {
			case c.in.Base == nil:
				g.Delta = DeltaUnknown
			case !c.in.Base.Inputs.Results.Present:
				g.Delta = DeltaSame
			default:
				g.Delta = DeltaNew
			}
			g.Blocks = g.Delta == DeltaNew || g.Delta == DeltaUnknown
			out = append(out, g)
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.Promise == suiteSubject) != (b.Promise == suiteSubject) {
			return b.Promise == suiteSubject
		}
		if a.Promise != b.Promise {
			return a.Promise < b.Promise
		}
		return signalRank(a.Signal) < signalRank(b.Signal)
	})
	return out
}

func gapNext(promise, signal string) string {
	switch signal {
	case SigTraced:
		return "anchor an enforcer: add @enforces " + promise + " to the code that keeps it"
	case SigConfirmed:
		return "a human confirms the promise: lattice prepare promise confirm " + promise
	case SigLive:
		return "wire an entry point that reaches the enforcer, or have a human mark it out of reach"
	case SigExercised:
		return "add a verifier whose own run executes the enforcer, and ingest per-test coverage in CI"
	case SigBreakTested:
		return "run lattice break in CI (a scripted break where no tool exists)"
	case SigNothingFell:
		return "restore the base audit snapshot (lattice/.cache/audit/<base sha>.json) so evidence can be compared"
	case SigSuiteGreen:
		return "ingest the full CI test run: lattice results ingest"
	}
	return ""
}

// waivedPair says whether a waiver names the pair. It is display only.
func (c *computation) waivedPair(promise, signal string) bool {
	for _, w := range c.in.Waivers {
		if w.Subject == promise || w.Subject == promise+"/"+signal {
			return true
		}
	}
	return false
}

func (c *computation) waivers() []Waiver {
	inChange := false
	for _, f := range c.in.Impact.ChangedFiles {
		for _, p := range []string{f.NewPath, f.OldPath} {
			if p == "waivers.yaml" || strings.HasSuffix(p, "/waivers.yaml") {
				inChange = true
			}
		}
	}
	out := []Waiver{}
	for _, w := range c.in.Waivers {
		w.InChange = inChange
		out = append(out, w)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Subject < out[j].Subject })
	return out
}

// outOfReach lists touched promises a human marked out of reach.
func (c *computation) outOfReach() []OutOfReach {
	out := []OutOfReach{}
	for _, w := range c.in.Waivers {
		if w.Kind != "out_of_reach" {
			continue
		}
		for p := range c.touched {
			if w.Subject == p || strings.HasPrefix(w.Subject, p+"/") {
				out = append(out, OutOfReach{Promise: p, Kind: w.Kind, Reason: w.Reason})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Promise < out[j].Promise })
	return out
}

// --- rows ---

func (c *computation) rows(gaps []Gap) []PromiseRow {
	byPromise := map[string][]Gap{}
	for _, g := range gaps {
		byPromise[g.Promise] = append(byPromise[g.Promise], g)
	}
	var names []string
	for p := range c.touched {
		if _, ok := c.headRows[p]; ok {
			names = append(names, p)
		}
	}
	sort.Strings(names)
	out := make([]PromiseRow, 0, len(names))
	for _, p := range names {
		r := c.headRows[p]
		var why []string
		for w := range c.touched[p] {
			why = append(why, w)
		}
		sort.Strings(why)
		row := PromiseRow{
			Promise: p, Feature: r.Feature, Statement: r.Statement, Tier: r.Tier, Class: r.Class,
			Touched: why, NewPromise: c.newPromise[p], Signals: c.signals(r), Needs: needs(r.Tier),
		}
		row.NextAction = c.nextAction(p, byPromise[p])
		out = append(out, row)
	}
	return out
}

// nextAction is the one thing to do first for a promise.
func (c *computation) nextAction(p string, gaps []Gap) string {
	if ws := c.weakBy[p]; len(ws) > 0 {
		sort.SliceStable(ws, func(i, j int) bool { return ws[i].Code == CodeConfirmationVoid && ws[j].Code != CodeConfirmationVoid })
		if ws[0].Next != "" {
			return ws[0].Next
		}
	}
	for _, g := range gaps {
		if g.State != Pass && g.NextAction != "" {
			return g.NextAction
		}
	}
	return "nothing to do"
}

// --- verdict and merge ---

func countOpen(gaps []Gap) int {
	n := 0
	for _, g := range gaps {
		if g.State != Pass {
			n++
		}
	}
	return n
}

func (c *computation) verdict(gaps []Gap, untraced []UntracedLine, oor []OutOfReach) Verdict {
	open := countOpen(gaps)
	switch {
	case len(c.weak) > 0:
		return Verdict{State: Broken, Code: CodeBroken, Params: map[string]interface{}{"n": len(c.weak)},
			Text: plural(len(c.weak), "thing") + " fell: " + c.weak[0].Text}
	case open > 0:
		return Verdict{State: Unproven, Code: CodeGaps, Params: map[string]interface{}{"n": open, "untraced": len(untraced)},
			Text: plural(open, "gap") + " in the touched promises"}
	case len(untraced) > 0 || len(oor) > 0:
		return Verdict{State: Unproven, Code: CodeUntracedLines, Params: map[string]interface{}{"untraced": len(untraced), "out_of_reach": len(oor)},
			Text: "touched code no promise covers, or promises marked out of reach"}
	}
	if len(c.touched) == 0 {
		return Verdict{State: Assured, Code: CodeNothingTouched, Text: "the change touches no promise and no production line"}
	}
	return Verdict{State: Assured, Code: CodeAllSignals, Params: map[string]interface{}{"promises": len(c.touched)},
		Text: "every touched line is traced, every touched promise has the signals its tier needs, and nothing fell"}
}

// listN joins the first n items and counts the rest.
func listN(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(items[:n], ", "), len(items)-n)
}

func plural(n int, w string) string {
	if n == 1 {
		return "1 " + w
	}
	return strconv.Itoa(n) + " " + w + "s"
}

// merge applies the merge rule. ASSURED merges. UNPROVEN merges only when no
// (promise, signal) pair has a gap at head that it did not have at base, the
// count of new untraced lines did not rise, and no tier-3 promise is involved.
// A waiver does not remove a gap from that comparison. BROKEN never merges.
// @enforces check:INV-3
func (c *computation) merge(v Verdict, gaps []Gap, newUntraced int, rows []PromiseRow) (bool, MergeRule) {
	switch v.State {
	case Assured:
		if v.Code == CodeNothingTouched {
			return true, MergeRule{Code: CodeMergeOK, Text: "ASSURED: nothing the kernel tracks was touched"}
		}
		return true, MergeRule{Code: CodeMergeOK, Text: "ASSURED: every signal is present"}
	case Broken:
		return false, MergeRule{Code: CodeBroken, Text: "something fell: BROKEN never merges"}
	}
	var tier3 []string
	for _, r := range rows {
		if r.Tier >= 3 {
			tier3 = append(tier3, r.Promise)
		}
	}
	var fresh, unknown []string
	for _, g := range gaps {
		if !g.Blocks {
			continue
		}
		pair := g.Promise + "/" + g.Signal
		if g.Delta == DeltaUnknown {
			unknown = append(unknown, pair)
		} else {
			fresh = append(fresh, pair)
		}
	}
	switch {
	case len(tier3) > 0:
		return false, MergeRule{Code: CodeTier3, Params: map[string]interface{}{"promises": tier3},
			Text: "a tier-3 promise is involved: it needs ASSURED, not UNPROVEN"}
	case len(fresh) > 0:
		return false, MergeRule{Code: CodeNewGap, Params: map[string]interface{}{"pairs": fresh},
			Text: "UNPROVEN may merge only with no new (promise, signal) gap: " + listN(fresh, 4)}
	case len(unknown) > 0:
		return false, MergeRule{Code: CodeNoBaseAudit, Params: map[string]interface{}{"pairs": unknown},
			Text: "no base audit to show these gaps are old: " + listN(unknown, 4)}
	case newUntraced > 0:
		return false, MergeRule{Code: CodeUntracedRose, Params: map[string]interface{}{"new_untraced": newUntraced},
			Text: fmt.Sprintf("%d new touched lines are untraced", newUntraced)}
	}
	return true, MergeRule{Code: CodeMergeOK, Text: "UNPROVEN, and no gap is new: the merge rule allows it (not a pass)"}
}
