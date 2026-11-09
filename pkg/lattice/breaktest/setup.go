package breaktest

// @module-feature breaktest

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// setupTimeout bounds each setup command, which may install dependencies.
const setupTimeout = 10 * time.Minute

// ensureSetup prepares the worktree once, before the first test run: it
// links node_modules from the main checkout and runs break.setup. A failure
// is remembered, so every run after it reads unknown with the same reason.
// @enforces breaktest:INV-8
func (r *runner) ensureSetup(ctx context.Context) error {
	if r.setupDone {
		return r.setupErr
	}
	r.setupDone = true
	r.setupErr = r.setup(ctx)
	return r.setupErr
}

func (r *runner) setup(ctx context.Context) error {
	cfg := r.opts.Config.Break
	if cfg.LinksNodeModules() {
		if err := linkNodeModules(r.repo.Top, r.wt.root); err != nil {
			return fmt.Errorf("setup failed: linking node_modules: %w", err)
		}
	}
	for _, c := range cfg.Setup {
		cmd := strings.ReplaceAll(c, "{repo}", shellQuote(r.repo.Top))
		res := command(ctx, "", r.wt.Dir, setupTimeout, "sh", "-c", cmd)
		if res.status != statusOK {
			why := res.reason
			if why == "" {
				why = failingLine(res.output)
			}
			return fmt.Errorf("setup failed: %s: %s", clip(c), why)
		}
	}
	return nil
}

// linkNodeModules symlinks each node_modules directory of the main checkout
// into the same relative path of the worktree, unless the worktree already
// has something there. A node_modules directory is not searched inside.
func linkNodeModules(top, root string) error {
	return filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable directory has nothing to link
		}
		rel, rerr := filepath.Rel(top, p)
		if rerr != nil || rel == "." {
			return nil
		}
		base := d.Name()
		if d.IsDir() && (base == ".git" || filepath.ToSlash(rel) == ".claude/worktrees") {
			return filepath.SkipDir
		}
		if base != "node_modules" || !(d.IsDir() || d.Type()&fs.ModeSymlink != 0) {
			return nil
		}
		dst := filepath.Join(root, rel)
		if _, lerr := os.Lstat(dst); lerr == nil {
			// Already there (tracked, or linked): nothing to do.
		} else if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		} else if err := os.Symlink(p, dst); err != nil {
			return err
		}
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
}
