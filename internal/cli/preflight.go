package cli

// @module-feature cli

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/salahmyn/lattice/internal/preflight"
	"github.com/salahmyn/lattice/pkg/lattice/breaktest"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// noPreflightUsage is the help text of every --no-preflight flag.
const noPreflightUsage = "skip the preflight checks of executables, paths, versions, Docker and services"

// gate is one command's preflight requirements.
type gate struct {
	name string
	reqs []preflight.Requirement
}

// gateRequirements are the preflight requirements of each gate whose config
// is declared, in the order doctor lists them.
func gateRequirements(io *IO, ws *workspace.Workspace, cfg config.Config) []gate {
	var out []gate
	if cfg.Runtime.Configured() {
		out = append(out, gate{"runs-clean", preflight.Runtime(cfg.Runtime, io.Repo)})
	}
	if reqs := preflight.Break(cfg.Break, filepath.Dir(ws.LatticeDir)); len(reqs) > 0 {
		out = append(out, gate{"break", reqs})
	}
	return out
}

// dirtyProblem is break's dirty-tree refusal as a preflight problem, so it is
// reported with the others before the graph is built. Outside git there is
// nothing to report here; break says so itself.
func dirtyProblem(ctx context.Context, ws *workspace.Workspace) []preflight.Problem {
	repo, err := breaktest.OpenRepo(ctx, ws.PrimaryCodeRoot().Abs, ws.LatticeDir)
	if err != nil || !repo.Dirty {
		return nil
	}
	files := repo.DirtyFiles
	more := ""
	if len(files) > 5 {
		files, more = files[:5], ", …"
	}
	return []preflight.Problem{{
		Code:   preflight.CodeDirtyTree,
		Key:    "--allow-dirty",
		Detail: breaktest.ErrDirty.Error() + ": " + strings.Join(files, ", ") + more,
		Next:   "commit or stash the changes, or pass --allow-dirty to test the tree as it stands",
	}}
}

// runPreflight checks reqs and refuses the command when any fails, or when
// extra (problems found by the caller) is not empty. The refusal lists every
// problem with its code, config key and fix, and exits 2: it is a setup
// error, not a verdict. Nothing long has run when it returns.
// @enforces cli:INV-4
func runPreflight(ctx context.Context, io *IO, command string, reqs []preflight.Requirement, extra ...preflight.Problem) error {
	problems := append(append([]preflight.Problem{}, extra...), preflight.Problems(preflight.Check(ctx, reqs))...)
	if len(problems) == 0 {
		return nil
	}
	msg := command + " preflight failed: " + plural(len(problems), "problem") + " found; nothing was run"
	next := "fix each problem, or pass --no-preflight to skip these checks"
	if io.JSON {
		_ = io.printJSON(jsonError{Error: msg, Code: "PREFLIGHT_FAILED", NextAction: next, Problems: problems})
	} else {
		io.errorf("error: %s\n", msg)
		for _, p := range problems {
			io.errorf("  %-20s %s: %s\n", p.Code, p.Key, p.Detail)
			io.errorf("  %-20s next: %s\n", "", p.Next)
		}
		io.errorf("%s\n", next)
	}
	return exitCodeError{code: 2}
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return itoa(n) + " " + word + "s"
}
