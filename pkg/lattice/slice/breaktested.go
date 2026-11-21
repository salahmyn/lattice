package slice

// @module-feature reachability

import "sort"

// EdgeRun is one edge break of a hop, as `lattice break --edges` recorded it:
// the hop whose out-end key was altered, how it ended, and the verifiers
// that failed. The caller reads it from breaks.json, so the slice names no
// provider.
type EdgeRun struct {
	From, To, Kind string
	// Killed is true only for an outcome of killed. A survived or unknown run
	// shows nothing about the hop and is not passed at all.
	Killed bool
	// By are the verifiers that failed after the break.
	By []string
}

// MarkBreakTested marks, on the listed paths, each resolved hop an edge
// break killed: break_tested, with the verifiers that failed. A hop is
// marked only for a run of that hop whose outcome was killed; a survived or
// unknown run, a hop with no run and an unknown hop are never marked, so an
// absent field means nothing was shown. Without any edge run the record is
// left as it was.
// @enforces breaktest:INV-19
func MarkBreakTested(rec *Record, runs []EdgeRun) {
	if len(runs) == 0 {
		return
	}
	by := map[[3]string]map[string]bool{}
	for _, r := range runs {
		if !r.Killed {
			continue
		}
		k := [3]string{r.From, r.To, r.Kind}
		if by[k] == nil {
			by[k] = map[string]bool{}
		}
		for _, v := range r.By {
			by[k][v] = true
		}
	}
	for pi := range rec.Paths {
		p := &rec.Paths[pi]
		n := 0
		for i := range p.Hops {
			h := &p.Hops[i]
			h.BreakTested, h.BreakTestedBy = false, nil
			if h.Truth != TruthResolved {
				continue
			}
			vs, ok := by[[3]string{h.From, h.To, h.Kind}]
			if !ok {
				continue
			}
			for v := range vs {
				h.BreakTestedBy = append(h.BreakTestedBy, v)
			}
			sort.Strings(h.BreakTestedBy)
			if len(h.BreakTestedBy) > MaxExercisedBy {
				h.BreakTestedBy = h.BreakTestedBy[:MaxExercisedBy]
			}
			h.BreakTested = true
			n++
		}
		p.BreakTestedHops = &n
	}
}
