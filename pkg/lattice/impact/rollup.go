package impact

// @module-feature impact

import (
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// CodeRollup is the code-level reach of the changed and removed enforcers,
// from the SCIP code graph: who references each one, rolled up to modules,
// test files and entry points. It is optional data — with no code graph
// the report still holds and Available is false.
type CodeRollup struct {
	Available   bool               `json:"available"`
	Note        string             `json:"note,omitempty"`
	MaxSymbols  int                `json:"max_symbols"`
	Total       int                `json:"total_symbols"`
	Expanded    int                `json:"expanded_symbols"`
	Truncated   bool               `json:"truncated"`
	Symbols     []SymbolRollup     `json:"symbols"`
	Modules     []RollupModule     `json:"modules"`
	TestFiles   []string           `json:"test_files"`
	EntryPoints []RollupEntryPoint `json:"entry_points"`
}

// SymbolRollup is the blast radius of one enforcer.
type SymbolRollup struct {
	Symbol      string   `json:"symbol"`
	File        string   `json:"file"`
	Resolved    bool     `json:"resolved"`
	References  int      `json:"references"`
	Modules     []string `json:"modules"`
	TestFiles   []string `json:"test_files"`
	EntryPoints []string `json:"entry_points"`
}

// RollupModule is a module that references a changed enforcer.
type RollupModule struct {
	File    string `json:"file"`
	Feature string `json:"feature,omitempty"`
}

// RollupEntryPoint is an entry point a changed enforcer reaches, and how:
// "handler" (it is the handler), "handler file" (the handler's file
// references it) or "flow: <feature>" (the entry point's flow reaches a
// feature of the enforcer or of a module referencing it).
type RollupEntryPoint struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Trigger string   `json:"trigger,omitempty"`
	Via     []string `json:"via"`
}

type rollTarget struct{ fqn, file, graphFile, feature string }

// rollup expands the blast radius of each changed or removed enforcer,
// capped at in.MaxSymbols. Enforcers of approved criteria go first, then
// those of draft and proposed ones, then the rest, so a cap keeps what
// matters most.
func rollup(in Input, pm pathMap, cc codeResult, approved, drafts []AffectedCriterion) CodeRollup {
	r := CodeRollup{MaxSymbols: in.MaxSymbols, Symbols: []SymbolRollup{}, Modules: []RollupModule{},
		TestFiles: []string{}, EntryPoints: []RollupEntryPoint{}}

	feature := map[string]string{}
	for _, cs := range cc.symbols {
		feature[cs.Symbol] = cs.Feature
	}
	rank := map[string]int{}
	var targets []rollTarget
	addT := func(fqn, file, feat string, prio int) {
		if p, ok := rank[fqn]; ok {
			if prio < p {
				rank[fqn] = prio
			}
			return
		}
		rank[fqn] = prio
		if f, ok := feature[fqn]; ok && f != "" {
			feat = f
		}
		gf, _, _ := pm.toGraph(file)
		targets = append(targets, rollTarget{fqn: fqn, file: file, graphFile: gf, feature: feat})
	}
	for prio, list := range [][]AffectedCriterion{approved, drafts} {
		for _, c := range list {
			for _, e := range c.Enforcers {
				addT(e.Symbol, e.File, featureOf(e.Invariant), prio)
			}
		}
	}
	for _, cs := range cc.symbols {
		if len(cs.Enforces) > 0 {
			addT(cs.Symbol, cs.File, cs.Feature, 2)
		}
	}
	for _, rm := range cc.removed {
		addT(rm.Symbol, rm.File, featureOf(rm.Invariant), 2)
	}
	for _, a := range cc.added {
		addT(a.Symbol, a.File, featureOf(a.Invariant), 2)
	}
	sort.SliceStable(targets, func(i, j int) bool {
		if rank[targets[i].fqn] != rank[targets[j].fqn] {
			return rank[targets[i].fqn] < rank[targets[j].fqn]
		}
		return targets[i].fqn < targets[j].fqn
	})
	r.Total = len(targets)

	if in.Corpus == nil || in.Corpus.Empty() {
		r.Note = "code graph unavailable"
		if in.CorpusError != "" {
			r.Note += ": " + in.CorpusError
		} else {
			r.Note += ": no SCIP index (run `lattice extract --with-code-graph`)"
		}
		return r
	}
	r.Available = true
	if len(targets) > in.MaxSymbols {
		targets = targets[:in.MaxSymbols]
		r.Truncated = true
	}
	r.Expanded = len(targets)

	moduleFeature := map[string]string{}
	for _, m := range in.Graph.Modules {
		moduleFeature[m.File] = m.Feature
	}
	testFile := map[string]bool{}
	for _, t := range in.Graph.Tests {
		testFile[t.File] = true
	}
	// toModule maps a SCIP document path (relative to the code root the
	// indexer ran in) to the graph's module path.
	toModule := func(p string) string {
		if _, ok := moduleFeature[p]; ok {
			return p
		}
		for _, rp := range pm.roots {
			if _, ok := moduleFeature[rp.prefix+p]; ok && rp.prefix != "" {
				return rp.prefix + p
			}
		}
		return p
	}

	allMods := map[string]string{}
	allTests := map[string]bool{}
	allEPs := map[string]*RollupEntryPoint{}
	for _, t := range targets {
		br := in.Corpus.BlastRadius(t.fqn)
		sr := SymbolRollup{Symbol: t.fqn, File: t.file, Resolved: br.Resolved, References: len(br.References),
			Modules: []string{}, TestFiles: []string{}, EntryPoints: []string{}}
		refFiles := map[string]bool{}
		features := map[string]bool{}
		if t.feature != "" {
			features[t.feature] = true
		}
		for _, ref := range br.References {
			f := toModule(ref.File)
			if refFiles[f] {
				continue
			}
			refFiles[f] = true
			gitPath := pm.toGit(f)
			if testFile[f] {
				sr.TestFiles = append(sr.TestFiles, gitPath)
				allTests[gitPath] = true
				continue
			}
			sr.Modules = append(sr.Modules, gitPath)
			allMods[gitPath] = moduleFeature[f]
			if feat := moduleFeature[f]; feat != "" {
				features[feat] = true
			}
		}
		for _, ep := range in.Graph.EntryPoints {
			via := entryPointVia(ep, t, refFiles, features)
			if len(via) == 0 {
				continue
			}
			sr.EntryPoints = append(sr.EntryPoints, ep.ID)
			agg, ok := allEPs[ep.ID]
			if !ok {
				agg = &RollupEntryPoint{ID: ep.ID, Kind: ep.Kind, Trigger: triggerText(ep)}
				allEPs[ep.ID] = agg
			}
			for _, v := range via {
				if !contains(agg.Via, v) {
					agg.Via = append(agg.Via, v)
				}
			}
		}
		sort.Strings(sr.Modules)
		sort.Strings(sr.TestFiles)
		sort.Strings(sr.EntryPoints)
		r.Symbols = append(r.Symbols, sr)
	}
	for _, f := range sortedKeys(allMods) {
		r.Modules = append(r.Modules, RollupModule{File: f, Feature: allMods[f]})
	}
	r.TestFiles = sortedKeys(allTests)
	for _, id := range sortedKeys(allEPs) {
		ep := allEPs[id]
		sort.Strings(ep.Via)
		r.EntryPoints = append(r.EntryPoints, *ep)
	}
	return r
}

func entryPointVia(ep schema.EntryPoint, t rollTarget, refFiles, features map[string]bool) []string {
	var via []string
	switch {
	case ep.Handler.Symbol != "" && ep.Handler.Symbol == t.fqn:
		via = append(via, "handler")
	case ep.Handler.File != "" && (refFiles[ep.Handler.File] || ep.Handler.File == t.graphFile):
		via = append(via, "handler file")
	}
	for _, st := range ep.Flow {
		if features[st.Feature] {
			via = append(via, "flow: "+st.Feature)
		}
	}
	return via
}

func triggerText(ep schema.EntryPoint) string {
	t := ep.Trigger
	switch {
	case t.Command != "":
		return t.Command
	case t.Path != "":
		return strings.TrimSpace(t.Method + " " + t.Path)
	case t.Schedule != "":
		return t.Schedule
	case t.Queue != "":
		return t.Queue
	case t.Event != "":
		return t.Event
	}
	return ""
}

func featureOf(ref string) string {
	if i := strings.LastIndex(ref, ":"); i > 0 {
		return ref[:i]
	}
	return ""
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
