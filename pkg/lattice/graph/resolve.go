package graph

import (
	"sort"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// roleInfo is a manifest-declared role flattened for lookup.
type roleInfo struct {
	invariants []string
}

// resolver computes the effective annotation set of every symbol, applying
// module-level, role-based, and class-inheritance propagation (sections 16-18).
type resolver struct {
	roles map[string]roleInfo // role id -> declaring feature + invariants
	byFQN map[string]*ir.Symbol
}

func newResolver(manifests []schema.Manifest, modules []ir.Module) *resolver {
	r := &resolver{
		roles: map[string]roleInfo{},
		byFQN: map[string]*ir.Symbol{},
	}
	for _, m := range manifests {
		for _, role := range m.Roles {
			r.roles[role.ID] = roleInfo{invariants: role.AppliesInvariants}
		}
	}
	for i := range modules {
		mod := &modules[i]
		for j := range mod.Symbols {
			s := &mod.Symbols[j]
			r.byFQN[s.FQN] = s
		}
	}
	return r
}

// resolved holds a symbol's fully propagated annotation set.
type resolved struct {
	feature      string
	capabilities []string
	enforces     []string
	dependsOn    []string
	roles        []string
	verifies     []string
	suppressed   []schema.SuppressedInvariant
}

// resolveSymbol returns the effective annotation set for one symbol.
func (r *resolver) resolveSymbol(sym *ir.Symbol, mod *ir.Module) resolved {
	var res resolved
	enf := newStrSet()
	dep := newStrSet()
	cap := newStrSet()
	rol := newStrSet()
	ver := newStrSet()
	supp := map[string]string{}

	// 1. Module-level annotations (lowest precedence for feature; union for sets).
	moduleFeature := ""
	for _, a := range mod.ModuleAnnotations {
		switch a.Kind {
		case "module_feature", "feature":
			if v := firstString(a); v != "" {
				moduleFeature = v
			}
		case "module_enforces_invariant", "enforces_invariant":
			enf.addAll(annStrings(a))
		case "module_depends_on_feature", "depends_on_feature":
			dep.addAll(annStrings(a))
		}
	}

	// 2. Inheritance chain: collect this symbol, its enclosing class, and all
	//    transitive base classes. Earlier entries win for `feature`.
	chain := r.annotationChain(sym)
	feature := ""
	for _, link := range chain {
		for _, a := range link.Annotations {
			switch a.Kind {
			case "feature":
				if feature == "" {
					if v := firstString(a); v != "" {
						feature = v
					}
				}
				// feature annotation may carry capability kwargs.
				if c, ok := a.Kwargs["capability"]; ok {
					cap.addAll(toStrings(c))
				}
			case "capability", "feature_capability":
				cap.addAll(annStrings(a))
			case "enforces_invariant":
				enf.addAll(annStrings(a))
			case "depends_on_feature":
				dep.addAll(annStrings(a))
			case "role":
				rol.addAll(annStrings(a))
			case "verifies", "verifies_capability":
				ver.addAll(annStrings(a))
			case "suppresses_invariant":
				inv := firstString(a)
				reason, _ := a.Kwargs["reason"].(string)
				if reason == "" && len(a.Args) > 1 {
					reason = toString(a.Args[1])
				}
				if inv != "" {
					supp[inv] = reason
				}
			}
		}
	}
	if feature == "" {
		feature = moduleFeature
	}

	// 3. Role expansion: each role contributes its invariants.
	for _, roleID := range rol.sorted() {
		if ri, ok := r.roles[roleID]; ok {
			enf.addAll(ri.invariants)
		}
	}

	// 4. Suppression: remove suppressed invariants from the enforced set and
	//    record them loudly.
	for inv, reason := range supp {
		enf.remove(inv)
		res.suppressed = append(res.suppressed, schema.SuppressedInvariant{Invariant: inv, Reason: reason})
	}
	sort.Slice(res.suppressed, func(i, j int) bool {
		return res.suppressed[i].Invariant < res.suppressed[j].Invariant
	})

	res.feature = feature
	res.capabilities = cap.sorted()
	res.enforces = enf.sorted()
	res.dependsOn = dep.sorted()
	res.roles = rol.sorted()
	res.verifies = ver.sorted()
	return res
}

// annotationChain returns the symbol plus its enclosing class and transitive
// base classes, nearest first. Class annotations thus propagate to methods and
// base-class annotations to subclasses.
func (r *resolver) annotationChain(sym *ir.Symbol) []*ir.Symbol {
	var chain []*ir.Symbol
	seen := map[string]bool{}

	var visit func(s *ir.Symbol)
	visit = func(s *ir.Symbol) {
		if s == nil || seen[s.FQN] {
			return
		}
		seen[s.FQN] = true
		chain = append(chain, s)
		for _, base := range s.BaseClasses {
			visit(r.byFQN[base])
		}
	}
	visit(sym)
	if sym.EnclosingFQN != "" {
		visit(r.byFQN[sym.EnclosingFQN])
	}
	return chain
}

// --- small helpers ---

type strSet struct {
	order []string
	have  map[string]bool
}

func newStrSet() *strSet { return &strSet{have: map[string]bool{}} }

func (s *strSet) add(v string) {
	if v == "" || s.have[v] {
		return
	}
	s.have[v] = true
	s.order = append(s.order, v)
}

func (s *strSet) addAll(vs []string) {
	for _, v := range vs {
		s.add(v)
	}
}

func (s *strSet) remove(v string) {
	if !s.have[v] {
		return
	}
	delete(s.have, v)
	out := s.order[:0]
	for _, x := range s.order {
		if x != v {
			out = append(out, x)
		}
	}
	s.order = out
}

func (s *strSet) sorted() []string {
	out := append([]string(nil), s.order...)
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

// annStrings flattens an annotation's positional args to strings.
func annStrings(a ir.Annotation) []string { return toStrings(a.Args) }

// firstString returns the first positional arg as a string.
func firstString(a ir.Annotation) string {
	if len(a.Args) == 0 {
		return ""
	}
	return toString(a.Args[0])
}

func toStrings(v interface{}) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []string:
		return t
	case []interface{}:
		var out []string
		for _, x := range t {
			out = append(out, toStrings(x)...)
		}
		return out
	default:
		return nil
	}
}

func toString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
