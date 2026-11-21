package impact

// @module-feature impact

import (
	"sort"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Why a change touches an acceptance case.
const (
	// CaseEnforcerChanged: the case's own enforcer (its `enforcer` field)
	// changed.
	CaseEnforcerChanged = "enforcer_changed"
	// CaseVerifierChanged: a test tagged @verifies <promise>#<case> changed.
	CaseVerifierChanged = "verifier_changed"
)

// TouchedCase is one acceptance case a change touches, and the changed
// symbol that touches it.
type TouchedCase struct {
	Promise string `json:"promise"`
	Case    string `json:"case"`
	// Label is how the case is displayed: "1a" for case a of INV-1.
	Label     string `json:"label"`
	Statement string `json:"statement"`
	Why       string `json:"why"`
	Symbol    string `json:"symbol"`
	Change    string `json:"change"`
}

// touchedCases names the cases a change touches: a case whose own enforcer
// changed (by its head or base name), and a case a changed test is tagged
// with on the working-tree side. A case without an enforcer of its own is
// not touched by a change to the invariant's enforcers: those touch the
// promise, which the report already names.
// @enforces impact:INV-10
func touchedCases(head schema.KnowledgeGraph, cc codeResult) []TouchedCase {
	type caseInfo struct {
		promise, id, label, statement string
	}
	byEnforcer := map[string][]caseInfo{}
	cases := map[string]caseInfo{}
	for _, f := range head.Features {
		for _, inv := range f.Invariants {
			promise := f.ID + ":" + inv.ID
			for _, c := range inv.Cases {
				ci := caseInfo{promise: promise, id: c.ID, label: schema.CaseLabel(inv.ID, c.ID), statement: c.Statement}
				cases[promise+"#"+c.ID] = ci
				if c.Enforcer != "" {
					byEnforcer[c.Enforcer] = append(byEnforcer[c.Enforcer], ci)
				}
			}
		}
	}
	if len(cases) == 0 {
		return nil
	}
	tagged := map[string][]string{}
	for _, t := range head.Tests {
		for _, ref := range t.VerifiesCases {
			tagged[t.FQN] = append(tagged[t.FQN], normRef(ref, t.Feature))
		}
	}

	seen := map[string]bool{}
	var out []TouchedCase
	add := func(ci caseInfo, why string, cs ChangedSymbol) {
		k := ci.promise + "#" + ci.id + "\x00" + why + "\x00" + cs.Symbol
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, TouchedCase{Promise: ci.promise, Case: ci.id, Label: ci.label, Statement: ci.statement,
			Why: why, Symbol: cs.Symbol, Change: cs.Change})
	}
	for _, cs := range cc.symbols {
		if cs.Test {
			for _, ref := range tagged[cs.Symbol] {
				if ci, ok := cases[ref]; ok {
					add(ci, CaseVerifierChanged, cs)
				}
			}
			continue
		}
		for _, name := range []string{cs.Symbol, cs.OldSymbol} {
			for _, ci := range byEnforcer[name] {
				add(ci, CaseEnforcerChanged, cs)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Promise != b.Promise {
			return lessID(a.Promise, b.Promise)
		}
		if a.Case != b.Case {
			return a.Case < b.Case
		}
		if a.Why != b.Why {
			return a.Why < b.Why
		}
		return a.Symbol < b.Symbol
	})
	return out
}
