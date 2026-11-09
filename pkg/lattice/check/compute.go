package check

// @module-feature check

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/impact"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Input is everything one verdict needs.
type Input struct {
	// Rev is the base as the user wrote it; Impact.Base is its merge base.
	Rev string
	CI  bool
	// Head is the audit at head, computed from the evidence the check counts.
	Head audit.Report
	// Base is the audit snapshot of the merge base, or nil. BaseNote says why
	// there is none.
	Base     *audit.Report
	BaseNote string
	Impact   impact.Report
	// KG is the head graph: symbols, reach and entry points.
	KG       schema.KnowledgeGraph
	Evidence Evidence
	Waivers  []Waiver
}

// Compute builds the report. It is a pure function of its input.
func Compute(in Input) Report {
	c := &computation{in: in, headRows: map[string]audit.Row{}, baseRows: map[string]audit.Row{}}
	return c.run()
}

type computation struct {
	in       Input
	headRows map[string]audit.Row
	baseRows map[string]audit.Row
	// touched maps a promise to why it is on the page.
	touched    map[string]map[string]bool
	newPromise map[string]bool
	weak       []Weakening
	// weakBy lists the weakenings of each promise.
	weakBy map[string][]Weakening
	// touchedLines counts the lines of the touched production symbols.
	touchedLines int
}

func (c *computation) run() Report {
	in := c.in
	for _, r := range in.Head.Rows {
		c.headRows[r.Promise] = r
	}
	if in.Base != nil {
		for _, r := range in.Base.Rows {
			c.baseRows[r.Promise] = r
		}
	}
	c.touched = map[string]map[string]bool{}
	c.newPromise = map[string]bool{}
	c.weakBy = map[string][]Weakening{}

	c.findTouched()
	c.findWeakenings()

	untraced, newUntraced := c.untraced()
	gaps := c.gaps(len(untraced) > 0)
	rows := c.rows(gaps)
	oor := c.outOfReach()

	rep := Report{
		Contract: Contract,
		Commit:   in.Impact.Head,
		Base:     Ref{Rev: in.Rev, SHA: in.Impact.Base},
		Head:     Ref{SHA: in.Impact.Head, Dirty: in.Impact.Uncommitted},
		CI:       in.CI,
		Evidence: in.Evidence,
		Promises: rows,
		Gaps:     gaps,
		Waivers:  c.waivers(),
		Note:     note,
	}
	rep.Evidence.BaseAudit = BaseAudit{Present: in.Base != nil, Text: in.BaseNote}
	if in.Base != nil {
		rep.Evidence.BaseAudit.Commit = in.Base.Commit
	}
	rep.Weakenings = c.weak
	if rep.Weakenings == nil {
		rep.Weakenings = []Weakening{}
	}
	rep.BlindSpot = blindSpot(untraced, newUntraced, c.touchedLines, oor)

	rep.Verdict = c.verdict(gaps, untraced, oor)
	rep.MergeAllowed, rep.Merge = c.merge(rep.Verdict, gaps, newUntraced, rows)
	return rep
}

// --- touched promises ---

func (c *computation) touch(promise, why string) {
	if promise == "" {
		return
	}
	if c.touched[promise] == nil {
		c.touched[promise] = map[string]bool{}
	}
	c.touched[promise][why] = true
}

var (
	invField = regexp.MustCompile(`^invariants\[([^\]]+)\](?:\.(.*))?$`)
	scField  = regexp.MustCompile(`^success_criteria\[([^\]]+)\](?:\.(.*))?$`)
)

// findTouched fills touched and newPromise from impact: changed enforcers
// and verifiers, removed enforcement, and changed manifests.
func (c *computation) findTouched() {
	im := c.in.Impact

	verifierOf := map[string][]string{} // verifier FQN -> promises
	for _, rows := range []map[string]audit.Row{c.headRows, c.baseRows} {
		for p, r := range rows {
			for _, v := range r.Evidence.Verifiers {
				verifierOf[v.FQN] = append(verifierOf[v.FQN], p)
			}
		}
	}
	for _, cs := range im.ChangedSymbols {
		if cs.Test {
			for _, fqn := range []string{cs.Symbol, cs.OldSymbol} {
				for _, p := range verifierOf[fqn] {
					c.touch(p, "verifier_changed")
				}
			}
			continue
		}
		for _, p := range cs.Enforces {
			c.touch(p, "enforcer_changed")
		}
	}
	for _, ac := range append(append([]impact.AffectedCriterion{}, im.AffectedCriteria...), im.DraftCriteria...) {
		for _, e := range ac.Enforcers {
			c.touch(e.Invariant, "enforcer_changed")
		}
	}
	for _, re := range im.RemovedEnforcement {
		c.touch(re.Invariant, "enforcer_removed")
	}

	// Manifests: a feature's invariants, a BRD criterion's tier, statement
	// or mapping.
	headInv := map[string][]string{}
	for p, r := range c.headRows {
		headInv[r.Feature] = append(headInv[r.Feature], p)
	}
	for _, ref := range im.ManifestDiff.Features.Added {
		for _, p := range headInv[ref.ID] {
			c.touch(p, "promise_added")
			c.newPromise[p] = true
		}
	}
	for _, mc := range append(append([]impact.ManifestChange{}, im.ManifestDiff.Features.Changed...), im.ManifestDiff.Features.Renamed...) {
		for _, f := range mc.Fields {
			m := invField.FindStringSubmatch(f.Field)
			if m == nil {
				continue
			}
			p := mc.ID + ":" + m[1]
			switch {
			case m[2] == "" && f.Change == "added":
				c.touch(p, "promise_added")
				c.newPromise[p] = true
			case m[2] == "" && f.Change == "removed":
				c.touch(p, "promise_removed")
			case m[2] == "statement" || m[2] == "":
				c.touch(p, "promise_edited")
			}
		}
	}
	for _, mc := range append(append([]impact.ManifestChange{}, im.ManifestDiff.BRDs.Changed...), im.ManifestDiff.BRDs.Renamed...) {
		for _, f := range mc.Fields {
			m := scField.FindStringSubmatch(f.Field)
			if m == nil {
				continue
			}
			switch m[2] {
			case "tier", "statement", "maps_to_invariant", "":
			default:
				continue
			}
			for _, p := range c.criterionPromises(mc.ID, m[1], f) {
				c.touch(p, "criterion_edited")
			}
		}
	}

	// A promise at head that the base audit lacks is new, whatever the
	// manifest diff said.
	if c.in.Base != nil {
		for p := range c.headRows {
			if _, ok := c.baseRows[p]; !ok {
				c.newPromise[p] = true
				c.touch(p, "promise_added")
			}
		}
	}
}

// criterionPromises lists the promises a BRD criterion maps to, at head and
// as the edit left them.
func (c *computation) criterionPromises(brd, criterion string, f impact.FieldChange) []string {
	seen := map[string]bool{}
	var out []string
	add := func(refs ...string) {
		for _, r := range refs {
			if r != "" && !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	for _, b := range c.in.KG.BRDs {
		if b.ID != brd {
			continue
		}
		for _, sc := range b.SuccessCriteria {
			if sc.ID == criterion {
				add(sc.MapsToInvariant...)
			}
		}
	}
	if strings.HasSuffix(f.Field, "maps_to_invariant") || !strings.Contains(f.Field, ".") {
		add(stringsOf(f.Before)...)
		add(stringsOf(f.After)...)
	}
	sort.Strings(out)
	// Only promises the audit knows are rows.
	var known []string
	for _, p := range out {
		if _, ok := c.headRows[p]; ok {
			known = append(known, p)
		} else if _, ok := c.baseRows[p]; ok {
			known = append(known, p)
		}
	}
	return known
}

func stringsOf(v interface{}) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []interface{}:
		var out []string
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case map[string]interface{}:
		if refs, ok := x["maps_to_invariant"]; ok {
			return stringsOf(refs)
		}
	}
	return nil
}

// --- weakenings ---

// brokenClasses are the classes that say the evidence shows the promise is
// not kept. A drop to unknown or demonstrated_only is missing evidence, not
// a fall.
var brokenClasses = map[string]bool{
	audit.ClassFake: true, audit.ClassUnenforced: true,
	audit.ClassUnverified: true, audit.ClassNotDemonstrated: true,
}

func classRank(class string) int {
	for i, k := range audit.Classes {
		if k == class {
			return i
		}
	}
	return len(audit.Classes)
}

func (c *computation) addWeak(w Weakening) {
	c.weak = append(c.weak, w)
	if w.Promise != "" {
		c.weakBy[w.Promise] = append(c.weakBy[w.Promise], w)
		c.touch(w.Promise, "fell")
	}
}

// findWeakenings lists everything that fell. Any one reads BROKEN.
// @enforces check:INV-1
func (c *computation) findWeakenings() {
	im := c.in.Impact

	for _, re := range im.RemovedEnforcement {
		c.addWeak(Weakening{
			Code: CodeEnforcerRemoved, Promise: re.Invariant,
			Params: map[string]interface{}{"symbol": re.Symbol, "file": re.File, "line": re.Line, "reason": re.Reason},
			Text:   fmt.Sprintf("%s no longer enforces %s (%s)", re.Symbol, re.Invariant, impact.ReasonText(re)),
			Next:   "restore the enforcer, or retire the promise through a human-confirmed change",
		})
	}

	suite := c.in.Evidence.Suite
	if suite.Present && suite.Failed > 0 {
		c.addWeak(Weakening{
			Code:   CodeSuiteRed,
			Params: map[string]interface{}{"failed": suite.Failed, "cases": suite.Cases},
			Text:   fmt.Sprintf("the suite is red: %d of %d cases failed", suite.Failed, suite.Cases),
			Next:   "fix the failing tests: " + strings.Join(firstN(suite.Red, 5), ", "),
		})
	}

	// Compare each promise with the base audit.
	var promises []string
	for p := range c.baseRows {
		promises = append(promises, p)
	}
	sort.Strings(promises)
	for _, p := range promises {
		b := c.baseRows[p]
		h, ok := c.headRows[p]
		if !ok {
			continue // removal is read through the manifest diff below
		}
		survived := false
		if b.Signals.BreakTested.Value == "killed" && h.Signals.BreakTested.Value == "survived" {
			survived = true
			c.addWeak(Weakening{
				Code: CodeBreakSurvived, Promise: p,
				Params: map[string]interface{}{"enforcers": h.Signals.BreakTested.Items},
				Text:   fmt.Sprintf("%s: breaking %s was killed at base and survives at head", p, strings.Join(h.Signals.BreakTested.Items, ", ")),
				Next:   "the verifiers no longer fail when the enforcer is broken: restore what they assert",
			})
		}
		if !survived && brokenClasses[h.Class] && classRank(h.Class) < classRank(b.Class) {
			c.addWeak(Weakening{
				Code: CodeLevelDropped, Promise: p,
				Params: map[string]interface{}{"from": b.Class, "to": h.Class},
				Text:   fmt.Sprintf("%s fell from %s to %s: %s", p, b.Class, h.Class, h.Reason),
				Next:   "bring the promise back to its base level",
			})
		}
		if gone := removedVerifiers(b, h); len(gone) > 0 {
			c.addWeak(Weakening{
				Code: CodeVerifierRemoved, Promise: p,
				Params: map[string]interface{}{"verifiers": gone},
				Text:   fmt.Sprintf("%s lost its verifier %s", p, strings.Join(gone, ", ")),
				Next:   "restore the verifier, or have a human retire it with the promise",
			})
		}
	}

	// A confirmed promise's statement, tier or mapping was edited: the edit
	// voids the confirmation.
	voided := map[string]bool{}
	for p, why := range c.touched {
		if !(why["promise_edited"] || why["criterion_edited"] || why["promise_removed"]) {
			continue
		}
		if !c.wasConfirmed(p) {
			continue
		}
		voided[p] = true
	}
	var vp []string
	for p := range voided {
		vp = append(vp, p)
	}
	sort.Strings(vp)
	for _, p := range vp {
		c.addWeak(Weakening{
			Code: CodeConfirmationVoid, Promise: p,
			Text: fmt.Sprintf("%s was confirmed and its statement, tier or mapping was edited: the edit voids the confirmation", p),
			Next: "a human re-confirms the promise: lattice prepare promise confirm " + p + "; it reads BROKEN until then",
		})
	}
	sort.SliceStable(c.weak, func(i, j int) bool {
		if c.weak[i].Code != c.weak[j].Code {
			return c.weak[i].Code < c.weak[j].Code
		}
		return c.weak[i].Promise < c.weak[j].Promise
	})
}

// wasConfirmed says whether the promise was confirmed before this change: by
// the base audit, or, with no base audit, by the head audit (confirmed, or
// confirmed and since drifted).
func (c *computation) wasConfirmed(p string) bool {
	if b, ok := c.baseRows[p]; ok {
		return b.Signals.Confirmed.Value == audit.ConfirmedHuman
	}
	if c.in.Base != nil {
		return false
	}
	h, ok := c.headRows[p]
	if !ok {
		return false
	}
	v := h.Signals.Confirmed.Value
	return v == audit.ConfirmedHuman || v == audit.ConfirmedDrifted
}

// removedVerifiers lists the base verifiers a promise no longer has. A
// renamed verifier keeps the count, so it is not a removal.
func removedVerifiers(b, h audit.Row) []string {
	have := map[string]bool{}
	for _, v := range h.Evidence.Verifiers {
		have[v.FQN] = true
	}
	var gone []string
	for _, v := range b.Evidence.Verifiers {
		if !have[v.FQN] {
			gone = append(gone, v.FQN)
		}
	}
	if len(h.Evidence.Verifiers) >= len(b.Evidence.Verifiers) {
		return nil
	}
	sort.Strings(gone)
	return gone
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
