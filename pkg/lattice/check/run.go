package check

// @module-feature check

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/all"
	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/impact"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/scip"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// Run error codes. The CLI maps each to its own exit and message.
const (
	RunNotARepo  = "NOT_A_REPO"
	RunGitFailed = "GIT_FAILED"
	RunBadBase   = "BAD_BASE"
	RunExtract   = "EXTRACT_FAILED"
)

// RunError is a failure of Run with the stable code the CLI reports.
type RunError struct {
	Code string
	Err  error
}

func (e *RunError) Error() string { return e.Err.Error() }
func (e *RunError) Unwrap() error { return e.Err }

func runErr(code string, err error) *RunError { return &RunError{Code: code, Err: err} }

// RunOptions are the knobs of `lattice check`.
type RunOptions struct {
	// Base is the git ref to compare with; the base is merge-base(Base, HEAD).
	Base string
	// CI counts only evidence stamped with the head commit and a CI run.
	CI bool
	// SaveSnapshot writes the audit of HEAD as lattice/.cache/audit/<sha>.json,
	// as the CLI does. The web API never sets it: it only reads.
	SaveSnapshot bool
}

// Run is `lattice check --base`, as a function: the one verdict for a change.
// The CLI and the web API both call it, so `lattice check --json` and
// GET /api/v2/check return the same report, byte for byte. kg is the head
// graph; the caller builds it (read-only for the API).
// @enforces api:INV-5
func Run(ctx context.Context, ws *workspace.Workspace, kg schema.KnowledgeGraph, opt RunOptions) (Report, error) {
	top, err := impact.TopLevel(ws.LatticeDir)
	if err != nil {
		return Report{}, runErr(RunNotARepo, err)
	}
	head, err := GitRev(top, "HEAD")
	if err != nil {
		return Report{}, runErr(RunGitFailed, err)
	}
	dirty, _ := GitDirty(filepath.Dir(ws.LatticeDir))

	ev := Gather(ws.LatticeDir, head, opt.CI)
	src := audit.Collect(ws.LatticeDir, kg, dirty)
	headAudit := audit.Compute(ev.Apply(src))
	if opt.SaveSnapshot {
		// The snapshot is the base side of a later check: it keeps its own
		// rule (GatherSnapshot), so the stale rule of the head side does not
		// change what it records.
		snap := headAudit
		if sev := GatherSnapshot(ws.LatticeDir, head, opt.CI); sev.counted() != ev.counted() {
			snap = audit.Compute(sev.Apply(src))
		}
		_, _ = SaveSnapshot(ws.LatticeDir, snap)
	}

	baseSHA, err := GitRev(top, opt.Base)
	if err != nil {
		return Report{}, runErr(RunBadBase, fmt.Errorf("--base %q is not a commit: %v", opt.Base, err))
	}
	adCfg, err := config.LoadAdapters(ws.LatticeDir)
	if err != nil {
		return Report{}, runErr(RunExtract, err)
	}
	in := impact.Input{Workspace: ws, Registry: all.Registry(adCfg), Graph: kg, Since: opt.Base, IncludeDrafts: true}
	if corpus, cerr := scip.Load(SCIPIndexPaths(ws)...); cerr == nil {
		in.Corpus = corpus
	}
	im, err := impact.Compute(ctx, in)
	if err != nil {
		return Report{}, runErr(RunBadBase, fmt.Errorf("--base %q (%s): %v", opt.Base, baseSHA[:min(10, len(baseSHA))], err))
	}

	baseAudit, note := LoadSnapshot(ws.LatticeDir, im.Base)
	return Compute(Input{
		Rev: opt.Base, CI: opt.CI, Head: headAudit, Base: baseAudit, BaseNote: note,
		Impact: im, KG: kg, Evidence: ev, Waivers: LoadWaivers(ws.LatticeDir),
		Reconfirm: reconfirmedBy(ws, im, kg),
	}), nil
}

// SCIPIndexPaths lists the SCIP index file of every registered language.
func SCIPIndexPaths(ws *workspace.Workspace) []string {
	adCfg, _ := config.LoadAdapters(ws.LatticeDir)
	reg := all.Registry(adCfg)
	var paths []string
	for _, lang := range reg.Names() {
		paths = append(paths, filepath.Join(ws.SCIPDir(), lang+".scip"))
	}
	return paths
}

// GitRev resolves a rev to a commit sha.
func GitRev(top, rev string) (string, error) {
	out, err := exec.Command("git", "-C", top, "rev-parse", "--verify", "--quiet", rev+"^{commit}").Output()
	if err != nil {
		return "", fmt.Errorf("cannot resolve %s", rev)
	}
	return strings.TrimSpace(string(out)), nil
}

// GitDirty reports whether tracked files under dir differ from HEAD, with the
// first few paths. Derived caches and the ledger are not source.
func GitDirty(dir string) (bool, []string) {
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain", "--untracked-files=no", "--", ".").Output()
	if err != nil {
		return false, nil
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 4 {
			continue
		}
		p := strings.TrimSpace(line[3:])
		if i := strings.Index(p, " -> "); i >= 0 {
			p = p[i+4:]
		}
		if strings.Contains("/"+p, "/lattice/.ledger/") || strings.Contains("/"+p, "/lattice/.cache/") {
			continue
		}
		files = append(files, p)
	}
	sort.Strings(files)
	dirty := len(files) > 0
	if len(files) > 10 {
		files = append(files[:10], fmt.Sprintf("and %d more", len(files)-10))
	}
	return dirty, files
}

// reconfirmedBy reads the decision files this change adds or edits and lists
// the promises they re-confirm (Reconfirmed). Changed paths are relative to
// the repository top.
func reconfirmedBy(ws *workspace.Workspace, im impact.Report, kg schema.KnowledgeGraph) map[string]bool {
	out, err := exec.Command("git", "-C", ws.LatticeDir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return nil
	}
	top := strings.TrimSpace(string(out))
	if real, err := filepath.EvalSymlinks(top); err == nil {
		top = real
	}
	dir := ws.LatticeDir
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	rel, err := filepath.Rel(top, dir)
	if err != nil {
		return nil
	}
	var paths []string
	for _, f := range im.ChangedFiles {
		if f.NewPath != "" && f.Status != "D" {
			paths = append(paths, filepath.ToSlash(f.NewPath))
		}
	}
	read := func(p string) ([]byte, error) { return os.ReadFile(filepath.Join(top, filepath.FromSlash(p))) }
	return Reconfirmed(paths, filepath.ToSlash(rel), read, kg.BRDs)
}
