// Package graph builds the Lattice knowledge graph: it fuses feature
// manifests, parsed source IR, initiatives, and tasks into the single
// deterministically-ordered structure emitted as lattice.json.
package graph

import (
	"sort"
	"strings"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/buildinfo"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// Input is the raw material the graph builder fuses.
type Input struct {
	BRDs        []schema.BRD
	Manifests   []schema.Manifest
	Modules     []ir.Module
	Initiatives []schema.Initiative
	Tasks       []schema.Task
	Violations  []schema.Violation
	// Review marks a graph built without source (the PM/QA review case).
	Review bool
}

// Options pins environment-derived fields so output can be byte-identical.
type Options struct {
	GeneratedAt     time.Time         // zero -> time.Now().UTC()
	Commit          string            // git SHA, "" if unknown
	LanguageIndexes map[string]string // scip index paths, if available
}

// Build fuses the input into a knowledge graph with fully resolved edges and
// deterministic ordering.
func Build(in Input, opts Options) schema.KnowledgeGraph {
	res := newResolver(in.Manifests, in.Modules)

	var symbols, tests []schema.GraphSymbol
	var modules []schema.GraphModule

	for mi := range in.Modules {
		mod := &in.Modules[mi]
		gm := resolveModule(mod)
		for si := range mod.Symbols {
			sym := &mod.Symbols[si]
			r := res.resolveSymbol(sym, mod)
			gs := schema.GraphSymbol{
				Name: sym.Name, FQN: sym.FQN, Kind: string(sym.Kind),
				File: sym.File, Line: sym.Line, Language: mod.Language,
				EnclosingFQN: sym.EnclosingFQN, IsTest: sym.IsTest,
				Exported: sym.Exported,
				Feature:  r.feature, Capabilities: r.capabilities,
				EnforcesInvariants: r.enforces, DependsOnFeatures: r.dependsOn,
				Roles: r.roles, Verifies: r.verifies, SuppressedInvariants: r.suppressed,
			}
			gm.SymbolFQNs = append(gm.SymbolFQNs, sym.FQN)
			if sym.IsTest {
				tests = append(tests, gs)
			} else {
				symbols = append(symbols, gs)
			}
		}
		modules = append(modules, gm)
	}

	manifests := hydrateManifests(in.Manifests, symbols, tests)
	checks := collectStructuralChecks(manifests)
	tasks := computeUnblocks(in.Tasks)
	surfaces := buildSurfaces(in.Manifests, in.Modules, res)
	errors := buildErrors(in.Manifests, in.Modules, res)

	sortGraphSymbols(symbols)
	sortGraphSymbols(tests)
	sort.Slice(modules, func(i, j int) bool { return modules[i].File < modules[j].File })
	for i := range modules {
		sort.Strings(modules[i].SymbolFQNs)
	}

	gen := opts.GeneratedAt
	if gen.IsZero() {
		gen = time.Now().UTC()
	}

	violations := append([]schema.Violation(nil), in.Violations...)
	sortViolations(violations)

	// Normalize empty collections to non-nil so lattice.json emits [] not null.
	if symbols == nil {
		symbols = []schema.GraphSymbol{}
	}
	if tests == nil {
		tests = []schema.GraphSymbol{}
	}
	if modules == nil {
		modules = []schema.GraphModule{}
	}
	if surfaces == nil {
		surfaces = []schema.GraphSurface{}
	}
	if errors == nil {
		errors = []schema.GraphError{}
	}
	if checks == nil {
		checks = []schema.GraphStructuralCheck{}
	}
	if violations == nil {
		violations = []schema.Violation{}
	}
	initiatives := in.Initiatives
	if initiatives == nil {
		initiatives = []schema.Initiative{}
	}

	// BRDs: copy as-is and sort by id so re-extracts are byte-stable.
	// The BRD ↔ Feature reverse edge isn't materialised on the graph
	// node itself — `brd.FeaturesByBRD(brds, features)` rebuilds it on
	// demand. Keeping it derived means a feature edit never has to
	// re-write the BRD on disk.
	brds := append([]schema.BRD(nil), in.BRDs...)
	sort.Slice(brds, func(i, j int) bool { return brds[i].ID < brds[j].ID })
	if brds == nil {
		brds = []schema.BRD{}
	}

	return schema.KnowledgeGraph{
		SchemaVersion:       buildinfo.SchemaVersion,
		GeneratedAt:         gen.UTC().Format(time.RFC3339),
		GeneratedFromCommit: opts.Commit,
		BRDs:                brds,
		Features:            manifests,
		Symbols:             symbols,
		Tests:               tests,
		Modules:             modules,
		Surfaces:            surfaces,
		Errors:              errors,
		Initiatives:         initiatives,
		Tasks:               tasks,
		StructuralChecks:    checks,
		CodeGraph:           schema.CodeGraph{IndexedBy: "scip", LanguageIndexes: opts.LanguageIndexes},
		Violations:          violations,
		Review:              in.Review,
	}
}

// resolveModule lifts a module's file-scope annotations to graph form.
func resolveModule(mod *ir.Module) schema.GraphModule {
	gm := schema.GraphModule{File: mod.File, Language: mod.Language, LineCount: mod.LineCount}
	enf := newStrSet()
	dep := newStrSet()
	for _, a := range mod.ModuleAnnotations {
		switch a.Kind {
		case "module_feature", "feature":
			if v := firstString(a); v != "" {
				gm.Feature = v
			}
		case "module_enforces_invariant", "enforces_invariant":
			enf.addAll(annStrings(a))
		case "module_depends_on_feature", "depends_on_feature":
			dep.addAll(annStrings(a))
		}
	}
	gm.EnforcesInvariants = enf.sorted()
	gm.DependsOnFeatures = dep.sorted()
	return gm
}

// hydrateManifests populates the auto-derived manifest fields: children,
// implementations, and verifications.
func hydrateManifests(manifests []schema.Manifest, symbols, tests []schema.GraphSymbol) []schema.Manifest {
	ids := make(map[string]bool, len(manifests))
	for _, m := range manifests {
		ids[m.ID] = true
	}

	out := make([]schema.Manifest, len(manifests))
	copy(out, manifests)

	for i := range out {
		m := &out[i]
		m.Children = childrenOf(m.ID, ids)
		m.Implementations = nil
		m.Verifications = nil

		for _, s := range symbols {
			if s.Feature != m.ID || !isImplementationEdge(s) {
				continue
			}
			m.Implementations = append(m.Implementations, schema.Implementation{
				Symbol: s.FQN, File: s.File, Line: s.Line,
				Language: s.Language, ViaCapabilities: s.Capabilities,
			})
		}
		for _, t := range tests {
			refs := verificationsForFeature(t, m.ID)
			if len(refs) == 0 {
				continue
			}
			m.Verifications = append(m.Verifications, schema.Verification{
				Symbol: t.FQN, File: t.File, Line: t.Line,
				Language: t.Language, Verifies: refs,
			})
		}
		sort.Slice(m.Implementations, func(a, b int) bool {
			return m.Implementations[a].Symbol < m.Implementations[b].Symbol
		})
		sort.Slice(m.Verifications, func(a, b int) bool {
			return m.Verifications[a].Symbol < m.Verifications[b].Symbol
		})
	}
	return out
}

// implementationKinds are the symbol kinds that can implement a feature.
// Type-only declarations (interfaces, type aliases) are excluded.
var implementationKinds = map[string]bool{
	string(ir.KindClass):    true,
	string(ir.KindFunction): true,
	string(ir.KindMethod):   true,
	string(ir.KindTrait):    true,
}

// isImplementationEdge reports whether a symbol should appear among a
// feature's implementation edges. It must be an implementation-bearing kind
// (not an interface) and part of its module's public surface — private
// helpers stay in the graph but are not feature implementations.
func isImplementationEdge(s schema.GraphSymbol) bool {
	return implementationKinds[s.Kind] && s.Exported
}

// childrenOf returns the immediate dot-nested children of a feature id.
func childrenOf(id string, all map[string]bool) []string {
	var kids []string
	prefix := id + "."
	for candidate := range all {
		if !strings.HasPrefix(candidate, prefix) {
			continue
		}
		rest := candidate[len(prefix):]
		if !strings.Contains(rest, ".") {
			kids = append(kids, candidate)
		}
	}
	sort.Strings(kids)
	return kids
}

// verificationsForFeature returns the verify-refs of a test that belong to
// the given feature. A ref "feature:INV" matches by prefix; a bare ref
// matches when the test's own feature is the feature.
func verificationsForFeature(t schema.GraphSymbol, feature string) []string {
	var out []string
	for _, ref := range t.Verifies {
		if f, _, ok := splitRef(ref); ok {
			if f == feature {
				out = append(out, ref)
			}
			continue
		}
		if t.Feature == feature {
			out = append(out, ref)
		}
	}
	sort.Strings(out)
	return out
}

// splitRef splits "feature:INV-N" into its parts.
func splitRef(ref string) (feature, item string, ok bool) {
	if i := strings.LastIndex(ref, ":"); i > 0 {
		return ref[:i], ref[i+1:], true
	}
	return "", ref, false
}

// collectStructuralChecks hoists every manifest's structural checks to graph
// level, tagged with the owning feature.
func collectStructuralChecks(manifests []schema.Manifest) []schema.GraphStructuralCheck {
	var checks []schema.GraphStructuralCheck
	for _, m := range manifests {
		for _, c := range m.StructuralChecks {
			checks = append(checks, schema.GraphStructuralCheck{
				ID: c.ID, Feature: m.ID, Command: c.Command,
				VerifiesInvariants: c.VerifiesInvariants, Scope: c.Scope,
			})
		}
	}
	sort.Slice(checks, func(i, j int) bool {
		if checks[i].Feature != checks[j].Feature {
			return checks[i].Feature < checks[j].Feature
		}
		return checks[i].ID < checks[j].ID
	})
	return checks
}

// computeUnblocks fills each task's Unblocks as the inverse of depends_on.
func computeUnblocks(tasks []schema.Task) []schema.Task {
	out := make([]schema.Task, len(tasks))
	copy(out, tasks)

	unblocks := map[string][]string{}
	for _, t := range out {
		for _, dep := range t.DependsOn {
			if dep.Task != "" {
				unblocks[dep.Task] = append(unblocks[dep.Task], t.ID)
			}
		}
	}
	for i := range out {
		u := unblocks[out[i].ID]
		sort.Strings(u)
		out[i].Unblocks = u
	}
	return out
}

func sortGraphSymbols(s []schema.GraphSymbol) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].FQN != s[j].FQN {
			return s[i].FQN < s[j].FQN
		}
		return s[i].File < s[j].File
	})
}

func sortViolations(v []schema.Violation) {
	sort.SliceStable(v, func(i, j int) bool {
		fi, fj := "", ""
		if v[i].Location != nil {
			fi = v[i].Location.File
		}
		if v[j].Location != nil {
			fj = v[j].Location.File
		}
		if fi != fj {
			return fi < fj
		}
		return v[i].Code < v[j].Code
	})
}
