package breaktest

// @module-feature breaktest

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// ErrDirty is returned for a working tree with uncommitted changes when
// AllowDirty is off.
var ErrDirty = errors.New("working tree has uncommitted changes (commit them, or pass --allow-dirty)")

// Options controls a break run.
type Options struct {
	// Promise is "feature:INV-n", or "feature:INV-n#<case>" for one
	// acceptance case: its own enforcer (else the invariant's) broken against
	// only the tests tagged with the case. "" breaks every promise.
	Promise    string
	Feature    string // limit to one feature, "" for all
	Timeout    time.Duration
	AllowDirty bool
	Config     config.Config
	LatticeDir string // absolute path of lattice/
	CodeRoot   string // absolute path of the primary code root
	// Warn receives notes such as a reclaimed stale lock; nil is os.Stderr.
	Warn io.Writer
	// Edges adds, for each promise, a run per hop of EdgeHops that has a
	// literal out end: the literal is altered and the promise's verifiers
	// run. It is off by default because it multiplies the runs.
	Edges bool
	// EdgeHops are the hops on each promise's slice paths ("feature:INV-n"),
	// read from the reach graph by the caller. Only used with Edges.
	EdgeHops map[string][]EdgeHop
	// Lines records, for each promise, which lines its own verifiers ran: each
	// baseline invocation runs with coverage on and the report is kept in
	// verifier-lines.json (see the lines package). It needs a clean tree, and
	// without it commands render and are cached exactly as before.
	Lines bool
}

// warn is where notes go.
func (o Options) warn() io.Writer {
	if o.Warn != nil {
		return o.Warn
	}
	return os.Stderr
}

// Selects reports whether a promise is one the options ask for: every
// promise when neither --promise nor --feature is set.
func (o Options) Selects(promise string) bool {
	switch {
	case o.Promise != "":
		return promise == o.Promise
	case o.Feature != "":
		return strings.HasPrefix(promise, o.Feature+":")
	}
	return true
}

// target is one run to make: an enforcer of a promise, and the verifiers
// that should notice when it breaks.
type target struct {
	promise   string
	enforcers []schema.GraphSymbol // one, or all of the promise's for a script
	verifiers []results.Selector
	tests     []schema.GraphSymbol
	script    string // absolute path of the patch, "" for a generated break
	// hop is set for an edge break: the run breaks that hop's out end, and
	// enforcers is empty.
	hop *EdgeHop
}

// promiseKey normalises an invariant reference to "feature:INV-n".
func promiseKey(feature, ref string) string {
	if strings.Contains(ref, ":") {
		return ref
	}
	return feature + ":" + ref
}

// plan lists the runs: one per (promise, enforcer), or one per promise when
// a script replaces the generated break. Promises without an enforcer have
// nothing to break and no run.
func plan(kg schema.KnowledgeGraph, opts Options) []target {
	if base, id, ok := schema.SplitCaseRef(opts.Promise); ok {
		return planCase(kg, opts, base, id)
	}
	enforcers := map[string][]schema.GraphSymbol{}
	for _, s := range kg.Symbols {
		for _, inv := range s.EnforcesInvariants {
			k := promiseKey(s.Feature, inv)
			enforcers[k] = append(enforcers[k], s)
		}
	}
	selectors := map[string]results.Selector{}
	for _, sel := range results.Selectors(kg, true) {
		selectors[sel.Test] = sel
	}
	type pair struct {
		sels  []results.Selector
		tests []schema.GraphSymbol
	}
	verifiers := map[string]*pair{}
	for _, t := range kg.Tests {
		for _, v := range t.Verifies {
			k := promiseKey(t.Feature, v)
			p := verifiers[k]
			if p == nil {
				p = &pair{}
				verifiers[k] = p
			}
			p.sels = append(p.sels, selectors[t.FQN])
			p.tests = append(p.tests, t)
		}
	}

	keys := make([]string, 0, len(enforcers))
	for k := range enforcers {
		if opts.Promise != "" && k != opts.Promise {
			continue
		}
		if opts.Feature != "" && !strings.HasPrefix(k, opts.Feature+":") {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out []target
	for _, k := range keys {
		syms := dedupSymbols(enforcers[k])
		var sels []results.Selector
		var tests []schema.GraphSymbol
		if p := verifiers[k]; p != nil {
			sels, tests = p.sels, dedupSymbols(p.tests)
		}
		feature, inv, _ := strings.Cut(k, ":")
		script := filepath.Join(opts.LatticeDir, "breaks", feature, inv+".patch")
		if _, err := os.Stat(script); err != nil {
			script = ""
		}
		if script != "" {
			out = append(out, target{promise: k, enforcers: syms, verifiers: sels, tests: tests, script: script})
		} else {
			for _, s := range syms {
				out = append(out, target{promise: k, enforcers: []schema.GraphSymbol{s}, verifiers: sels, tests: tests})
			}
		}
		if opts.Edges {
			out = append(out, planEdges(opts, k, sels, tests)...)
		}
	}
	return out
}

// planEdges lists the edge runs of one promise: each hop with a literal out
// end, then the hops that are skipped, which read unknown with their reason.
func planEdges(opts Options, promise string, sels []results.Selector, tests []schema.GraphSymbol) []target {
	try, skipped, over := edgeTargets(opts.EdgeHops[promise])
	if over > 0 {
		fmt.Fprintf(opts.warn(), "break: %s has %d more hop(s) than the %d an edge break tries; the rest are not broken\n", promise, over, MaxEdgesPerPromise)
	}
	var out []target
	for _, h := range append(try, skipped...) {
		h := h
		out = append(out, target{promise: promise, verifiers: sels, tests: tests, hop: &h})
	}
	return out
}

func dedupSymbols(in []schema.GraphSymbol) []schema.GraphSymbol {
	seen := map[string]bool{}
	var out []schema.GraphSymbol
	for _, s := range in {
		if !seen[s.FQN] {
			seen[s.FQN] = true
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FQN < out[j].FQN })
	return out
}

// FindCase looks up an acceptance case by its ref ("feature:INV-n#<case>").
func FindCase(kg schema.KnowledgeGraph, ref string) (schema.InvariantCase, bool) {
	base, id, ok := schema.SplitCaseRef(ref)
	if !ok {
		return schema.InvariantCase{}, false
	}
	feature, invID, _ := strings.Cut(base, ":")
	for _, f := range kg.Features {
		if f.ID != feature {
			continue
		}
		for _, inv := range f.Invariants {
			if inv.ID != invID {
				continue
			}
			for _, c := range inv.Cases {
				if c.ID == id {
					return c, true
				}
			}
		}
	}
	return schema.InvariantCase{}, false
}

// planCase lists the runs for one acceptance case: the case's own enforcer,
// or each enforcer of the invariant when it names none, broken against only
// the tests tagged @verifies <promise>#<case>. The runs are recorded under
// the case ref, never the promise's, so they count for the case's coverage
// and leave the promise's break signal to runs of all its verifiers.
// @enforces breaktest:INV-11
func planCase(kg schema.KnowledgeGraph, opts Options, base, id string) []target {
	c, ok := FindCase(kg, opts.Promise)
	if !ok {
		return nil
	}
	var syms []schema.GraphSymbol
	for _, s := range kg.Symbols {
		for _, inv := range s.EnforcesInvariants {
			if promiseKey(s.Feature, inv) == base && (c.Enforcer == "" || s.FQN == c.Enforcer) {
				syms = append(syms, s)
			}
		}
	}
	syms = dedupSymbols(syms)
	if len(syms) == 0 {
		return nil
	}
	selectors := map[string]results.Selector{}
	for _, sel := range results.Selectors(kg, true) {
		selectors[sel.Test] = sel
	}
	var sels []results.Selector
	var tests []schema.GraphSymbol
	for _, t := range kg.Tests {
		for _, ref := range t.VerifiesCases {
			if promiseKey(t.Feature, ref) == opts.Promise {
				sels = append(sels, selectors[t.FQN])
				tests = append(tests, t)
				break
			}
		}
	}
	tests = dedupSymbols(tests)
	feature, inv, _ := strings.Cut(base, ":")
	script := filepath.Join(opts.LatticeDir, "breaks", feature, inv+".patch")
	if _, err := os.Stat(script); err != nil {
		script = ""
	}
	if script != "" {
		return []target{{promise: opts.Promise, enforcers: syms, verifiers: sels, tests: tests, script: script}}
	}
	var out []target
	for _, s := range syms {
		out = append(out, target{promise: opts.Promise, enforcers: []schema.GraphSymbol{s}, verifiers: sels, tests: tests})
	}
	return out
}
