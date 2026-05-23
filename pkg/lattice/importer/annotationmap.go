package importer

import (
	"os"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// AnnotationMapFileName is the sidecar feature↔code map under the import dir.
const AnnotationMapFileName = "annotation-map.yaml"

const annotationMapVersion = 1

// AnnotationMap is the sidecar that links features to the code symbols that
// implement them, without touching source. The graph builder merges it as if
// the annotations had been written inline — so adopting Lattice no longer
// requires a giant code-mod PR.
type AnnotationMap struct {
	Version  int                    `yaml:"version"`
	Features []AnnotationMapFeature  `yaml:"features"`
}

// AnnotationMapFeature maps one feature id to its implementing symbol FQNs,
// optionally with per-capability symbol assignments. Capabilities is the
// v0.2.1 addition that lets sidecar mode produce capability edges (not
// just feature edges) so the 172 UNIMPLEMENTED_CAPABILITY warnings the
// dogfood revealed are auto-resolved for confident matches.
type AnnotationMapFeature struct {
	ID           string              `yaml:"id"`
	Symbols      []string            `yaml:"symbols"`
	Capabilities map[string][]string `yaml:"capabilities,omitempty"`
}

// LoadAnnotationMap reads the sidecar map from path.
func LoadAnnotationMap(path string) (AnnotationMap, error) {
	var am AnnotationMap
	data, err := os.ReadFile(path)
	if err != nil {
		return am, err
	}
	err = yaml.Unmarshal(data, &am)
	return am, err
}

// SaveAnnotationMap writes the sidecar map to path, sorted for stable diffs.
func SaveAnnotationMap(path string, am AnnotationMap) error {
	am.Version = annotationMapVersion
	sort.Slice(am.Features, func(i, j int) bool { return am.Features[i].ID < am.Features[j].ID })
	for i := range am.Features {
		am.Features[i].Symbols = sortedUnique(am.Features[i].Symbols)
	}
	data, err := yaml.Marshal(am)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ApplyAnnotationMap overlays the sidecar map onto parsed modules: every
// symbol the map names gains a synthetic `feature` annotation (and, when
// the sidecar assigns it, a synthetic `capability` annotation too), exactly
// as if it had been annotated in source. Any in-code annotations stay
// first in the list, so an explicit @feature still wins. Mutates modules
// in place.
func ApplyAnnotationMap(modules []ir.Module, am AnnotationMap) {
	feature := map[string]string{}
	caps := map[string][]string{} // FQN -> capability ids
	for _, f := range am.Features {
		for _, s := range f.Symbols {
			if _, exists := feature[s]; !exists {
				feature[s] = f.ID
			}
		}
		for capID, fqns := range f.Capabilities {
			for _, s := range fqns {
				caps[s] = append(caps[s], capID)
				// Capability implies feature membership even if the
				// flat Symbols list didn't include this FQN.
				if _, exists := feature[s]; !exists {
					feature[s] = f.ID
				}
			}
		}
	}
	if len(feature) == 0 {
		return
	}
	for mi := range modules {
		for si := range modules[mi].Symbols {
			sym := &modules[mi].Symbols[si]
			if fid, ok := feature[sym.FQN]; ok {
				sym.Annotations = append(sym.Annotations, ir.Annotation{
					Kind: "feature",
					Args: []interface{}{fid},
				})
			}
			for _, capID := range caps[sym.FQN] {
				sym.Annotations = append(sym.Annotations, ir.Annotation{
					Kind: "capability",
					Args: []interface{}{capID},
				})
			}
		}
	}
}

func sortedUnique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
