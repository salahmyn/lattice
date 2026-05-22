package graph

import (
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// interactionTypes are the surface types treated as user/system interactions.
// The "module" surface type is a structural marker, not an interaction, so it
// is excluded from the interaction inventory.
var interactionTypes = map[string]bool{
	"http":            true,
	"event_emit":      true,
	"event_consume":   true,
	"webhook_receive": true,
	"scheduled":       true,
}

// buildSurfaces fuses manifest-declared surfaces with the surfaces found in
// source — auto-detected framework routes and @surface annotations — into one
// interaction inventory. Each entry records whether it is declared, whether it
// is implemented, and the code sites that implement it.
func buildSurfaces(manifests []schema.Manifest, modules []ir.Module, res *resolver) []schema.GraphSurface {
	byKey := map[string]*schema.GraphSurface{}

	get := func(typ, method, path, name string) *schema.GraphSurface {
		key := surfaceKey(typ, method, path, name)
		gs := byKey[key]
		if gs == nil {
			gs = &schema.GraphSurface{Type: typ, Method: method, Path: path, Name: name}
			byKey[key] = gs
		}
		return gs
	}

	// Declared surfaces from feature manifests.
	for _, m := range manifests {
		for _, s := range m.Surface {
			typ := string(s.Type)
			if !interactionTypes[typ] {
				continue
			}
			name := s.Name
			if typ == "scheduled" {
				name = s.Job
			}
			gs := get(typ, strings.ToUpper(s.Method), s.Path, name)
			gs.Declared = true
			if gs.Feature == "" {
				gs.Feature = m.ID
			}
		}
	}

	// Surfaces found in source.
	for mi := range modules {
		mod := &modules[mi]
		modFeature := resolveModule(mod).Feature

		// Auto-detected framework routes.
		for _, s := range mod.Surfaces {
			if !interactionTypes[s.Type] {
				continue
			}
			gs := get(s.Type, strings.ToUpper(s.Method), s.Path, s.Name)
			gs.Implemented = true
			if gs.Feature == "" {
				gs.Feature = modFeature
			}
			gs.ImplementedBy = append(gs.ImplementedBy, schema.SurfaceImpl{
				File: mod.File, Line: s.Line, Detected: s.Detected,
			})
		}

		// @surface annotations on symbols.
		for si := range mod.Symbols {
			sym := &mod.Symbols[si]
			for _, a := range sym.Annotations {
				if a.Kind != "surface" {
					continue
				}
				cs, ok := surfaceFromAnnotation(a)
				if !ok {
					continue
				}
				gs := get(cs.Type, cs.Method, cs.Path, cs.Name)
				gs.Implemented = true
				if gs.Feature == "" {
					gs.Feature = res.resolveSymbol(sym, mod).feature
				}
				gs.ImplementedBy = append(gs.ImplementedBy, schema.SurfaceImpl{
					File: sym.File, Line: a.Line, Symbol: sym.FQN,
				})
			}
		}
	}

	out := make([]schema.GraphSurface, 0, len(byKey))
	for _, gs := range byKey {
		sort.Slice(gs.ImplementedBy, func(i, j int) bool {
			a, b := gs.ImplementedBy[i], gs.ImplementedBy[j]
			if a.File != b.File {
				return a.File < b.File
			}
			return a.Line < b.Line
		})
		out = append(out, *gs)
	}
	sort.Slice(out, func(i, j int) bool {
		ki := surfaceKey(out[i].Type, out[i].Method, out[i].Path, out[i].Name)
		kj := surfaceKey(out[j].Type, out[j].Method, out[j].Path, out[j].Name)
		return ki < kj
	})
	return out
}

// surfaceKey is the identity of an interaction: method+path for HTTP-like
// surfaces, type+name for events and scheduled jobs.
func surfaceKey(typ, method, path, name string) string {
	if path != "" {
		return typ + "|" + strings.ToUpper(method) + "|" + path
	}
	return typ + "|" + name
}

// surfaceFromAnnotation parses an @surface annotation into an ir.Surface.
// Arguments may arrive space-joined (TypeScript JSDoc) or as separate strings
// (Python/PHP), so all string args are flattened on whitespace first.
func surfaceFromAnnotation(a ir.Annotation) (ir.Surface, bool) {
	var fields []string
	for _, arg := range a.Args {
		if s, ok := arg.(string); ok {
			fields = append(fields, strings.Fields(s)...)
		}
	}
	if len(fields) < 2 {
		return ir.Surface{}, false
	}
	typ := fields[0]
	if !interactionTypes[typ] {
		return ir.Surface{}, false
	}
	surf := ir.Surface{Type: typ, Line: a.Line}
	switch typ {
	case "http", "webhook_receive":
		if len(fields) < 3 {
			return ir.Surface{}, false
		}
		surf.Method = strings.ToUpper(fields[1])
		surf.Path = fields[2]
	default: // event_emit, event_consume, scheduled
		surf.Name = fields[1]
	}
	return surf, true
}
