package ui

// @module-feature ui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/entrypoints"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// The fingerprint names the state of every input the read-only graph build
// and the audit read, without reading what they compute from:
//
//   - every file under lattice/, .cache/ included (features, BRDs, entry
//     points, config, ingested results, coverage, breaks, confirm.json, the
//     SCIP indexes, the import sidecar);
//   - for each git repository holding a code root or lattice/: HEAD, and
//     each path `git status` lists (changed, staged, untracked and not
//     ignored, and the ignored build files reach reads by name), with the
//     identity of the file now on disk;
//   - the files the entry-point detectors read beside the parsed source
//     (entrypoints.InputGlobs).
//
// A tracked file git reports clean has the content of HEAD, so HEAD stands
// for it. A file's identity is its size, mtime, ctime, inode and device; a
// file written in the last moment, which a later write in the same clock
// tick would leave looking the same, is hashed instead. A code root outside
// git (extraction would scan every file in it) cannot be fingerprinted
// cheaply, and is an error.

// racyWindow is how close to now a file's mtime or ctime may be before its
// stat alone no longer tells two contents apart. It covers the coarse clocks
// of network and FAT file systems. Tests shorten it to reach the stat path.
var racyWindow = 2 * time.Second

const (
	// racyHashLimit is the largest recently written file that is hashed;
	// a larger one is an error, so the cache is not used.
	racyHashLimit = 1 << 20
	// maxRepos bounds the repositories one fingerprint visits (nested
	// repositories and submodules included).
	maxRepos = 64
)

// errNotGit is returned for a code root that git does not track.
var errNotGit = errors.New("not in a git repository")

type fingerprinter struct {
	ctx   context.Context
	h     hash.Hash
	now   time.Time
	repos map[string]bool
	build map[string]bool // base names reach reads beside parsed modules
}

// inputsFingerprint hashes the identity of every input of the read-only
// graph build and the audit. Any error means the inputs cannot be named,
// and the caller must not use a cached result.
// @enforces ui:INV-3
func inputsFingerprint(ctx context.Context, ws *workspace.Workspace) (string, error) {
	f := &fingerprinter{ctx: ctx, h: sha256.New(), now: time.Now(), repos: map[string]bool{}, build: map[string]bool{}}
	for _, n := range reach.BuildFiles() {
		f.build[n] = true
	}
	f.line("lattice.inputs/1")

	lat, err := filepath.EvalSymlinks(ws.LatticeDir)
	if err != nil {
		return "", err
	}
	if err := f.tree(lat); err != nil {
		return "", err
	}

	// The repository holding lattice/ stamps the graph's commit and says
	// whether the tree is dirty, even when it holds no code root.
	switch err := f.repo(filepath.Dir(lat)); {
	case errors.Is(err, errNotGit):
		f.line("lattice-repo", "none")
	case err != nil:
		return "", err
	}

	for _, r := range ws.CodeRoots {
		f.line("root", r.Name, r.Abs, fmt.Sprint(r.Available))
		if !r.Available {
			continue
		}
		abs, err := filepath.EvalSymlinks(r.Abs)
		if err != nil {
			return "", err
		}
		if within(abs, lat) {
			continue // walked with lattice/
		}
		if err := f.repo(abs); err != nil {
			return "", fmt.Errorf("code root %s: %w", r.Name, err)
		}
	}

	for _, g := range entrypoints.InputGlobs(ws) {
		matches, err := filepath.Glob(g)
		if err != nil {
			return "", err
		}
		sort.Strings(matches)
		f.line("glob", g)
		for _, m := range matches {
			if err := f.file(m); err != nil && !errors.Is(err, errIsDir) {
				return "", err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(f.h.Sum(nil)), nil
}

func (f *fingerprinter) line(parts ...string) {
	for _, p := range parts {
		_, _ = io.WriteString(f.h, p)
		_, _ = f.h.Write([]byte{0})
	}
	_, _ = f.h.Write([]byte{'\n'})
}

// within reports whether p is dir or inside it.
func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// tree adds every file under dir. A symlink to a directory is followed,
// once: the loaders open paths through it.
func (f *fingerprinter) tree(dir string) error {
	seen := map[string]bool{}
	var walk func(string) error
	walk = func(root string) error {
		real, err := filepath.EvalSymlinks(root)
		if err != nil {
			return err
		}
		if seen[real] {
			return nil
		}
		seen[real] = true
		return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			err = f.file(p)
			if errors.Is(err, errIsDir) {
				f.line("dirlink", p)
				return walk(p)
			}
			return err
		})
	}
	return walk(dir)
}

// errIsDir is returned by file for a path that is (or links to) a directory.
var errIsDir = errors.New("a directory where a file was expected")

// file adds the identity of the file at p: absent, or its stat (or hash,
// when it was written in the last moment). A symlink adds its target too.
func (f *fingerprinter) file(p string) error {
	if lst, err := os.Lstat(p); err == nil && lst.Mode()&fs.ModeSymlink != 0 {
		target, err := os.Readlink(p)
		if err != nil {
			return err
		}
		f.line("link", p, target)
	}
	st, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		f.line("file", p, "absent")
		return nil
	}
	if err != nil {
		return err
	}
	if st.IsDir() {
		return fmt.Errorf("%s: %w", p, errIsDir)
	}
	if !f.racy(st) {
		f.line("file", p, statID(st))
		return nil
	}
	if st.Size() > racyHashLimit {
		return fmt.Errorf("%s: written just now and too large to hash", p)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	f.line("file", p, "sha256", hex.EncodeToString(sum[:]))
	return nil
}

// racy reports whether st was changed so recently that a second write in
// the same clock tick could leave its stat unchanged.
func (f *fingerprinter) racy(st fs.FileInfo) bool {
	latest := st.ModTime()
	if c, ok := changeTime(st); ok && c.After(latest) {
		latest = c
	}
	return !latest.Before(f.now.Add(-racyWindow))
}

// repo adds the state of the git repository that holds dir: its HEAD and
// every path its status lists. A nested repository or submodule that git
// reports is added in turn.
func (f *fingerprinter) repo(dir string) error {
	out, err := f.git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		if strings.Contains(err.Error(), "not a git repository") {
			return errNotGit
		}
		return err
	}
	top := filepath.Clean(strings.TrimSpace(string(out)))
	if real, err := filepath.EvalSymlinks(top); err == nil {
		top = real
	}
	if f.repos[top] {
		return nil
	}
	if len(f.repos) >= maxRepos {
		return fmt.Errorf("more than %d git repositories under the code roots", maxRepos)
	}
	f.repos[top] = true

	head, err := f.git(top, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	f.line("repo", top, strings.TrimSpace(string(head)))

	// One pass lists tracked changes against HEAD and the index, untracked
	// files one by one, and ignored paths (an ignored directory once).
	status, err := f.git(top, "status", "--porcelain=v1", "-z", "--no-renames",
		"--untracked-files=all", "--ignored=matching", "--ignore-submodules=none")
	if err != nil {
		return err
	}
	for _, e := range bytes.Split(status, []byte{0}) {
		if len(e) < 4 {
			continue
		}
		xy, rel := string(e[:2]), string(e[3:])
		abs := filepath.Join(top, filepath.FromSlash(strings.TrimSuffix(rel, "/")))
		if xy == "!!" {
			// Extraction never parses an ignored file. Reach reads its
			// build files by name wherever modules sit; an ignored
			// directory holds no parsed module, so only files count.
			if !strings.HasSuffix(rel, "/") && f.build[filepath.Base(abs)] {
				f.line("ignored", xy)
				if err := f.file(abs); err != nil && !errors.Is(err, errIsDir) {
					return err
				}
			}
			continue
		}
		f.line("status", xy)
		st, err := os.Lstat(abs)
		switch {
		case err == nil && st.IsDir():
			// a nested repository (untracked, listed as dir/) or a
			// submodule: its files are in its own status
			f.line("nested", abs)
			if linkedWorktree(abs) {
				continue // extraction skips a second checkout
			}
			if err := f.repo(abs); err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
		default:
			// a symlink to a directory is recorded by its target:
			// the source walk does not follow it
			if err := f.file(abs); err != nil && !errors.Is(err, errIsDir) {
				return err
			}
		}
	}
	return nil
}

// linkedWorktree reports whether dir is a linked git worktree, which
// extraction never descends into.
func linkedWorktree(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return false
	}
	line := strings.TrimSpace(string(b))
	return strings.HasPrefix(line, "gitdir:") && strings.Contains(filepath.ToSlash(line), "/worktrees/")
}

// git runs a read-only git command in dir. Optional locks are off, so
// status never rewrites the index while another git command runs.
func (f *fingerprinter) git(dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(f.ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
