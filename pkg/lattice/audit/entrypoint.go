package audit

// @module-feature audit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/lines"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// A promise can be break-tested and still not hold in production: its
// verifiers may call the enforcer directly, while the deployed way in wraps
// it in code that never lets the enforcer see the failure. The entry-point
// proof asks, for each break-tested promise, whether a verifier that killed a
// break ran the enforcer through a way in: the handler of an entry point whose
// static path reaches it. It reads the call graph, per-test coverage and the
// verifiers' line data as inputs, and never imports the packages that write
// them.

// Outcomes of the entry-point proof of a break-tested promise.
const (
	// EntryProven: some verifier that killed a break ran a way in.
	EntryProven = "entrypoint"
	// EntryNone: every killer is shown not to have run a way in, from a
	// complete static call graph, and nothing dynamic shows one ran.
	EntryNone = "none"
	// EntryUnknown: anything else. It raises no finding and is counted.
	EntryUnknown = "unknown"
)

// Causes of an unknown outcome, in EntryProof.Cause.
const (
	CauseNoDynamic  = "no_dynamic_evidence" // a killer can reach a way in; no coverage shows it ran
	CauseIncomplete = "incomplete_graph"    // a killer's static graph has gaps: unresolved or missing calls
	CauseNoWayIn    = "no_way_in"           // no entry point's handler reaches the enforcer
	CauseSQL        = "sql_enforcer"        // the way in to a SQL object is a database role
	CauseNoFailed   = "no_failed_tests"     // the break recorded no failed verifier
	CauseNoGraph    = "no_call_graph"       // no call graph was supplied
)

// causeLabels are the causes as the text summary names them.
var causeLabels = map[string]string{
	CauseNoDynamic:  "no dynamic evidence",
	CauseIncomplete: "incomplete call graph",
	CauseNoWayIn:    "no way in reaches the enforcer",
	CauseSQL:        "SQL enforcer",
	CauseNoFailed:   "no failed test names",
	CauseNoGraph:    "no call graph",
}

// Sources of a way in (WayIn.Source).
const (
	WayEntryPoint  = "entry_point"
	WayRuntimeRoot = "runtime_root"
)

// WayIn is a way into the system: the handler of a declared entry point, or
// a runtime root (a serve handler, a detected route) no entry point declares.
type WayIn struct {
	// ID is the entry point's id, or a label for a runtime root.
	ID      string `json:"id"`
	Handler string `json:"handler"`
	Kind    string `json:"kind,omitempty"`
	Source  string `json:"source"`
}

// EntryProof is the entry-point proof of one break-tested promise.
type EntryProof struct {
	Outcome string `json:"outcome"` // entrypoint | none | unknown
	// Cause says why an outcome is unknown.
	Cause  string `json:"cause,omitempty"`
	Reason string `json:"reason"`
	// Enforcers are the enforcers whose killed breaks were read; Killers the
	// verifiers whose failure killed them.
	Enforcers []string `json:"enforcers,omitempty"`
	Killers   []string `json:"killers,omitempty"`
	// WaysIn are the ways in whose static path reaches an enforcer.
	WaysIn []WayIn `json:"ways_in,omitempty"`
	// Evidence says, per killer that ran a way in, how that is known.
	Evidence []string `json:"evidence,omitempty"`
}

// EntryPointSummary counts the break-tested promises by outcome.
type EntryPointSummary struct {
	Proven  int `json:"proven"`
	None    int `json:"none"`
	Unknown int `json:"unknown"`
	// UnknownBy counts the unknown ones by cause.
	UnknownBy map[string]int `json:"unknown_by,omitempty"`
}

// CallGraph is the resolved call graph as the proof reads it. The caller
// fills it from the graph it built, or from callgraph.json.
type CallGraph struct {
	Edges      []CallEdge
	Unresolved []CallUnresolved
	// Roots are runtime roots that no declared entry point covers: other ways
	// in than the declared entry points.
	Roots []WayIn
}

// CallEdge is one resolved reference: From's code names To.
type CallEdge struct{ From, To, Kind string }

// CallUnresolved is a reference left without a target. A reference from code
// a killer reaches means the killer's static graph is not complete.
type CallUnresolved struct {
	From, Kind, Name, Qualifier, Why string
}

// publish and consume edges cross a channel: a way in whose path needs one
// reaches the enforcer asynchronously, which is no way in here.
func channelEdge(kind string) bool { return kind == "publish" || kind == "consume" }

// prover holds what the proof reads, indexed once per report.
type prover struct {
	cov      rtm.CoverageEvidence
	vlines   *lines.File
	symbols  map[string]schema.GraphSymbol
	hasGraph bool
	// out and in are the synchronous edges; all is every edge.
	out, in, all map[string][]string
	unresolved   map[string][]CallUnresolved
	ways         []WayIn
}

func newProver(src Sources, symbols map[string]schema.GraphSymbol) *prover {
	p := &prover{cov: src.Coverage, vlines: src.VerifierLines, symbols: symbols,
		out: map[string][]string{}, in: map[string][]string{}, all: map[string][]string{},
		unresolved: map[string][]CallUnresolved{}}
	if g := src.Calls; g != nil {
		p.hasGraph = true
		for _, e := range g.Edges {
			p.all[e.From] = append(p.all[e.From], e.To)
			if !channelEdge(e.Kind) {
				p.out[e.From] = append(p.out[e.From], e.To)
				p.in[e.To] = append(p.in[e.To], e.From)
			}
		}
		for _, u := range g.Unresolved {
			p.unresolved[u.From] = append(p.unresolved[u.From], u)
		}
		p.ways = append(p.ways, g.Roots...)
	}
	for _, ep := range src.KG.EntryPoints {
		// A proposal derived from a runtime root is the root again, which the
		// graph lists itself; a deprecated entry point is no way in.
		if ep.DetectedBy != "" || ep.Status == schema.StatusDeprecated || ep.Handler.Symbol == "" {
			continue
		}
		p.ways = append(p.ways, WayIn{ID: ep.ID, Handler: ep.Handler.Symbol, Kind: ep.Kind, Source: WayEntryPoint})
	}
	sort.SliceStable(p.ways, func(i, j int) bool {
		if p.ways[i].ID != p.ways[j].ID {
			return p.ways[i].ID < p.ways[j].ID
		}
		return p.ways[i].Handler < p.ways[j].Handler
	})
	return p
}

// reachFrom is the set of nodes adj reaches from start, start included.
func reachFrom(adj map[string][]string, start string) map[string]bool {
	seen := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, n := range adj[cur] {
			if !seen[n] {
				seen[n] = true
				queue = append(queue, n)
			}
		}
	}
	return seen
}

// wayPath is a way in whose handler reaches the enforcer, with the nodes on
// the static paths between: reached from the handler and reaching the
// enforcer. The enforcer itself is on a path only when it is the handler.
type wayPath struct {
	WayIn
	path []string
}

// waysTo lists the ways in whose handler reaches enf through synchronous
// edges, or is enf itself.
func (p *prover) waysTo(enf string) []wayPath {
	back := reachFrom(p.in, enf)
	var out []wayPath
	for _, w := range p.ways {
		if !back[w.Handler] {
			continue
		}
		if w.Handler != enf && !reachFrom(p.out, w.Handler)[enf] {
			continue
		}
		wp := wayPath{WayIn: w}
		if w.Handler == enf {
			wp.path = []string{enf}
		} else {
			fwd := reachFrom(p.out, w.Handler)
			for n := range fwd {
				if back[n] && n != enf {
					wp.path = append(wp.path, n)
				}
			}
			sort.Strings(wp.path)
		}
		out = append(out, wp)
	}
	return out
}

// Killer verdicts.
const (
	killerRan = iota
	killerNone
	killerUnknown
)

type killerVerdict struct {
	state  int
	cause  string
	detail string
}

// proveKiller reads what shows whether one verifier that killed a break of
// enf ran a way in, strongest evidence first: its own per-test run, the
// unshared line data of its verifier group, then the static call graph, which
// can only say it could (unknown) or could not (none).
func (p *prover) proveKiller(promise, k, enf string, ways []wayPath, killers []string) killerVerdict {
	// Proof is the handler itself running: a test that calls a symbol further
	// down the path skips whatever the handler does first (a catch-all, a
	// guard), which is the very gap an entry-point proof is for.
	perTest := false
	if p.cov != nil && p.cov.HasPerTest() {
		for _, w := range ways {
			ex, basis := p.cov.TestRan(k, w.Handler)
			if basis != rtm.EvidencePerTest {
				continue
			}
			perTest = true
			if ex {
				return killerVerdict{state: killerRan,
					detail: k + ": its own run executed the handler " + w.Handler + " of " + w.ID + " (per-test coverage)"}
			}
		}
	}
	// Line data is the group's: an unshared group's invocation ran only the
	// promise's verifiers, but not which of them hit a line. A killer whose
	// own per-test run is known is judged by that run alone.
	if !perTest {
		if d, ok := p.linesRan(promise, k, ways, killers); ok {
			return killerVerdict{state: killerRan, detail: d}
		}
	}

	reach := reachFrom(p.all, k)
	var hits []string
	for _, w := range ways {
		if reach[w.Handler] {
			hits = append(hits, w.ID+" ("+w.Handler+")")
		}
	}
	switch {
	case len(hits) > 0:
		return killerVerdict{state: killerUnknown, cause: CauseNoDynamic,
			detail: k + " reaches " + strings.Join(hits, ", ") + " statically, but no per-test coverage or unshared line data shows it ran"}
	case len(p.all[k]) == 0:
		return killerVerdict{state: killerUnknown, cause: CauseIncomplete,
			detail: "the call graph has no reference from " + k}
	case !reach[enf]:
		return killerVerdict{state: killerUnknown, cause: CauseIncomplete,
			detail: "the call graph has no static path from " + k + " to " + enf + ", which its failure shows it ran"}
	}
	var froms []string
	for n := range reach {
		if len(p.unresolved[n]) > 0 {
			froms = append(froms, n)
		}
	}
	if len(froms) > 0 {
		sort.Strings(froms)
		u := p.unresolved[froms[0]][0]
		what := u.Kind
		if u.Name != "" {
			what += " " + u.Name
		}
		return killerVerdict{state: killerUnknown, cause: CauseIncomplete,
			detail: k + " reaches code with an unresolved call (" + froms[0] + ": " + what + ", " + u.Why + "), which could run a way in"}
	}
	return killerVerdict{state: killerNone}
}

// linesRan reads verifier-lines.json: the killer's runner group had an
// invocation that ran no other test, and it hit a line of a way in's handler.
// Group data cannot say which of its tests ran a line, so it proves a killer
// only when every test the group ran killed the break: a verifier that ran
// the handler and passed noticed nothing, and must not lend its run.
func (p *prover) linesRan(promise, k string, ways []wayPath, killers []string) (string, bool) {
	if p.vlines == nil {
		return "", false
	}
	for _, g := range p.vlines.Promises[promise] {
		if g.Shared || g.Unknown != "" || g.Key == "" || !hasString(g.Tests, k) {
			continue
		}
		allKilled := len(g.Tests) > 0
		for _, t := range g.Tests {
			if !hasString(killers, t) {
				allKilled = false
			}
		}
		if !allKilled {
			continue
		}
		inv, ok := p.vlines.Invocations[g.Key]
		if !ok || inv.Unknown != "" {
			continue
		}
		for _, w := range ways {
			s := p.symbols[w.Handler]
			fl, ok := inv.Files[s.File]
			if !ok {
				continue
			}
			if ran, known := fl.RanSymbol(s); known && ran {
				return k + ": its verifier group's unshared invocation ran lines of the handler " + w.Handler + " of " + w.ID +
					" (verifier-lines.json; group level, it does not say which verifier of the group ran them)", true
			}
		}
	}
	return "", false
}

// verifierFQNs lists the verifiers of a row.
func verifierFQNs(r Row) []string {
	out := make([]string, 0, len(r.Evidence.Verifiers))
	for _, v := range r.Evidence.Verifiers {
		out = append(out, v.FQN)
	}
	return out
}

// failedSet reads the names a break run recorded as failed tests with the
// matching rules of an ingested set, and says whether a verifier is among
// them.
func failedSet(names []string) func(verifier string) bool {
	set := results.FailedSet(names)
	return func(v string) bool {
		o, ok := set.Lookup(v)
		return ok && o == results.Fail
	}
}

// killersOf lists the verifiers whose failure killed a run: the verifiers of
// the promise among the run's failed tests.
func killersOf(run BreakRun, verifiers []string) []string {
	failed := failedSet(run.FailedTests)
	var out []string
	for _, v := range verifiers {
		if failed(v) {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// runProof is the proof of one killed break.
type runProof struct {
	outcome, cause, reason string
	killers                []string
	ways                   []WayIn
	evidence               []string
}

func (p *prover) proveRun(promise string, run BreakRun, verifiers []string) runProof {
	rp := runProof{outcome: EntryUnknown}
	if s := p.symbols[run.Enforcer]; s.Language == "postgres" || s.Language == "sql" {
		rp.cause, rp.reason = CauseSQL, run.Enforcer+": SQL enforcer: ways in are database roles"
		return rp
	}
	rp.killers = killersOf(run, verifiers)
	switch {
	case len(rp.killers) == 0:
		rp.cause = CauseNoFailed
		rp.reason = "the break of " + run.Enforcer + " recorded no failed test that is a verifier of the promise, so no killer is known"
		return rp
	case !p.hasGraph:
		rp.cause, rp.reason = CauseNoGraph, "no call graph was read, so no way in can be named for "+run.Enforcer
		return rp
	}
	ways := p.waysTo(run.Enforcer)
	if len(ways) == 0 {
		rp.cause = CauseNoWayIn
		rp.reason = "no entry point's handler has a static path to " + run.Enforcer
		return rp
	}
	for _, w := range ways {
		rp.ways = append(rp.ways, w.WayIn)
	}

	var unknown []killerVerdict
	for _, k := range rp.killers {
		v := p.proveKiller(promise, k, run.Enforcer, ways, rp.killers)
		switch v.state {
		case killerRan:
			rp.evidence = append(rp.evidence, v.detail)
		case killerUnknown:
			unknown = append(unknown, v)
		}
	}
	switch {
	case len(rp.evidence) > 0:
		rp.outcome = EntryProven
		rp.reason = strings.Join(rp.evidence, "; ")
	case len(unknown) == 0:
		rp.outcome = EntryNone
		rp.reason = "no verifier that killed a break of " + run.Enforcer + " ran an entry point's handler that reaches it: " +
			strings.Join(rp.killers, ", ") + " reach none of " + wayNames(rp.ways) +
			" in the static call graph, which has no unresolved call from them"
	default:
		rp.cause = unknown[0].cause
		var why []string
		for _, u := range unknown {
			why = append(why, u.detail)
		}
		rp.reason = strings.Join(why, "; ")
	}
	return rp
}

// wayNames lists ways in as "id (handler)".
func wayNames(ways []WayIn) string {
	var out []string
	for _, w := range ways {
		out = append(out, w.ID+" ("+w.Handler+")")
	}
	return strings.Join(out, ", ")
}

// proveRow folds the proof of each killed break of a promise into one: proven
// when a killer of any killed break ran a way in, none when every break's
// killers are shown not to have, unknown otherwise. It returns nil for a
// promise with no killed break. A killed break whose verifiers are unknown
// stays unknown, and no finding is raised from it.
// @enforces audit:INV-11
func (p *prover) proveRow(promise string, verifiers []string, runs []BreakRun) *EntryProof {
	var killed []BreakRun
	for _, r := range runs {
		if r.Outcome == "killed" {
			killed = append(killed, r)
		}
	}
	if len(killed) == 0 {
		return nil
	}
	proof := &EntryProof{}
	var rps []runProof
	for _, r := range killed {
		rp := p.proveRun(promise, r, verifiers)
		rps = append(rps, rp)
		proof.Enforcers = append(proof.Enforcers, r.Enforcer)
	}
	sort.Strings(proof.Enforcers)

	waySeen := map[string]bool{}
	killerSeen := map[string]bool{}
	allNone, anyProven := true, false
	var unknown []runProof
	for _, rp := range rps {
		for _, w := range rp.ways {
			if k := w.ID + "\x00" + w.Handler; !waySeen[k] {
				waySeen[k] = true
				proof.WaysIn = append(proof.WaysIn, w)
			}
		}
		for _, k := range rp.killers {
			if !killerSeen[k] {
				killerSeen[k] = true
				proof.Killers = append(proof.Killers, k)
			}
		}
		switch rp.outcome {
		case EntryProven:
			anyProven = true
			proof.Evidence = append(proof.Evidence, rp.evidence...)
		case EntryUnknown:
			unknown = append(unknown, rp)
		}
		if rp.outcome != EntryNone {
			allNone = false
		}
	}
	sort.Strings(proof.Killers)
	sort.SliceStable(proof.WaysIn, func(i, j int) bool { return proof.WaysIn[i].ID < proof.WaysIn[j].ID })

	switch {
	case anyProven:
		proof.Outcome = EntryProven
		proof.Reason = "a verifier that killed a break ran a way in: " + strings.Join(proof.Evidence, "; ")
	case allNone:
		proof.Outcome = EntryNone
		var parts []string
		for _, rp := range rps {
			parts = append(parts, rp.reason)
		}
		proof.Reason = strings.Join(dedupe(parts), "; ")
	default:
		proof.Outcome = EntryUnknown
		proof.Cause = unknown[0].cause
		var parts []string
		for _, rp := range unknown {
			parts = append(parts, rp.reason)
		}
		proof.Reason = strings.Join(dedupe(parts), "; ")
	}
	return proof
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// tallyEntryPoints counts the rows' proofs; nil when no row has one.
func tallyEntryPoints(rows []Row) *EntryPointSummary {
	var s EntryPointSummary
	n := 0
	for _, r := range rows {
		if r.EntryPoint == nil {
			continue
		}
		n++
		switch r.EntryPoint.Outcome {
		case EntryProven:
			s.Proven++
		case EntryNone:
			s.None++
		default:
			s.Unknown++
			if s.UnknownBy == nil {
				s.UnknownBy = map[string]int{}
			}
			s.UnknownBy[r.EntryPoint.Cause]++
		}
	}
	if n == 0 {
		return nil
	}
	return &s
}

// Line is the summary as one text line: "entry-point proof: 3 proven, 1 none, 7
// unknown (5 no dynamic evidence, 2 unresolved calls)".
func (s EntryPointSummary) Line() string {
	line := fmt.Sprintf("entry-point proof: %d proven, %d none, %d unknown", s.Proven, s.None, s.Unknown)
	if s.Unknown == 0 {
		return line
	}
	var causes []string
	for c := range s.UnknownBy {
		causes = append(causes, c)
	}
	sort.Slice(causes, func(i, j int) bool {
		if s.UnknownBy[causes[i]] != s.UnknownBy[causes[j]] {
			return s.UnknownBy[causes[i]] > s.UnknownBy[causes[j]]
		}
		return causes[i] < causes[j]
	})
	var parts []string
	for _, c := range causes {
		label := causeLabels[c]
		if label == "" {
			label = "other"
		}
		parts = append(parts, fmt.Sprintf("%d %s", s.UnknownBy[c], label))
	}
	return line + " (" + strings.Join(parts, ", ") + ")"
}

// EntryPointFindings is BREAK_NO_ENTRYPOINT (info) for each break-tested
// promise whose outcome is none: the verifiers that killed a break never ran
// an entry point's handler that reaches the enforcer, so the break test does
// not show the promise holds on the way the system is entered. An unknown
// outcome raises nothing: it is counted in the audit. loc maps a feature to
// where it is declared.
// @enforces audit:INV-11
func EntryPointFindings(rep *Report, loc map[string]*schema.Location) []schema.Violation {
	if rep == nil {
		return nil
	}
	var out []schema.Violation
	for _, r := range rep.Rows {
		ep := r.EntryPoint
		if ep == nil || ep.Outcome != EntryNone {
			continue
		}
		out = append(out, schema.Violation{
			Code: schema.CodeBreakNoEntryPoint, Severity: schema.SeverityInfo,
			FeatureID: r.Feature, InvariantID: r.Invariant, Subject: r.Promise, Location: loc[r.Feature],
			Message: fmt.Sprintf("invariant %s is break-tested, but no verifier that killed a break ran an entry point's handler that reaches %s: killed by %s; the way in that reaches it is %s (evidence: static call graph, no unresolved call from the killers)",
				r.Promise, strings.Join(ep.Enforcers, ", "), strings.Join(ep.Killers, ", "), wayNames(ep.WaysIn)),
			NextAction: &schema.NextAction{Kind: "add_verification", Ref: r.Promise,
				Detail: "add a verifier that runs " + r.Promise + " through one of these ways in (" + wayNames(ep.WaysIn) +
					"), tag it `@verifies " + r.Promise + "`, and run it as a break runner, so a kill of the enforcer can come from it"},
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Subject < out[j].Subject })
	return out
}
