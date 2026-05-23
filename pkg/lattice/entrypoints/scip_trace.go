package entrypoints

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/scip"
)

// TraceWithSCIP is v0.3.1's transitive flow tracer. When a SCIP
// corpus is available it BFS-walks the call graph from each entry-
// point's handler, recording every feature-annotated symbol it
// reaches as a FlowStep. When SCIP is empty or the handler doesn't
// resolve into a SCIP moniker, it falls back to the v0.3.0
// module-proximity tracer so the flow graph is never worse than
// before — only better when an index is present.
//
// The depth cap is intentional: a deep transitive walk through a
// large codebase can produce hundreds of "reached" features per EP.
// Eight hops covers the common controller → service → repository →
// model chain without exploding into unrelated parts of the system.
const defaultSCIPTraceDepth = 8

// TraceWithSCIP populates Flow on each EP, preferring SCIP-derived
// transitive callees when the corpus has them and falling back to
// module proximity otherwise. The caller passes in a built SCIP
// CallGraph (nil = skip the SCIP branch entirely).
func TraceWithSCIP(eps []schema.EntryPoint, features []schema.Manifest, corpus *scip.Corpus) []schema.EntryPoint {
	// Always run the v0.3.0 tracer first — its module-proximity hits
	// are still useful even when SCIP also produced hits (the two
	// signals stack rather than fighting).
	out := Trace(eps, features)
	if corpus == nil || corpus.Empty() {
		return out
	}
	graph := corpus.BuildCallGraph()
	if graph == nil {
		return out
	}
	// Build a symbol -> (feature, capability) lookup from features.
	type loc struct{ feature, capability string }
	bySymbol := map[string]loc{}
	for _, f := range features {
		for _, impl := range f.Implementations {
			bySymbol[impl.Symbol] = loc{feature: f.ID}
			// Capability sub-link when the implementation declares one.
			if len(impl.ViaCapabilities) > 0 {
				bySymbol[impl.Symbol] = loc{feature: f.ID, capability: impl.ViaCapabilities[0]}
			}
		}
	}

	for i := range out {
		ep := &out[i]
		scipSym := corpus.ResolveSymbol(ep.Handler.Symbol)
		if scipSym == "" {
			continue
		}
		reached := graph.TransitiveCallees(scipSym, defaultSCIPTraceDepth)
		if len(reached) == 0 {
			continue
		}
		// Merge SCIP hits into existing flow, deduping by feature.
		existing := map[string]bool{}
		for _, s := range ep.Flow {
			existing[s.Feature] = true
		}
		for _, callee := range reached {
			// Match the SCIP moniker back to a Lattice symbol via
			// the suffix-match join key used elsewhere.
			fqn := monikerToFQN(callee)
			if fqn == "" {
				continue
			}
			// Try direct FQN; fall back to looking through bySymbol
			// by suffix.
			lookup := bySymbol[fqn]
			if lookup.feature == "" {
				for sym, l := range bySymbol {
					if strings.HasSuffix(strings.ToLower(sym), strings.ToLower(fqn)) {
						lookup = l
						break
					}
				}
			}
			if lookup.feature == "" || existing[lookup.feature] {
				continue
			}
			existing[lookup.feature] = true
			ep.Flow = append(ep.Flow, schema.FlowStep{
				Feature:    lookup.feature,
				Capability: lookup.capability,
				ViaSymbols: []string{fqn},
			})
		}
		sort.Slice(ep.Flow, func(a, b int) bool { return ep.Flow[a].Feature < ep.Flow[b].Feature })
	}
	return out
}

// monikerToFQN strips the leading scheme prefix off a SCIP symbol
// ("scip-php php . . . App.Foo.bar()") and converts the descriptor
// back to a Lattice-shaped FQN. Best-effort; fall back to the
// trailing identifier when the format is unfamiliar.
func monikerToFQN(scipSym string) string {
	// SCIP symbols look like: "<scheme> <manager> <pkg> <version> <descriptors>".
	// Descriptors use ., #, (), [], . Strip the prefix on the first
	// space-separated cluster ending with the package version.
	parts := strings.SplitN(scipSym, " ", 5)
	if len(parts) < 5 {
		return strings.TrimRight(scipSym, "().")
	}
	desc := parts[4]
	// Strip trailing () that SCIP appends to method/function symbols.
	desc = strings.TrimRight(desc, "().# ")
	// Replace . with \\ for PHP-like joins; the suffix match handles
	// the rest. This is approximate by design.
	desc = strings.ReplaceAll(desc, "/", string(filepath.Separator))
	return desc
}
