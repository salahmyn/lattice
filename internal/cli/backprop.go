package cli

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/flags"
	"github.com/salahmyn/lattice/pkg/lattice/ledger"
	"github.com/salahmyn/lattice/pkg/lattice/revision"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/sweep"
)

// newBackpropCommand implements the back-propagation scan (v0.8.1):
// after a code change merges, diff the touched symbols against the
// graph and classify the blast radius. The requirement never silently
// follows the code — a doc update is an explicit, attributed, human-
// approved transition.
func newBackpropCommand(io *IO) *cobra.Command {
	var since string
	var raise bool
	cmd := &cobra.Command{
		Use:   "backprop",
		Short: "Scan a merged change's blast radius against grounded intent",
		Long: `Diffs the files changed since --since (default HEAD~1) against the
graph: which enforcer symbols moved, which invariants they back, which
grounded criteria sit above. Outcomes:

  no doc impact — nothing grounded is semantically affected (ledgered)
  amendments    — affected grounded criteria are listed for the PM gate;
                  with --flag each is flagged until a human clears it

The scan never edits a requirement. Narrowing a criterion is never an
amendment — escalate it to a CR (lattice cr propose).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			kg, ws, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			changed, err := changedFiles(io.Repo, since)
			if err != nil {
				return io.fail("GIT_FAILED", err.Error(), nil)
			}

			affected := affectedCriteria(kg, changed)
			if len(affected) == 0 {
				appendLedgerEvent(io, ws, ledger.EventCheckRun, "workspace",
					"backprop: no doc impact ("+since+"…HEAD, "+fmt.Sprint(len(changed))+" files)")
				if io.JSON {
					return io.printJSON(map[string]interface{}{
						"changed_files": len(changed), "affected": []string{}, "impact": "none",
					})
				}
				io.printf("backprop: no doc impact — %d changed file(s) touch no grounded criterion's enforcement\n", len(changed))
				return nil
			}

			for _, unit := range affected {
				appendLedgerEvent(io, ws, ledger.EventCheckRun, unit, "backprop: enforcement changed since "+since)
				if raise {
					_, _ = flags.Raise(ws.LatticeDir, unit,
						"backprop: enforcement changed since "+since+" — confirm the requirement still holds",
						io.actor(), "backprop", time.Now())
				}
			}
			if io.JSON {
				return io.printJSON(map[string]interface{}{
					"changed_files": len(changed), "affected": affected, "flagged": raise,
				})
			}
			io.printf("backprop: %d grounded criterion/criteria affected by changes since %s:\n", len(affected), since)
			for _, unit := range affected {
				io.printf("  %s\n", unit)
			}
			if raise {
				io.printf("flags raised — a human approves the amendment (flag clear) or bounces the code change\n")
			} else {
				io.printf("draft an amendment per unit for the PM gate, or rerun with --flag to demote until reviewed\n")
			}
			io.printf("anything narrowing the requirement escalates: lattice cr propose <unit> --text …\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&since, "since", "HEAD~1", "git ref to diff against")
	cmd.Flags().BoolVar(&raise, "flag", false, "raise a meaning flag on each affected criterion")
	return cmd
}

// newSweepCommand is the forbidden-move sweep (v0.8.1): a verifier that
// existed at the recorded baseline and is gone now must trace to a
// retirement item of an approved CR, else TEST_RETIRED_ILLEGALLY.
func newSweepCommand(io *IO) *cobra.Command {
	var update bool
	cmd := &cobra.Command{
		Use:   "sweep",
		Short: "Forbidden-move sweep: no verifier disappears without a CR retirement item",
		RunE: func(cmd *cobra.Command, _ []string) error {
			kg, ws, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			current := sweep.Inventory(kg)
			baseline, ok := sweep.Load(ws.LatticeDir)
			if !ok {
				if err := sweep.Save(ws.LatticeDir, sweep.Baseline{
					Commit: kg.GeneratedFromCommit, Verifiers: current,
				}); err != nil {
					return io.fail("SWEEP_FAILED", err.Error(), nil)
				}
				io.printf("sweep: baseline recorded (%d verifiers @ %s)\n", len(current), shortCommit(kg.GeneratedFromCommit))
				return nil
			}

			revs, _ := revision.LoadAll(ws.LatticeDir)
			var illegal []schema.Violation
			legal := 0
			for _, fqn := range sweep.Disappeared(baseline, current) {
				if crID, covered := revision.RetirementCovered(revs, fqn); covered {
					legal++
					appendLedgerEvent(io, ws, ledger.EventCheckRun, fqn, "sweep: retired legally under "+crID)
					continue
				}
				illegal = append(illegal, schema.Violation{
					Code:     schema.CodeTestRetiredIllegally,
					Severity: schema.SeverityWarning,
					Message:  fqn + " was a verifier at the baseline and is gone, with no approved CR retirement item",
					NextAction: &schema.NextAction{
						Kind:   "open_cr",
						Detail: "restore the test, or route the descoping through lattice cr (narrowing class)",
					},
				})
			}

			appendLedgerEvent(io, ws, ledger.EventCheckRun, "workspace",
				fmt.Sprintf("sweep: %d verifiers, %d legally retired, %d illegal", len(current), legal, len(illegal)))

			if update && len(illegal) == 0 {
				if err := sweep.Save(ws.LatticeDir, sweep.Baseline{
					Commit: kg.GeneratedFromCommit, Verifiers: current,
				}); err != nil {
					return io.fail("SWEEP_FAILED", err.Error(), nil)
				}
			}

			if io.JSON {
				_ = io.printJSON(map[string]interface{}{
					"verifiers": len(current), "legally_retired": legal, "violations": illegal,
				})
			} else {
				io.printf("sweep: %d verifiers now (baseline %d @ %s); %d legally retired\n",
					len(current), len(baseline.Verifiers), shortCommit(baseline.Commit), legal)
				for _, v := range illegal {
					io.printf("  warn %s %s\n", v.Code, v.Message)
				}
			}
			if len(illegal) > 0 {
				return io.fail("SWEEP_FORBIDDEN_MOVE",
					fmt.Sprintf("%d verifier(s) disappeared without a retirement item", len(illegal)), nil)
			}
			if update {
				io.printf("baseline updated (%d verifiers @ %s)\n", len(current), shortCommit(kg.GeneratedFromCommit))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&update, "update-baseline", false, "record the current inventory as the new baseline (only when clean)")
	return cmd
}

// changedFiles lists repo-relative paths changed between since and HEAD,
// plus uncommitted modifications.
func changedFiles(repo, since string) ([]string, error) {
	out, err := exec.Command("git", "-C", repo, "diff", "--name-only", since, "HEAD").Output()
	if err != nil {
		return nil, fmt.Errorf("git diff %s..HEAD failed: %w", since, err)
	}
	dirty, _ := exec.Command("git", "-C", repo, "diff", "--name-only", "HEAD").Output()
	set := map[string]bool{}
	for _, ln := range strings.Split(string(out)+"\n"+string(dirty), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			set[filepath.ToSlash(ln)] = true
		}
	}
	files := make([]string, 0, len(set))
	for f := range set {
		files = append(files, f)
	}
	sort.Strings(files)
	return files, nil
}

// affectedCriteria maps changed files → enforcer symbols → invariants →
// grounded (approved-BRD) criteria above them.
func affectedCriteria(kg schema.KnowledgeGraph, changed []string) []string {
	changedSet := map[string]bool{}
	for _, f := range changed {
		changedSet[f] = true
	}
	// Invariant refs whose enforcing symbols live in changed files.
	movedInv := map[string]bool{}
	for _, sym := range kg.Symbols {
		if len(sym.EnforcesInvariants) == 0 || !fileChanged(changedSet, sym.File) {
			continue
		}
		for _, ref := range sym.EnforcesInvariants {
			feature, inv := ref, ""
			if i := strings.LastIndex(ref, ":"); i > 0 {
				feature, inv = ref[:i], ref[i+1:]
			} else {
				feature, inv = sym.Feature, ref
			}
			movedInv[feature+":"+inv] = true
		}
	}
	var units []string
	for _, b := range kg.BRDs {
		if b.Status != schema.BRDApproved {
			continue // only grounded intent back-propagates
		}
		for _, sc := range b.SuccessCriteria {
			if movedInv[sc.MapsToInvariant] {
				units = append(units, b.ID+"/"+sc.ID)
			}
		}
	}
	sort.Strings(units)
	return units
}

func fileChanged(changed map[string]bool, file string) bool {
	if file == "" {
		return false
	}
	f := filepath.ToSlash(file)
	if changed[f] {
		return true
	}
	// Graph paths may be code-root-relative while git paths are
	// repo-relative; fall back to suffix matching.
	for c := range changed {
		if strings.HasSuffix(c, f) || strings.HasSuffix(f, c) {
			return true
		}
	}
	return false
}
