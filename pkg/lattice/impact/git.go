package impact

// @module-feature impact

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// repoGit runs read-only git commands in one work tree.
type repoGit struct {
	dir string
	env []string
}

// run runs one git command and returns its stdout. GIT_OPTIONAL_LOCKS=0
// keeps commands that take optional locks from writing on the side.
// @enforces impact:INV-1
func (g repoGit) run(args ...string) ([]byte, error) {
	full := append([]string{"-C", g.dir, "-c", "core.quotePath=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(append(os.Environ(), "GIT_OPTIONAL_LOCKS=0"), g.env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return out, nil
}

// openRepo returns a runner for the work tree at top whose commands use a
// private copy of the index. `git diff <commit>` against the working tree
// refreshes stale stat data and rewrites the index even with
// GIT_OPTIONAL_LOCKS=0; on the copy it gets the same answer and .git is
// left as it was. The returned cleanup removes the copy and must always run.
// @enforces impact:INV-1
func openRepo(top string) (repoGit, func(), error) {
	g := repoGit{dir: top}
	out, err := g.run("rev-parse", "--git-path", "index")
	if err != nil {
		return g, func() {}, err
	}
	idx := strings.TrimSpace(string(out))
	if !filepath.IsAbs(idx) {
		idx = filepath.Join(top, idx)
	}
	data, err := os.ReadFile(idx)
	if os.IsNotExist(err) {
		return g, func() {}, nil // no index yet: nothing to protect
	}
	if err != nil {
		return g, func() {}, err
	}
	f, err := os.CreateTemp("", "lattice-impact-index-*")
	if err != nil {
		return g, func() {}, err
	}
	cleanup := func() { _ = os.Remove(f.Name()) }
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		cleanup()
		return g, func() {}, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return g, func() {}, err
	}
	g.env = []string{"GIT_INDEX_FILE=" + f.Name()}
	return g, cleanup, nil
}

// TopLevel returns the root of the git work tree that contains dir.
func TopLevel(dir string) (string, error) {
	out, err := repoGit{dir: dir}.run("rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.Clean(strings.TrimSpace(string(out))), nil
}

// resolveBase returns merge-base(since, HEAD) and HEAD's commit.
func resolveBase(g repoGit, since string) (base, head string, err error) {
	out, err := g.run("rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", "", err
	}
	head = strings.TrimSpace(string(out))
	out, err = g.run("merge-base", since, "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("no merge base between %s and HEAD: %w", since, err)
	}
	return strings.TrimSpace(string(out)), head, nil
}

// FileChange is one path the change touches, as `git diff -M --name-status`
// reports it against the working tree. OldPath is empty for an added file and
// NewPath for a deleted one; both are set (and differ) for a rename.
type FileChange struct {
	Status    string `json:"status"` // A | M | D | R | C | T
	OldPath   string `json:"old_path,omitempty"`
	NewPath   string `json:"new_path,omitempty"`
	Untracked bool   `json:"untracked,omitempty"`

	oldRanges []lineRange // changed lines on the base side
	newRanges []lineRange // changed lines on the working-tree side
}

// path is the path a change is listed under: the new one unless deleted.
func (c FileChange) path() string {
	if c.NewPath != "" {
		return c.NewPath
	}
	return c.OldPath
}

// lineRange is an inclusive 1-based range of lines.
type lineRange struct{ from, to int }

// changedFiles lists every file that differs between base and the working
// tree, with the changed line ranges on each side. Untracked files that git
// does not ignore are included as added: extraction reads them too.
func changedFiles(g repoGit, base string) ([]FileChange, error) {
	out, err := g.run("diff", "-M", "--name-status", "-z", "--no-ext-diff", base)
	if err != nil {
		return nil, err
	}
	changes := parseNameStatus(out)

	patch, err := g.run("diff", "-M", "--unified=0", "--no-color", "--no-ext-diff",
		"--no-textconv", "--src-prefix=a/", "--dst-prefix=b/", base)
	if err != nil {
		return nil, err
	}
	hunks := parseHunks(patch)
	for i := range changes {
		if h, ok := hunks[changes[i].path()]; ok {
			changes[i].oldRanges, changes[i].newRanges = h.old, h.new
		}
	}

	untracked, err := g.run("ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	for _, p := range bytes.Split(untracked, []byte{0}) {
		if len(p) == 0 {
			continue
		}
		rel := string(p)
		n := countLines(filepath.Join(g.dir, filepath.FromSlash(rel)))
		c := FileChange{Status: "A", NewPath: rel, Untracked: true}
		if n > 0 {
			c.newRanges = []lineRange{{1, n}}
		}
		changes = append(changes, c)
	}
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].path() < changes[j].path() })
	return changes, nil
}

// parseNameStatus reads `git diff --name-status -z` output.
func parseNameStatus(out []byte) []FileChange {
	fields := strings.Split(string(out), "\x00")
	var changes []FileChange
	for i := 0; i < len(fields); i++ {
		st := fields[i]
		if st == "" {
			continue
		}
		kind := st[:1]
		switch kind {
		case "R", "C":
			if i+2 >= len(fields) {
				return changes
			}
			changes = append(changes, FileChange{Status: kind, OldPath: fields[i+1], NewPath: fields[i+2]})
			i += 2
		default:
			if i+1 >= len(fields) {
				return changes
			}
			p := fields[i+1]
			i++
			switch kind {
			case "A":
				changes = append(changes, FileChange{Status: kind, NewPath: p})
			case "D":
				changes = append(changes, FileChange{Status: kind, OldPath: p})
			default: // M, T, U
				changes = append(changes, FileChange{Status: kind, OldPath: p, NewPath: p})
			}
		}
	}
	return changes
}

type fileHunks struct{ old, new []lineRange }

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// parseHunks reads a `--unified=0` patch into changed line ranges per file,
// keyed by the file's new path (its old path when deleted). A pure
// insertion has no old-side lines and a pure deletion no new-side lines, so
// a symbol that only shifted down or up overlaps nothing.
func parseHunks(patch []byte) map[string]fileHunks {
	out := map[string]fileHunks{}
	var oldPath, newPath string
	for _, ln := range strings.Split(string(patch), "\n") {
		switch {
		case strings.HasPrefix(ln, "diff --git "):
			oldPath, newPath = "", ""
		case strings.HasPrefix(ln, "--- "):
			oldPath = patchPath(strings.TrimPrefix(ln, "--- "), "a/")
		case strings.HasPrefix(ln, "+++ "):
			newPath = patchPath(strings.TrimPrefix(ln, "+++ "), "b/")
		case strings.HasPrefix(ln, "rename from "):
			oldPath = unquote(strings.TrimPrefix(ln, "rename from "))
		case strings.HasPrefix(ln, "rename to "):
			newPath = unquote(strings.TrimPrefix(ln, "rename to "))
		case strings.HasPrefix(ln, "@@ "):
			m := hunkHeader.FindStringSubmatch(ln)
			if m == nil {
				continue
			}
			cur := newPath
			if cur == "" {
				cur = oldPath
			}
			h := out[cur]
			if r, ok := hunkRange(m[1], m[2]); ok {
				h.old = append(h.old, r)
			}
			if r, ok := hunkRange(m[3], m[4]); ok {
				h.new = append(h.new, r)
			}
			out[cur] = h
		}
	}
	return out
}

func hunkRange(start, count string) (lineRange, bool) {
	s, _ := strconv.Atoi(start)
	n := 1
	if count != "" {
		n, _ = strconv.Atoi(count)
	}
	if n <= 0 {
		return lineRange{}, false
	}
	return lineRange{s, s + n - 1}, true
}

// patchPath strips the a/ or b/ prefix from a ---/+++ header; /dev/null is "".
func patchPath(p, prefix string) string {
	p = strings.TrimSuffix(p, "\t")
	if p == "/dev/null" {
		return ""
	}
	p = unquote(p)
	return strings.TrimPrefix(p, prefix)
}

// unquote undoes git's C-style quoting of a path with unusual characters.
func unquote(p string) string {
	if strings.HasPrefix(p, `"`) {
		if u, err := strconv.Unquote(p); err == nil {
			return u
		}
	}
	return p
}

// showBlob returns a file's content at a commit, or nil when it is absent.
func showBlob(g repoGit, commit, path string) ([]byte, bool) {
	out, err := g.run("cat-file", "blob", commit+":"+path)
	if err != nil {
		return nil, false
	}
	return out, true
}

// dirty reports whether the working tree differs from HEAD.
func dirty(g repoGit) bool {
	out, err := g.run("status", "--porcelain", "--untracked-files=normal")
	return err == nil && len(bytes.TrimSpace(out)) > 0
}

func countLines(path string) int {
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return 0
	}
	n := bytes.Count(b, []byte{'\n'})
	if b[len(b)-1] != '\n' {
		n++
	}
	return n
}
