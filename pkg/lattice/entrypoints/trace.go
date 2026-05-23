package entrypoints

import (
	"path"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/importer"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Trace populates each entry point's Flow with the features its handler
// reaches. v0.3.0-β ships a *module-proximity* tracer: a feature is
// reached when one of its implementation symbols shares the handler's
// class, file, or enclosing directory.
//
// That is intentionally coarser than a full call-graph walk. SCIP-backed
// transitive tracing lands in v0.3.1 — promoting the same data shape,
// not replacing it. Module proximity is right for the dominant Laravel
// pattern of "controller calls service in the same Module/<Name>/"
// without requiring an installed indexer.
//
// The capability per step is picked by the v0.2.1 token-overlap matcher
// from the implementation symbols whose names best match a cap on the
// reached feature — same heuristic, different consumer.
func Trace(eps []schema.EntryPoint, features []schema.Manifest) []schema.EntryPoint {
	if len(eps) == 0 || len(features) == 0 {
		return eps
	}
	indexes := make([]featureIndex, 0, len(features))
	for _, f := range features {
		ix := featureIndex{
			feature: f.ID, caps: f.Capabilities,
			byClass: map[string]bool{}, byFile: map[string]bool{}, byDir: map[string]bool{},
		}
		for _, impl := range f.Implementations {
			ix.implSyms = append(ix.implSyms, impl.Symbol)
			if cls, _, ok := splitFQNMethod(impl.Symbol); ok {
				ix.byClass[cls] = true
			} else {
				ix.byClass[impl.Symbol] = true
			}
			if impl.File != "" {
				ix.byFile[impl.File] = true
				ix.byDir[path.Dir(impl.File)] = true
			}
		}
		indexes = append(indexes, ix)
	}

	out := make([]schema.EntryPoint, len(eps))
	copy(out, eps)
	for i := range out {
		flow, sideEffects := traceOne(out[i], indexes)
		out[i].Flow = flow
		out[i].SideEffects = sideEffects
	}
	return out
}

// featureIndex is one feature's implementation footprint, precomputed
// once and reused per entry point during the tracing pass.
type featureIndex struct {
	feature  string
	caps     []schema.Capability
	byClass  map[string]bool
	byFile   map[string]bool
	byDir    map[string]bool
	implSyms []string
}

func traceOne(ep schema.EntryPoint, indexes []featureIndex) ([]schema.FlowStep, []schema.SideEffect) {

	cls, _, _ := splitFQNMethod(ep.Handler.Symbol)
	handlerDir := ""
	if ep.Handler.File != "" {
		handlerDir = path.Dir(ep.Handler.File)
	}
	// Same-module match goes 2 directory levels up — covers the typical
	// Laravel Module/<Name>/Http/Controllers/X reaching Module/<Name>/
	// Services/Y or Module/<Name>/Actions/Z.
	handlerModule := upN(handlerDir, 2)

	var steps []schema.FlowStep
	for _, ix := range indexes {
		matched, why := false, ""
		switch {
		case cls != "" && ix.byClass[cls]:
			matched, why = true, "class"
		case handlerDir != "" && ix.byDir[handlerDir]:
			matched, why = true, "directory"
		case handlerDir != "" && hasDirPrefix(ix.byDir, handlerModule):
			matched, why = true, "module"
		}
		if !matched {
			continue
		}
		// Pick the implementation symbols that justify the link; show
		// up to 3 in via_symbols as breadcrumbs.
		via := pickVia(ep.Handler, ix.implSyms, why)
		// Run the cap-matcher across the impl set so the flow step can
		// name a specific capability when one stands out.
		capID := bestCapability(via, ix.caps)
		steps = append(steps, schema.FlowStep{
			Feature: ix.feature, Capability: capID, ViaSymbols: via,
		})
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].Feature < steps[j].Feature })
	// v0.3.0-β side-effect detection is intentionally minimal: an HTTP
	// route is assumed to write to "http" (the request/response cycle).
	// Richer detection (ORM persistence sites, Guzzle calls, dispatch())
	// lands when the flow tracer gains call-graph access in v0.3.1.
	return steps, nil
}

// pickVia returns up to 3 implementation symbols that share something
// with the handler — preferring same-class methods, then same-file
// symbols. The result is a debugging breadcrumb in the flow step.
func pickVia(handler schema.Handler, impls []string, why string) []string {
	handlerCls, _, _ := splitFQNMethod(handler.Symbol)
	scored := make([]string, 0, len(impls))
	for _, s := range impls {
		if c, _, ok := splitFQNMethod(s); ok && c == handlerCls {
			scored = append(scored, s)
		}
	}
	if len(scored) == 0 {
		scored = impls
	}
	sort.Strings(scored)
	if len(scored) > 3 {
		scored = scored[:3]
	}
	return scored
}

// bestCapability runs the v0.2.1 token-overlap matcher over the via
// symbols and returns the cap id with the most assignments, or "" if
// none cross the threshold.
func bestCapability(symbols []string, caps []schema.Capability) string {
	if len(caps) == 0 || len(symbols) == 0 {
		return ""
	}
	assigned := importer.MatchCapabilities(symbols, caps)
	best, bestCount := "", 0
	for id, fqns := range assigned {
		if len(fqns) > bestCount || (len(fqns) == bestCount && id < best) {
			best, bestCount = id, len(fqns)
		}
	}
	return best
}

// upN returns the n-th parent of dir; "" if dir is empty.
func upN(dir string, n int) string {
	if dir == "" {
		return ""
	}
	for i := 0; i < n; i++ {
		dir = path.Dir(dir)
		if dir == "." || dir == "/" {
			break
		}
	}
	return dir
}

// hasDirPrefix reports whether any key in dirs is a descendant of root.
func hasDirPrefix(dirs map[string]bool, root string) bool {
	if root == "" || root == "." {
		return false
	}
	for d := range dirs {
		if d == root || strings.HasPrefix(d, root+"/") {
			return true
		}
	}
	return false
}

// splitFQNMethod splits "Ns\Class::method" into ("Ns\Class", "method").
func splitFQNMethod(fqn string) (string, string, bool) {
	if i := strings.LastIndex(fqn, "::"); i >= 0 {
		return fqn[:i], fqn[i+2:], true
	}
	return "", "", false
}
