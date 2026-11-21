package reach

// @module-feature reachability

// importLanguages are the languages whose adapters record imports, so that
// "which modules load together" is known. A language outside the set (PHP
// records none) keeps every by-name candidate, and Go keeps its package
// visibility rule, since a Go program is linked from packages rather than
// loaded from an entry module.
var importLanguages = map[string]bool{"typescript": true, "python": true}

// coloader answers whether two modules can be loaded in the same program:
// some module, possibly one of the two, transitively imports both. Each
// module's ancestors (the modules that transitively import it, itself
// included) are computed on first use and kept as a bitset; two modules are
// co-loaded when their ancestor sets intersect.
type coloader struct {
	importedBy [][]int // module -> modules that import it
	isTest     []bool
	words      int
	// anc[0] counts every module as an importer; anc[1] only the non-test
	// ones, which is what a production caller is judged by.
	anc [2][][]uint64
}

func newColoader(g *Graph) *coloader {
	n := len(g.modules)
	c := &coloader{importedBy: make([][]int, n), isTest: make([]bool, n), words: (n + 63) / 64}
	c.anc[0], c.anc[1] = make([][]uint64, n), make([][]uint64, n)
	for mi := range g.modules {
		m := &g.modules[mi]
		if t, ok := g.tops[mi]; ok {
			c.isTest[mi] = g.nodes[t].isTest
		} else if len(m.Symbols) > 0 {
			c.isTest[mi] = true
			for _, s := range m.Symbols {
				if !s.IsTest {
					c.isTest[mi] = false
					break
				}
			}
		}
		seen := map[int]bool{}
		for _, im := range m.Imports {
			if im.Module == "" {
				continue
			}
			for _, to := range g.byNS[im.Module] {
				if to != mi && !seen[to] {
					seen[to] = true
					c.importedBy[to] = append(c.importedBy[to], mi)
				}
			}
		}
	}
	return c
}

// ancestors returns the modules that transitively import mi, mi included.
// With production set, a test module is no importer.
func (c *coloader) ancestors(mi int, production bool) []uint64 {
	k := 0
	if production {
		k = 1
	}
	if a := c.anc[k][mi]; a != nil {
		return a
	}
	set := make([]uint64, c.words)
	set[mi/64] |= 1 << (mi % 64)
	queue := []int{mi}
	for len(queue) > 0 {
		x := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for _, p := range c.importedBy[x] {
			if set[p/64]&(1<<(p%64)) != 0 || (production && c.isTest[p]) {
				continue
			}
			set[p/64] |= 1 << (p % 64)
			queue = append(queue, p)
		}
	}
	c.anc[k][mi] = set
	return set
}

// coLoaded reports whether a program can load both modules.
func (c *coloader) coLoaded(a, b int, production bool) bool {
	if a == b {
		return true
	}
	x, y := c.ancestors(a, production), c.ancestors(b, production)
	for i := range x {
		if x[i]&y[i] != 0 {
			return true
		}
	}
	return false
}
