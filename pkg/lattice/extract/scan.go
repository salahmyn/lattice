package extract

import (
	"bytes"
	"os/exec"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// scanFilter decides which paths under one code root are parsed: git-ignored
// paths and the workspace's exclude globs are skipped. Paths are slash paths
// relative to the root.
type scanFilter struct {
	ignored map[string]bool // git's untracked-and-ignored paths; dirs end in "/"
	exclude []string
}

// newScanFilter asks git which untracked paths under root its ignore rules
// (every .gitignore, .git/info/exclude, core.excludesFile) exclude. Git is
// the authority on its own rules, so nested files and negations behave as
// they do for git; a tracked file is still scanned even when a pattern
// matches it. Outside a git work tree, or without git, nothing is ignored.
func newScanFilter(root string, exclude []string) scanFilter {
	f := scanFilter{ignored: map[string]bool{}, exclude: exclude}
	out, err := exec.Command("git", "-C", root, "ls-files", "-z",
		"--others", "--ignored", "--exclude-standard", "--directory").Output()
	if err != nil {
		return f
	}
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) > 0 {
			f.ignored[string(p)] = true
		}
	}
	return f
}

// skipDir reports whether the directory at rel (no trailing slash) is pruned.
func (f scanFilter) skipDir(rel string) bool {
	return f.ignored[rel+"/"] || f.excluded(rel)
}

// skipFile reports whether the file at rel is left out.
func (f scanFilter) skipFile(rel string) bool {
	return f.ignored[rel] || f.excluded(rel)
}

func (f scanFilter) excluded(rel string) bool {
	for _, g := range f.exclude {
		if ok, _ := doublestar.Match(strings.TrimPrefix(g, "./"), rel); ok {
			return true
		}
	}
	return false
}
