package reach

// @module-feature reachability

import (
	"fmt"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// Executed reports a symbol's whole-suite runtime coverage: whether the
// suite ran it, and whether any report covers its file at all. The
// coverage package's Index implements it.
type Executed interface {
	SuiteRan(fqn string) (ran, known bool)
}

// Effective is a symbol's class once runtime coverage is folded in.
// Coverage only moves a symbol up: one that ran is never unreached (it ran
// under the tests, so it is at least test_only); one that did not run
// keeps its static class.
// @enforces reachability:INV-5
func Effective(static string, fqn string, ran Executed) (class string, coverage bool) {
	if ran == nil || static != schema.ReachUnreached {
		return static, false
	}
	if did, known := ran.SuiteRan(fqn); known && did {
		return schema.ReachTestOnly, true
	}
	return static, false
}

// Findings raises the reachability findings. Their severities are the ones
// they take once enabled; validate drops each one unless the workspace
// enables it in validate.severity. With no roots the graph classifies
// nothing, so one REACHABILITY_NO_ROOTS stands in for every per-symbol
// finding. Every message names its evidence: the static call graph, and
// suite coverage where it was used.
// @enforces reachability:INV-6
func Findings(kg schema.KnowledgeGraph, r *Result, ran Executed) []schema.Violation {
	if r == nil || r.Graph == nil {
		return nil
	}
	var v []schema.Violation
	v = append(v, r.dynamicRPC()...)
	if !r.HasRoots() {
		return append(v, schema.Violation{
			Code: schema.CodeReachabilityNoRoots, Severity: schema.SeverityInfo,
			Subject:  "reachability",
			Message:  "the call graph has no roots (no entry point, entry file, func main, trigger, policy, cron job, serve handler or coverage.roots entry), so no symbol is classified (evidence: static call graph)",
			Location: &schema.Location{File: "config.yaml"},
			NextAction: &schema.NextAction{Kind: "edit_config", Field: "coverage.roots",
				Detail: "declare entry points, or list what the runtime starts from in coverage.roots (symbol or file globs, each with a reason)"},
		})
	}
	origin := r.origins()
	roots := map[string]bool{}
	for _, rt := range r.Roots {
		roots[rt.FQN] = true
	}
	withImports := map[string]bool{} // languages that have imports at all
	for _, m := range r.Graph.modules {
		if len(m.Imports) > 0 {
			withImports[m.Language] = true
		}
	}
	basis := fmt.Sprintf("static call graph, %d root(s)", len(r.Roots))
	for _, s := range kg.Symbols {
		static, ok := r.Class[s.FQN]
		if !ok {
			continue
		}
		loc := &schema.Location{File: s.File, Line: s.Line}
		class, raised := Effective(static, s.FQN, ran)
		cov := ""
		if ran != nil {
			did, known := ran.SuiteRan(s.FQN)
			switch {
			case !known:
				cov = "; no coverage for its file"
			case did:
				cov = "; suite coverage: it ran"
			default:
				cov = "; suite coverage: it did not run"
			}
		}
		switch class {
		case schema.ReachUnreached:
			msg := fmt.Sprintf("%s is reached from no root, no test and no reached symbol", s.FQN)
			if callers := r.Graph.Callers(s.FQN); len(callers) > 0 {
				msg += fmt.Sprintf("; its caller(s) %s are unreached too", strings.Join(head(callers, 3), ", "))
			}
			v = append(v, schema.Violation{
				Code: schema.CodeSymbolUnreached, Severity: schema.SeverityWarning,
				Subject: s.FQN, FeatureID: s.Feature, Location: loc,
				Message: msg + " (evidence: " + basis + cov + ")",
				NextAction: &schema.NextAction{Kind: "review_dead_code",
					Detail: "delete it, call it, or declare what reaches it in coverage.roots; allowlist it with a reason if the runtime reaches it by a convention the graph cannot see"},
			})
		case schema.ReachTestOnly:
			why := "no root reaches it"
			if raised {
				why = "the static graph reaches it from nothing, and it ran under the suite"
			}
			v = append(v, schema.Violation{
				Code: schema.CodeSymbolTestOnly, Severity: schema.SeverityInfo,
				Subject: s.FQN, FeatureID: s.Feature, Location: loc,
				Message: fmt.Sprintf("%s is reached only from tests: %s (evidence: %s%s)", s.FQN, why, basis, cov),
				NextAction: &schema.NextAction{Kind: "review_dead_code",
					Detail: "call it from production code, move it into test support, or delete it with its tests"},
			})
		case schema.ReachEntry:
			if ran == nil {
				break
			}
			if did, known := ran.SuiteRan(s.FQN); known && !did {
				from := ""
				if o := origin[s.FQN]; o != "" {
					from = " (" + o + ")"
				}
				v = append(v, schema.Violation{
					Code: schema.CodeSymbolUntested, Severity: schema.SeverityInfo,
					Subject: s.FQN, FeatureID: s.Feature, Location: loc,
					Message: fmt.Sprintf("%s is reachable from a root%s, but no test executed it (evidence: %s; suite coverage)", s.FQN, from, basis),
					NextAction: &schema.NextAction{Kind: "add_verification",
						Detail: "add a test that runs it, or ingest the coverage of the suite that does"},
				})
			}
		}
		if n := r.Graph.nodes[s.FQN]; n != nil && n.exported && n.enclosing == "" && n.namespace != "" &&
			withImports[n.lang] && !roots[s.FQN] && !r.Graph.Imported(s.FQN) {
			v = append(v, schema.Violation{
				Code: schema.CodeUnusedExport, Severity: schema.SeverityInfo,
				Subject: s.FQN, FeatureID: s.Feature, Location: loc,
				Message: fmt.Sprintf("%s is exported, but no other module imports or references it (evidence: static imports and references)", s.FQN),
				NextAction: &schema.NextAction{Kind: "review_dead_code",
					Detail: "make it module-private, or declare it in coverage.roots when it is public API used outside the workspace"},
			})
		}
	}
	return v
}

// dynamicRPC is RPC_UNRESOLVED: an .rpc() called with a computed name may
// call any database function, so it is named where a SYMBOL_UNREACHED of a
// SQL function could otherwise hide it. IMPORT_UNRESOLVED does the same
// for import(x) and require(x) of a computed module.
func (r *Result) dynamicRPC() []schema.Violation {
	var v []schema.Violation
	for _, u := range r.Graph.Unresolved {
		if u.Why != "dynamic" {
			continue
		}
		if u.Kind == ir.RefImport {
			v = append(v, schema.Violation{
				Code: schema.CodeImportUnresolved, Severity: schema.SeverityInfo,
				Subject:  fmt.Sprintf("%s:%d", u.From, u.Line),
				Message:  fmt.Sprintf("%s loads a module whose name is not a literal (import() or require()), so which module it reaches is unknown; an unreached symbol may be in it (evidence: static call graph)", u.From),
				Location: &schema.Location{File: u.File, Line: u.Line},
				NextAction: &schema.NextAction{Kind: "edit_code",
					Detail: "import a literal path, or declare the modules it can load in coverage.roots"},
			})
			continue
		}
		v = append(v, schema.Violation{
			Code: schema.CodeRPCUnresolved, Severity: schema.SeverityInfo,
			Subject:  fmt.Sprintf("%s:%d", u.From, u.Line),
			Message:  fmt.Sprintf("%s calls .rpc() with a name that is not a literal, so which database function it calls is unknown; an unreached SQL function may be its target (evidence: static call graph)", u.From),
			Location: &schema.Location{File: u.File, Line: u.Line},
			NextAction: &schema.NextAction{Kind: "edit_code",
				Detail: "call .rpc('name') with a literal name, or allowlist the call with the functions it can reach"},
		})
	}
	return v
}

// origins maps each node a root reaches to the reason of the root that
// reached it first (breadth-first from every root at once).
func (r *Result) origins() map[string]string {
	g := r.Graph
	out := map[string]string{}
	var queue []string
	for _, rt := range r.Roots {
		if _, seen := out[rt.FQN]; !seen {
			out[rt.FQN] = rt.Reason
			queue = append(queue, rt.FQN)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, n := range g.out[cur] {
			if _, seen := out[n]; !seen {
				out[n] = out[cur]
				queue = append(queue, n)
			}
		}
	}
	return out
}

// FeaturesReached returns the features with a symbol a root reaches (class
// entry). nil when the graph has no roots, so the caller keeps the
// flow-based rule.
// @enforces reachability:INV-7
func (r *Result) FeaturesReached(kg schema.KnowledgeGraph) map[string]bool {
	if !r.HasRoots() {
		return nil
	}
	out := map[string]bool{}
	for _, s := range kg.Symbols {
		if s.Feature == "" {
			continue
		}
		if r.Class[s.FQN] == schema.ReachEntry {
			out[s.Feature] = true
		}
	}
	return out
}

func head(list []string, n int) []string {
	if len(list) <= n {
		return list
	}
	out := append([]string(nil), list[:n]...)
	return append(out, fmt.Sprintf("and %d more", len(list)-n))
}
