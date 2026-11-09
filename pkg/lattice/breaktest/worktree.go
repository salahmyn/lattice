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
	Dirty  bool   // anything uncommitted, untracked files included
}

// OpenRepo reads the checkout around codeRoot. It does not change it.
func OpenRepo(ctx context.Context, codeRoot string) (Repo, error) {
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
	// Lattice's own ledger is appended by ingest and validate runs; it is
	// not code, so it does not make the tree dirty for a break run.
	status, err := git(ctx, r.Top, "status", "--porcelain", "--", ".", ":(exclude,glob)**/lattice/.ledger/**")
	if err != nil {
		return r, err
	}
	r.Dirty = status != ""
	return r, nil
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
	changed, err := git(ctx, w.repo.Top, "-c", "core.quotepath=off", "diff", "HEAD", "--no-renames", "--name-only", "-z")
	if err != nil {
		return err
	}
	untracked, err := git(ctx, w.repo.Top, "ls-files", "-o", "--exclude-standard", "-z")
	if err != nil {
		return err
	}
	for _, rel := range append(strings.Split(changed, "\x00"), strings.Split(untracked, "\x00")...) {
		if rel == "" {
			continue
		}
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
