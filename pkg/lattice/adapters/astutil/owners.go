package astutil

// @module-feature reachability

// Owners maps a byte offset of a source file to the innermost symbol whose
// declaration contains it, so a reference found anywhere in the file is
// credited to the function, method, class or test it sits in. A reference
// inside no symbol belongs to the module's top-level code.
type Owners struct {
	spans []ownerSpan
}

type ownerSpan struct {
	start, end uint32
	idx        int
}

// Add records that the symbol at index idx spans [start, end).
func (o *Owners) Add(start, end uint32, idx int) {
	o.spans = append(o.spans, ownerSpan{start, end, idx})
}

// At returns the index of the innermost symbol containing offset, or -1.
// Of two spans that contain it, the shorter is the inner one; on a tie the
// later-added one wins (a symbol added while walking its parent's body).
// @enforces reachability:INV-1
func (o *Owners) At(offset uint32) int {
	best, bestLen := -1, ^uint32(0)
	for _, s := range o.spans {
		if offset < s.start || offset >= s.end {
			continue
		}
		if l := s.end - s.start; l <= bestLen {
			best, bestLen = s.idx, l
		}
	}
	return best
}
