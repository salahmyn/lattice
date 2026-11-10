package breaktest

// @module-feature breaktest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// git runs a git command in dir and returns its trimmed stdout.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Repo is the git checkout around the code root.
type Repo struct {
	Top    string // the checkout's top-level directory
	Prefix string // the code root relative to Top, "" when they are the same
	Head   string // HEAD's sha
	// Dirty is true when anything differs from HEAD (tracked changes and
	// untracked files that are not ignored) outside Lattice's own state.
	Dirty bool
	// DirtyFiles are those paths, relative to Top, sorted.
	DirtyFiles []string
}

// OpenRepo reads the checkout around codeRoot. It does not change it.
// latticeDirs are the workspace directories whose .ledger/ and .cache/ are
// Lattice's own state; every lattice/.ledger/ and lattice/.cache/ is too.
func OpenRepo(ctx context.Context, codeRoot string, latticeDirs ...string) (Repo, error) {
	var r Repo
	top, err := git(ctx, codeRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		return r, fmt.Errorf("not a git checkout: %w", err)
	}
	r.Top = filepath.Clean(top)
	abs, err := filepath.EvalSymlinks(codeRoot)
	if err != nil {
		return r, err
	}
	topReal, err := filepath.EvalSymlinks(r.Top)
	if err != nil {
		return r, err
	}
	if r.Prefix, err = filepath.Rel(topReal, abs); err != nil {
		return r, err
	}
	if r.Prefix == "." {
		r.Prefix = ""
	}
	if r.Head, err = git(ctx, r.Top, "rev-parse", "HEAD"); err != nil {
		return r, err
	}
	// The dirty state is read from the same file list the overlay copies, so
	// what the report says matches what was tested, whatever the user's
	// status.showUntrackedFiles is.
	changed, err := uncommitted(ctx, r.Top)
	if err != nil {
		return r, err
	}
	owned := ownedPrefixes(topReal, latticeDirs)
	for _, rel := range changed {
		if !latticeOwned(rel, owned) {
			r.DirtyFiles = append(r.DirtyFiles, rel)
		}
	}
	sort.Strings(r.DirtyFiles)
	r.Dirty = len(r.DirtyFiles) > 0
	return r, nil
}

// uncommitted lists the paths, relative to top, that differ from HEAD:
// modified, added, deleted and untracked files that are not ignored.
func uncommitted(ctx context.Context, top string) ([]string, error) {
	changed, err := git(ctx, top, "-c", "core.quotepath=off", "diff", "HEAD", "--no-renames", "--name-only", "-z")
	if err != nil {
		return nil, err
	}
	untracked, err := git(ctx, top, "ls-files", "-o", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, rel := range append(strings.Split(changed, "\x00"), strings.Split(untracked, "\x00")...) {
		if rel != "" {
			out = append(out, rel)
		}
	}
	return out, nil
}

// ownedPrefixes returns the .ledger/ and .cache/ directories of each
// workspace inside the checkout, as slash paths relative to its top.
func ownedPrefixes(top string, latticeDirs []string) []string {
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
			continue // a workspace in another checkout writes nothing here
		}
		base := ""
		if rel != "." {
			base = filepath.ToSlash(rel) + "/"
		}
		out = append(out, base+".ledger/", base+".cache/")
	}
	return out
}

// latticeOwned reports whether a changed path is Lattice's own runtime
// state: the ledger that ingest, validate and check runs append to, and the
// cache that holds ingested results, coverage and breaks.json. Neither is an
// input of a break run. Source files, manifests and lattice.json are not
// Lattice's own state and always count.
// @enforces breaktest:INV-9
func latticeOwned(rel string, owned []string) bool {
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

// Worktree is a temporary detached `git worktree` at HEAD. Dir is the code
// root inside it. Every command of a break run happens here; the user's
// working tree is only ever read.
type Worktree struct {
	Dir string

	repo    Repo
	tmp     string // the directory holding the worktree
	root    string // the worktree's top-level directory
	overlay bool   // the user's uncommitted changes were copied in
}

// NewWorktree adds a detached worktree at HEAD. With overlay, the working
// tree's uncommitted changes (modified, deleted and untracked files) are
// copied in, so a dirty tree is broken and tested as it stands.
func NewWorktree(ctx context.Context, repo Repo, overlay bool) (*Worktree, error) {
	tmp, err := os.MkdirTemp("", "lattice-break-*")
	if err != nil {
		return nil, err
	}
	w := &Worktree{repo: repo, tmp: tmp, root: filepath.Join(tmp, "wt"), overlay: overlay}
	w.Dir = filepath.Join(w.root, repo.Prefix)
	if _, err := git(ctx, repo.Top, "worktree", "add", "--detach", w.root, "HEAD"); err != nil {
		w.Close()
		return nil, err
	}
	if overlay {
		if err := w.applyOverlay(ctx); err != nil {
			w.Close()
			return nil, err
		}
	}
	return w, nil
}

// Close removes the worktree and its directory. It runs on a context of its
// own, so an interrupt still cleans up.
func (w *Worktree) Close() {
	ctx := context.Background()
	if _, err := os.Stat(w.root); err == nil {
		_, _ = git(ctx, w.repo.Top, "worktree", "remove", "--force", w.root)
	}
	_ = os.RemoveAll(w.tmp)
	_, _ = git(ctx, w.repo.Top, "worktree", "prune")
}

// applyOverlay copies the working tree's uncommitted state into the worktree.
func (w *Worktree) applyOverlay(ctx context.Context) error {
	changed, err := uncommitted(ctx, w.repo.Top)
	if err != nil {
		return err
	}
	for _, rel := range changed {
		src, dst := filepath.Join(w.repo.Top, rel), filepath.Join(w.root, rel)
		info, err := os.Lstat(src)
		if os.IsNotExist(err) {
			_ = os.Remove(dst)
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			continue // symlinks and the like are not part of a break run
		}
		if err := copyFile(src, dst, info.Mode()); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Reset puts the worktree back to HEAD (and the overlay) after a break could
// not be undone any other way.
func (w *Worktree) Reset(ctx context.Context) error {
	if _, err := git(ctx, w.root, "checkout", "--force", "--", "."); err != nil {
		return err
	}
	if _, err := git(ctx, w.root, "clean", "-fdq"); err != nil {
		return err
	}
	if w.overlay {
		return w.applyOverlay(ctx)
	}
	return nil
}

// ApplyPatch applies a script's patch with `git apply`; reverse undoes it.
// Paths in the patch are relative to the repository root.
func (w *Worktree) ApplyPatch(ctx context.Context, patch string, reverse bool) error {
	args := []string{"apply", "--whitespace=nowarn"}
	if reverse {
		args = append(args, "-R")
	}
	args = append(args, patch)
	_, err := git(ctx, w.root, args...)
	return err
}

// errWorktree marks a failure that leaves the worktree in an unknown state,
// after which no further run can be trusted.
var errWorktree = errors.New("worktree could not be restored")
