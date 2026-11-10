package cli

// @module-feature check

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/all"
	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/check"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/impact"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/scip"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// exitCodeError ends the process with a given code after the command has
// already said why. Execute honours it.
type exitCodeError struct{ code int }

func (e exitCodeError) Error() string { return fmt.Sprintf("lattice: exit %d", e.code) }

// usageError reports a usage error and exits 2.
func usageError(io *IO, code, msg string) error {
	_ = io.fail(code, msg, nil)
	return exitCodeError{code: 2}
}

// newCheckCommand is `lattice check --base <rev>`: one verdict for a change.
func newCheckCommand(io *IO) *cobra.Command {
	var base string
	var markdown, ci, snapshot bool
	cmd := &cobra.Command{
		Use:   "check --base <rev>",
		Short: "One verdict for a change: BROKEN, UNPROVEN or ASSURED",
		Long: `Judges the change from merge-base(<rev>, HEAD) to the working tree and prints
one page: the verdict, one row per touched promise with its signals, the gaps
against the base, and the blind spot (touched lines no promise covers).

It finds the touched promises and lines with 'lattice impact', and the signals
(traced, confirmed, live, exercised, break_tested) with 'lattice audit' at head.

  BROKEN    something fell: a promise level dropped against base; an enforcer
            or verifier was removed; a break killed at base survives at head;
            the suite is red; or a confirmed promise's statement, tier or
            mapping was edited (the edit voids the confirmation).
  UNPROVEN  nothing fell, but a touched promise is unconfirmed, not exercised,
            not break-tested where its tier needs it, unknown, or touched lines
            are untraced. It is not a pass.
  ASSURED   every touched line is traced and every touched promise has the
            signals its tier needs.

Tier defaults: tier 1 needs exercised or break_tested; tier 2 and 3 need
break_tested; tier 3 always needs ASSURED.

merge_allowed is true for ASSURED, and for UNPROVEN only when no (promise,
signal) pair has a gap it did not have at base, the new untraced lines did not
rise, and no tier-3 promise is involved. A waiver never removes a gap.

The base audit is lattice/.cache/audit/<merge-base sha>.json, written by
'lattice check' (or 'lattice check --snapshot') on that commit; CI restores it
from a cache or artifact. With none, evidence is not compared with base: the
verdict reads UNPROVEN, never BROKEN, and the merge rule is not met.

--ci counts only evidence stamped with the head commit and a CI run (the
GITHUB_RUN_ID or CI_PIPELINE_ID that 'results ingest' and 'break' record);
anything else reads unknown (EVIDENCE_NOT_CI). Only CI's verdict counts.

Exit codes: 0 for ASSURED or an allowed UNPROVEN; 1 for BROKEN or a
disallowed UNPROVEN; 2 for a usage error. --json is lattice.check/1;
--markdown is a PR comment starting with ` + check.Marker + `.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if markdown && io.JSON {
				return usageError(io, "BAD_FLAG", "--markdown and --json are exclusive")
			}
			if base == "" && !snapshot {
				return usageError(io, "BASE_REQUIRED", "--base <rev> is required")
			}
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			top, err := impact.TopLevel(ws.LatticeDir)
			if err != nil {
				return usageError(io, "NOT_A_REPO", err.Error())
			}
			kg, err := readOnlyGraph(cmd, ws)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			head, err := gitRev(top, "HEAD")
			if err != nil {
				return usageError(io, "GIT_FAILED", err.Error())
			}
			dirty, _ := gitDirty(filepath.Dir(ws.LatticeDir))

			ev := check.Gather(ws.LatticeDir, head, ci)
			src := audit.Collect(ws.LatticeDir, kg, dirty)
			headAudit := audit.Compute(ev.Apply(src))
			saved, serr := check.SaveSnapshot(ws.LatticeDir, headAudit)

			if snapshot {
				if serr != nil {
					return io.fail("SNAPSHOT_FAILED", serr.Error(), nil)
				}
				if io.JSON {
					return io.printJSON(map[string]interface{}{"contract": "lattice.snapshot/1", "commit": head, "path": saved, "saved": saved != ""})
				}
				if saved == "" {
					io.printf("no snapshot saved: the working tree has uncommitted changes\n")
				} else {
					io.printf("saved %s\n", saved)
				}
				return nil
			}

			baseSHA, err := gitRev(top, base)
			if err != nil {
				return usageError(io, "BAD_BASE", fmt.Sprintf("--base %q is not a commit: %v", base, err))
			}
			adCfg, err := config.LoadAdapters(ws.LatticeDir)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			in := impact.Input{Workspace: ws, Registry: all.Registry(adCfg), Graph: kg, Since: base, IncludeDrafts: true}
			if corpus, cerr := scip.Load(scipIndexPaths(ws)...); cerr == nil {
				in.Corpus = corpus
			}
			im, err := impact.Compute(cmd.Context(), in)
			if err != nil {
				return usageError(io, "BAD_BASE", fmt.Sprintf("--base %q (%s): %v", base, baseSHA[:min(10, len(baseSHA))], err))
			}

			baseAudit, note := check.LoadSnapshot(ws.LatticeDir, im.Base)
			rep := check.Compute(check.Input{
				Rev: base, CI: ci, Head: headAudit, Base: baseAudit, BaseNote: note,
				Impact: im, KG: kg, Evidence: ev, Waivers: check.LoadWaivers(ws.LatticeDir),
				Reconfirm: reconfirmedBy(ws, im, kg),
			})

			switch {
			case io.JSON:
				if err := io.printJSON(rep); err != nil {
					return err
				}
			case markdown:
				check.WriteMarkdown(io.Out, rep)
			default:
				check.WriteText(io.Out, rep)
			}
			if !rep.MergeAllowed {
				return exitCodeError{code: 1}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "git ref to compare with; the base is merge-base(<rev>, HEAD) (required)")
	cmd.Flags().BoolVar(&markdown, "markdown", false, "print Markdown for a PR comment")
	cmd.Flags().BoolVar(&ci, "ci", false, "count only evidence stamped with the head commit and a CI run")
	cmd.Flags().BoolVar(&snapshot, "snapshot", false, "only save the audit of HEAD as lattice/.cache/audit/<sha>.json (for a base branch job)")
	return cmd
}

// gitRev resolves a rev to a commit sha.
func gitRev(top, rev string) (string, error) {
	out, err := exec.Command("git", "-C", top, "rev-parse", "--verify", "--quiet", rev+"^{commit}").Output()
	if err != nil {
		return "", fmt.Errorf("cannot resolve %s", rev)
	}
	return strings.TrimSpace(string(out)), nil
}

// reconfirmedBy reads the decision files this change adds or edits and lists
// the promises they re-confirm (check.Reconfirmed). Changed paths are relative
// to the repository top.
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
	return check.Reconfirmed(paths, filepath.ToSlash(rel), read, kg.BRDs)
}
