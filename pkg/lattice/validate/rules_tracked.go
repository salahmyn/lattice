package validate

// @module-feature validation

import (
	"fmt"
	"path"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// checkTrackedDerived warns when git tracks the workspace's derived graph
// (GRAPH_TRACKED) or files under its runtime cache (CACHE_TRACKED). Both are
// rewritten by ordinary runs, so tracking them leaves the tree permanently
// dirty and `lattice break` refuses to run. Paths come from Options.Tracked;
// nil (git unavailable, or not a repository) reports nothing.
// @enforces validation:INV-13
func (c *corpus) checkTrackedDerived() []schema.Violation {
	var v []schema.Violation
	var cache []string
	cacheDir := ""
	for _, p := range c.opts.Tracked {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if i := strings.Index("/"+p, "/.cache/"); i >= 0 {
			cache = append(cache, p)
			if cacheDir == "" {
				// "/.cache/" sits at i in "/"+p, so ".cache" starts at i in p.
				cacheDir = p[:i] + ".cache"
			}
			continue
		}
		if path.Base(p) != "lattice.json" {
			continue
		}
		v = append(v, schema.Violation{
			Code: schema.CodeGraphTracked, Severity: schema.SeverityWarning, Subject: p,
			Message:  fmt.Sprintf("%s is tracked: it is derived and rewritten by every extract, so the tree is never clean and `lattice break` refuses. Untrack it: `git rm --cached %s`, and ignore it.", p, p),
			Location: &schema.Location{File: p},
			NextAction: &schema.NextAction{Kind: "run_command",
				Detail: fmt.Sprintf("untrack it: `git rm --cached %s`, and ignore it (`lattice init` adds the line to .gitignore)", p)},
		})
	}
	if len(cache) > 0 {
		names := cache
		if len(names) > 5 {
			names = append(append([]string{}, names[:5]...), fmt.Sprintf("and %d more", len(cache)-5))
		}
		v = append(v, schema.Violation{
			Code: schema.CodeCacheTracked, Severity: schema.SeverityWarning, Subject: cacheDir + "/",
			Message: fmt.Sprintf("%d file(s) under %s/ are tracked (%s): it is Lattice's runtime cache, rewritten by ordinary runs, so the tree is never clean and `lattice break` refuses. Untrack it: `git rm -r --cached %s`, and ignore it.",
				len(cache), cacheDir, strings.Join(names, ", "), cacheDir),
			NextAction: &schema.NextAction{Kind: "run_command",
				Detail: fmt.Sprintf("untrack it: `git rm -r --cached %s`, and ignore it (`lattice init` adds the line to .gitignore)", cacheDir)},
		})
	}
	return v
}
