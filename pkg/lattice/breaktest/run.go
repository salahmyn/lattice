package breaktest

// @module-feature breaktest

import (
	"errors"
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
	Promise    string // "feature:INV-n", "" for every promise
	Feature    string // limit to one feature, "" for all
	Timeout    time.Duration
	AllowDirty bool
	Config     config.Config
	LatticeDir string // absolute path of lattice/
	CodeRoot   string // absolute path of the primary code root
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
			continue
		}
		for _, s := range syms {
			out = append(out, target{promise: k, enforcers: []schema.GraphSymbol{s}, verifiers: sels, tests: tests})
		}
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
