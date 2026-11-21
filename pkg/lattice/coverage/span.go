package coverage

// @module-feature coverage

import (
	"sort"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// LayerSpan is the set of layers of the symbols a test's own run executed,
// sorted. A symbol the graph knows with no layer counts as unknown. A symbol
// the graph does not know is not counted.
//
// ok is false when the test has no per-test coverage: its span is then
// unknown, and is never inferred from the file the test lives in or from a
// run of the whole file. A test with a per-test report that executed nothing
// has ok true and an empty span.
func (ix *Index) LayerSpan(test string) (layers []string, ok bool) {
	ran, has := ix.perTest[test]
	if !has {
		return nil, false
	}
	seen := map[string]bool{}
	for sym := range ran {
		l, known := ix.layer[sym]
		if !known {
			continue
		}
		if l == "" {
			l = schema.LayerUnknown
		}
		seen[l] = true
	}
	layers = make([]string, 0, len(seen))
	for l := range seen {
		layers = append(layers, l)
	}
	sort.Strings(layers)
	return layers, true
}
