package blocks

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// ReadGit reads the files matching glob from clone at ref through git, so the
// clone's working tree and checked-out branch never matter. It returns the
// files, the commit ref resolved to, and that commit's date.
func ReadGit(clone, ref, glob string) ([]File, string, string, error) {
	if ref == "" {
		ref = "HEAD"
	}
	if strings.HasPrefix(ref, "-") {
		return nil, "", "", fmt.Errorf("invalid ref %q", ref)
	}
	if !doublestar.ValidatePattern(glob) {
		return nil, "", "", fmt.Errorf("invalid glob %q", glob)
	}
	git := func(args ...string) ([]byte, error) { return runGit(clone, args...) }
	out, err := git("rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return nil, "", "", fmt.Errorf("ref %s not found: %w", ref, err)
	}
	commit := strings.TrimSpace(string(out))
	out, err = git("log", "-1", "--format=%cI", commit)
	if err != nil {
		return nil, "", "", err
	}
	date := strings.TrimSpace(string(out))
	// shortcut: lists the whole tree; fine for catalogue repos, slow on huge monorepos (use a pathspec then)
	out, err = git("ls-tree", "-r", "-z", "--name-only", commit)
	if err != nil {
		return nil, "", "", err
	}
	var files []File
	for _, name := range strings.Split(string(out), "\x00") {
		if name == "" {
			continue
		}
		if ok, _ := doublestar.Match(glob, name); !ok {
			continue
		}
		data, err := git("show", commit+":"+name)
		if err != nil {
			return nil, "", "", err
		}
		files = append(files, File{Path: name, Data: data})
	}
	return files, commit, date, nil
}

// ReadDir reads the files matching glob under dir, in path order.
func ReadDir(dir, glob string) ([]File, error) {
	// doublestar returns nil,nil for a missing dir, which would silently drop the source's blocks
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("source path %s is missing or not a directory", dir)
	}
	names, err := doublestar.Glob(os.DirFS(dir), glob)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	sort.Strings(names)
	files := make([]File, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		files = append(files, File{Path: name, Data: data})
	}
	return files, nil
}

// runGit runs a read-only git command in clone and returns its stdout.
func runGit(clone string, args ...string) ([]byte, error) {
	out, err := exec.Command("git", append([]string{"-C", clone}, args...)...).Output()
	if err != nil {
		msg := ""
		if ee, ok := err.(*exec.ExitError); ok {
			msg = ": " + strings.TrimSpace(string(ee.Stderr))
		}
		return nil, fmt.Errorf("git %s in %s: %w%s", strings.Join(args, " "), clone, err, msg)
	}
	return out, nil
}
