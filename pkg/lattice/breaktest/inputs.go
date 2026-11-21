package breaktest

// @module-feature breaktest

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// treeFile is one file of HEAD's tree: its path relative to the code root
// and its blob sha.
type treeFile struct {
	path string
	blob string
}

// inputsHash names what a run's outcome was measured on, for its cache key.
//
// By default that is HEAD's tree: an outcome is a measurement of one tree,
// and a change anywhere in it (a migration the runner applies, a fixture, a
// seed) can turn a kill into a baseline that does not run. A dirty tree has
// no tree and gets "": its outcome is neither reused nor kept for reuse.
//
// When every runner group of the run declares break.runners[].inputs, the
// outcome is measured on those files instead: the hash covers the files of
// HEAD's tree that match the union of the declared globs, plus the enforcer's
// and verifiers' own files, each by blob sha. The enforcer and verifier files
// are added because a change elsewhere in them can matter to the run
// although its span hashes do not see it; a run that declares inputs reuses
// less often for it, never wrongly. A run with any group that declares none
// (a Go group, a break.commands template, a runner without inputs), or a
// declared glob that is not valid, falls back to the tree.
// @enforces breaktest:INV-16
func (r *runner) inputsHash(ctx context.Context, t target, groups []group) string {
	if r.tree == "" {
		return ""
	}
	tree := "tree:" + r.tree
	globs, ok := declaredInputs(groups)
	if !ok {
		return tree
	}
	for _, g := range globs {
		if !doublestar.ValidatePattern(g) {
			fmt.Fprintf(r.opts.warn(), "break: invalid inputs glob %q: reusing outcomes on the same tree only\n", g)
			return tree
		}
	}
	files, err := r.treeFiles(ctx)
	if err != nil {
		return tree
	}
	implicit := map[string]bool{}
	for _, e := range t.enforcers {
		implicit[e.File] = true
	}
	if t.hop != nil {
		for _, st := range t.hop.Sites {
			implicit[st.File] = true
		}
	}
	for _, v := range t.tests {
		implicit[v.File] = true
	}
	var lines []string
	for _, f := range files {
		in := implicit[f.path]
		for _, g := range globs {
			if in {
				break
			}
			in, _ = doublestar.Match(g, f.path)
		}
		if in {
			lines = append(lines, f.path+"\x00"+f.blob)
		}
	}
	sort.Strings(lines)
	return "inputs:" + sha([]byte(strings.Join(lines, "\n")))
}

// declaredInputs returns the union of the groups' inputs globs, and whether
// every group declares some. A run with no group declares none.
func declaredInputs(groups []group) ([]string, bool) {
	if len(groups) == 0 {
		return nil, false
	}
	seen := map[string]bool{}
	var out []string
	for _, g := range groups {
		if g.kind != kindRunner || len(g.runner.Inputs) == 0 {
			return nil, false
		}
		for _, in := range g.runner.Inputs {
			if !seen[in] {
				seen[in] = true
				out = append(out, in)
			}
		}
	}
	sort.Strings(out)
	return out, true
}

// treeFiles lists HEAD's files under the code root with their blob shas, read
// once per run from the object database without reading the files.
func (r *runner) treeFiles(ctx context.Context) ([]treeFile, error) {
	if r.listed {
		return r.listing, r.listingErr
	}
	r.listed = true
	out, err := git(ctx, r.repo.Top, "ls-tree", "-r", "-z", "--full-tree", "HEAD")
	if err != nil {
		r.listingErr = err
		return nil, err
	}
	r.listing = parseTree(out, r.repo.Prefix)
	return r.listing, nil
}

// parseTree reads `git ls-tree -r -z` output ("<mode> <type> <sha>\t<path>"
// entries separated by NUL) and keeps the files under prefix, with paths
// relative to it.
func parseTree(out, prefix string) []treeFile {
	var files []treeFile
	for _, ent := range strings.Split(out, "\x00") {
		meta, p, ok := strings.Cut(ent, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) != 3 {
			continue
		}
		if prefix = filepath.ToSlash(prefix); prefix != "" {
			if p, ok = strings.CutPrefix(p, prefix+"/"); !ok {
				continue
			}
		}
		files = append(files, treeFile{path: p, blob: fields[2]})
	}
	return files
}

// baselineKnownFailing reports whether any of the run's baseline invocations
// is known not to pass at this tree: it failed earlier in this run, or an
// earlier run at the same tree kept it as failed. A cached outcome is never
// reused over one, whatever the inputs say: it guards a declared input list
// that is too narrow.
// @enforces breaktest:INV-16
func (r *runner) baselineKnownFailing(groups []group) bool {
	for _, g := range groups {
		key := r.keyOf(g)
		if inv, ok := r.shared[key]; ok && inv.failedBaseline() {
			return true
		}
		if r.store.failed(key) {
			return true
		}
	}
	return false
}
