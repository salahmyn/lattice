// Package gitstate reads whether a git working tree differs from HEAD. It is
// the one definition check, impact and break share: a path counts when it is
// modified, added or deleted against HEAD, or untracked and not ignored, and
// it is not Lattice's own runtime state. The package imports nothing else of
// Lattice, so any package can use it.
package gitstate

// @module-feature check

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// run runs one read-only git command in dir and returns its stdout.
// GIT_OPTIONAL_LOCKS=0 keeps it from refreshing the index on the side; env
// adds to the environment (GIT_INDEX_FILE, for a private index copy).
func run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(append(os.Environ(), "GIT_OPTIONAL_LOCKS=0"), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// Changed lists the paths under dir that differ from HEAD: modified, added,
// deleted, and untracked files that git does not ignore. Paths are slash
// paths relative to the checkout's top, in git's order, Lattice's own state
// included. It reads the same files whatever status.showUntrackedFiles is.
func Changed(ctx context.Context, dir string, env ...string) ([]string, error) {
	tracked, err := run(ctx, dir, env, "diff", "HEAD", "--no-renames", "--name-only", "-z", "--", ".")
	if err != nil {
		return nil, err
	}
	untracked, err := run(ctx, dir, env, "ls-files", "--others", "--exclude-standard", "--full-name", "-z")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, b := range [][]byte{tracked, untracked} {
		for _, rel := range strings.Split(string(b), "\x00") {
			if rel != "" {
				out = append(out, rel)
			}
		}
	}
	return out, nil
}

// Owned returns the .ledger/ and .cache/ directories of each workspace inside
// the checkout at top, as slash paths relative to top with a trailing slash.
// A workspace outside the checkout writes nothing in it and is left out.
func Owned(top string, latticeDirs []string) []string {
	if real, err := filepath.EvalSymlinks(top); err == nil {
		top = real
	}
	var out []string
	for _, d := range latticeDirs {
		if d == "" {
			continue
		}
		if real, err := filepath.EvalSymlinks(d); err == nil {
			d = real
		}
		rel, err := filepath.Rel(top, d)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		base := ""
		if rel != "." {
			base = filepath.ToSlash(rel) + "/"
		}
		out = append(out, base+".ledger/", base+".cache/")
	}
	return out
}

// LatticeOwned reports whether a changed path is Lattice's own runtime
// state: the ledger of gates, CRs, flags and sign-offs, and the cache that
// holds ingested results, coverage, the last check runs and breaks.json.
// Every lattice/.ledger/ and lattice/.cache/ is, and so are the directories
// owned lists (see Owned), whatever the workspace directory is called.
// Source files, manifests and lattice.json are not Lattice's own state and
// always count.
// @enforces breaktest:INV-9
// @enforces check:INV-12
func LatticeOwned(rel string, owned []string) bool {
	p := "/" + filepath.ToSlash(rel)
	if strings.Contains(p, "/lattice/.ledger/") || strings.Contains(p, "/lattice/.cache/") {
		return true
	}
	for _, o := range owned {
		if strings.HasPrefix(p, "/"+o) {
			return true
		}
	}
	return false
}

// Dirty lists the paths under dir that differ from HEAD outside Lattice's own
// state, relative to the checkout's top and sorted. latticeDirs are the
// workspace directories whose .ledger/ and .cache/ are also Lattice's; env is
// passed to every git command. An empty list means the tree is clean.
func Dirty(ctx context.Context, dir string, latticeDirs []string, env ...string) ([]string, error) {
	top, err := Top(ctx, dir)
	if err != nil {
		return nil, err
	}
	changed, err := Changed(ctx, dir, env...)
	if err != nil {
		return nil, err
	}
	return Filter(changed, Owned(top, latticeDirs)), nil
}

// Top returns the top-level directory of the checkout that holds dir, with
// symlinks resolved.
func Top(ctx context.Context, dir string) (string, error) {
	out, err := run(ctx, dir, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	top := filepath.Clean(strings.TrimSpace(string(out)))
	if real, err := filepath.EvalSymlinks(top); err == nil {
		top = real
	}
	return top, nil
}

// Abbrev keeps the first n paths and names how many more there are, as
// "and N more".
func Abbrev(files []string, n int) []string {
	if len(files) <= n {
		return files
	}
	return append(append([]string(nil), files[:n]...), fmt.Sprintf("and %d more", len(files)-n))
}

// Filter drops Lattice's own state (LatticeOwned) from changed and returns
// the rest sorted, without duplicates.
func Filter(changed, owned []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, rel := range changed {
		if seen[rel] || LatticeOwned(rel, owned) {
			continue
		}
		seen[rel] = true
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}
