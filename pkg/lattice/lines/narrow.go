package lines

// @module-feature audit

import "sort"

// Narrow keeps the blocks of one feature, and those whose intent names one
// of the promises, and recomputes the totals over what is kept. Like the
// audit's own filters it only narrows what is shown: the blocks and their
// levels were computed over the whole workspace. A filter that is empty
// keeps everything; with promises, blocks with no intent are dropped.
func Narrow(r Report, feature string, promises []string, byPromise bool) Report {
	if feature == "" && !byPromise {
		return r
	}
	want := map[string]bool{}
	for _, p := range promises {
		want[p] = true
	}
	out := Report{Schema: r.Schema, Inputs: r.Inputs, Features: []Feature{}, Blocks: []Block{}, Narrowed: true}
	feat := map[string]*Feature{}
	for _, b := range r.Blocks {
		if feature != "" && b.Feature != feature {
			continue
		}
		if byPromise {
			named := false
			for _, i := range b.Intent.Invariants {
				named = named || want[i.Invariant]
			}
			if !named {
				continue
			}
		}
		out.Blocks = append(out.Blocks, b)
		f := feat[b.Feature]
		if f == nil {
			f = &Feature{Feature: b.Feature}
			feat[b.Feature] = f
		}
		if b.Intent.Kind == IntentNone {
			f.NoIntent.add(b.Levels)
			out.Workspace.NoIntent.add(b.Levels)
		} else {
			f.WithIntent.add(b.Levels)
			out.Workspace.WithIntent.add(b.Levels)
		}
	}
	out.Workspace.finish()
	names := make([]string, 0, len(feat))
	for k := range feat {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		feat[k].finish()
		out.Features = append(out.Features, *feat[k])
	}
	return out
}
