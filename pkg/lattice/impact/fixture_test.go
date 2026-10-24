package impact

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/all"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/entrypoints"
	"github.com/salahmyn/lattice/pkg/lattice/extract"
	"github.com/salahmyn/lattice/pkg/lattice/graph"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// repo is a temporary git repository holding a Lattice workspace.
type repo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	r := &repo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.git("config", "user.email", "test@example.com")
	r.git("config", "user.name", "test")
	r.git("config", "commit.gpgsign", "false")
	// No background maintenance: newer git detaches `maintenance run --auto`
	// after a commit, which writes into .git while a test reads it.
	r.git("config", "maintenance.auto", "false")
	r.git("config", "gc.auto", "0")
	r.write("lattice/config.yaml", "agentic:\n  llm:\n    enabled: false\n")
	r.write("lattice/adapters.yaml", "adapters:\n  go:\n    enabled: true\n")
	return r
}

func (r *repo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *repo) write(rel, body string) {
	r.t.Helper()
	p := filepath.Join(r.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// mv moves a tracked file with git, creating the target directory.
func (r *repo) mv(from, to string) {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(r.dir, filepath.FromSlash(to))), 0o755); err != nil {
		r.t.Fatal(err)
	}
	r.git("mv", from, to)
}

func (r *repo) commit(msg string) string {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

// input builds the working tree's graph the way read commands do and
// wraps it in an Input.
func (r *repo) input(since string) Input {
	r.t.Helper()
	ws, err := workspace.Open(r.dir)
	if err != nil {
		r.t.Fatal(err)
	}
	adCfg, err := config.LoadAdapters(ws.LatticeDir)
	if err != nil {
		r.t.Fatal(err)
	}
	reg := all.Registry(adCfg)
	res, err := extract.Extract(context.Background(), ws, reg, extract.Options{})
	if err != nil {
		r.t.Fatal(err)
	}
	kg := graph.Build(graph.Input{BRDs: res.BRDs, Manifests: res.Manifests, Modules: res.Modules}, graph.Options{})
	eps, _, _ := entrypoints.LoadEntryPointsReport(ws.EntryPointsDir())
	kg.EntryPoints = eps
	return Input{Workspace: ws, Registry: reg, Graph: kg, Since: since, IncludeDrafts: true}
}

func (r *repo) report(in Input) Report {
	r.t.Helper()
	rep, err := Compute(context.Background(), in)
	if err != nil {
		r.t.Fatal(err)
	}
	return rep
}

// --- the shared fixture ---

const featureBase = `id: refund
version: 1
status: production
purpose: Refunds money a customer was charged.
owners:
  business: payments
  engineering: payments-eng
capabilities:
  - id: issue
    summary: Issue a refund
    rules:
      - A refund is issued against one capture.
invariants:
  - id: INV-1
    statement: A refund never exceeds the captured amount.
  - id: INV-2
    statement: Refunds are capped per day.
  - id: INV-3
    statement: A refund is tagged with its reason.
  - id: INV-4
    statement: A refund is reversible within a day.
  - id: INV-5
    statement: A refund is reported to the ledger.
  - id: INV-6
    statement: A refund keeps its currency.
`

const featureHead = `id: refund
version: 1
status: production
purpose: Refunds money a customer was charged.
owners:
  business: payments
  engineering: payments-eng
capabilities:
  - id: issue
    summary: Issue a refund
    rules:
      - A refund is issued against one capture.
      - A refund names the capture it reverses.
invariants:
  - id: INV-1
    statement: A refund never exceeds the captured amount.
    verifiable_by: [test, mutation]
  - id: INV-2
    statement: Refunds are capped per day.
  - id: INV-3
    statement: A refund is tagged with its reason.
  - id: INV-4
    statement: A refund is reversible within a day.
  - id: INV-5
    statement: A refund is reported to the ledger.
  - id: INV-6
    statement: A refund keeps its currency.
`

const approvedBase = `id: brd.refunds
version: 1
status: approved
title: Refunds customers trust
business_problem: Customers must get their money back, never more.
user_scenarios:
  - id: US-1
    actor: customer
    narrative: A customer asks for a refund.
success_criteria:
  - id: SC-1
    statement: A refund never exceeds what was captured.
    maps_to_invariant: refund:INV-1
    tier: 2
  - id: SC-2
    statement: A refund is tagged and reversible.
    maps_to_invariant: [refund:INV-3, refund:INV-4]
  - id: SC-3
    statement: Every refund is audited.
    direct_wire: true
    enforced_by: [refund.Audit]
  - id: SC-4
    statement: A refund is reported.
    maps_to_invariant: refund:INV-5
implements_via:
  - refund
`

const approvedHead = `id: brd.refunds
version: 1
status: approved
title: Refunds customers trust
business_problem: Customers must get their money back, never more.
user_scenarios:
  - id: US-1
    actor: support agent
    narrative: A customer asks for a refund.
    verified_by: [refund.TestGuard]
    given: [a captured payment]
    when: [the customer asks for a refund]
    then: [the refund is issued]
    blocks: [blk.refund]
success_criteria:
  - id: SC-1
    statement: A refund never exceeds what was captured.
    maps_to_invariant: refund:INV-1
    tier: 3
  - id: SC-2
    statement: A refund is tagged and reversible.
    maps_to_invariant: [refund:INV-3, refund:INV-4, refund:INV-6]
  - id: SC-3
    statement: Every refund is audited.
    direct_wire: true
    enforced_by: [refund.Audit]
  - id: SC-4
    statement: A refund is reported.
    maps_to_invariant: refund:INV-5
implements_via:
  - refund
`

const draftBRD = `id: brd.payouts
version: 1
status: draft
title: Payout limits
business_problem: Payouts must stay within daily limits.
success_criteria:
  - id: SC-1
    statement: Daily refund volume is capped.
    maps_to_invariant: refund:INV-2
  - id: SC-2
    statement: Currency is kept.
    maps_to_invariant: refund:INV-6
provenance:
  source: llm_from_code
`

const guardBase = `package refund

// @module-feature refund

// Guard refuses a refund above the captured amount.
// @enforces refund:INV-1
func Guard(amount, captured int) bool {
	return amount <= captured
}

// Untouched is never edited.
func Untouched() int {
	return 1
}

// Limit caps refunds per day.
// @enforces refund:INV-2
func Limit() int {
	return 10
}

// Audit records a refund.
func Audit() string {
	return "audit"
}
`

const guardHead = `package refund

// @module-feature refund

// NewHelper is added above everything else, shifting what follows.
func NewHelper() int {
	return 2
}

// Guard refuses a refund above the captured amount.
// @enforces refund:INV-1
func Guard(amount, captured int) bool {
	if amount < 0 {
		return false
	}
	return amount <= captured
}

// Untouched is never edited.
func Untouched() int {
	return 1
}

// Audit records a refund.
func Audit() string {
	return "audit: refund"
}
`

const taggedBase = `package refund

// @module-feature refund

// Tagged tags a refund with its reason.
// @enforces refund:INV-3
func Tagged(reason string) string {
	return "reason=" + reason
}
`

const taggedHead = `package refund

// @module-feature refund

// Tagged tags a refund with its reason.
func Tagged(reason string) string {
	return "reason=" + reason
}
`

func simpleGo(pkg, name, inv string) string {
	return "package " + pkg + "\n\n// @module-feature refund\n\n// " + name + " does one thing.\n// @enforces " + inv +
		"\nfunc " + name + "() int {\n\treturn 1\n}\n"
}

// buildFixture commits a base on main, branches, makes every kind of
// change (some committed, the dropped tag left uncommitted) and adds a
// later commit on main the report must not include. It returns the base.
func buildFixture(t *testing.T) (*repo, string) {
	r := newRepo(t)
	r.write("lattice/features/refund.yaml", featureBase)
	r.write("lattice/features/notes/old.yaml", "id: notes\nversion: 1\nstatus: proposal\npurpose: Notes.\nowners:\n  business: b\n  engineering: e\n")
	r.write("lattice/features/legacy.yaml", "id: legacy\nversion: 1\nstatus: deprecated\npurpose: Old.\nowners:\n  business: b\n  engineering: e\n")
	r.write("lattice/brds/brd.refunds.yaml", approvedBase)
	r.write("lattice/brds/brd.payouts.yaml", draftBRD)
	r.write("lattice/entry-points/ep.cli.refund.yaml", `id: ep.cli.refund
version: 1
status: production
kind: cli
purpose: Issue a refund from the command line
trigger:
  command: refund issue
handler:
  symbol: api.Handle
  file: api/handler.go
flow:
  - feature: refund
`)
	r.write("refund/guard.go", guardBase)
	r.write("refund/tagged.go", taggedBase)
	r.write("refund/gone.go", simpleGo("refund", "Gone", "refund:INV-4"))
	r.write("refund/moved.go", simpleGo("refund", "Moved", "refund:INV-5"))
	r.write("refund/kept.go", simpleGo("refund", "Kept", "refund:INV-6"))
	r.write("refund/guard_test.go", "package refund\n\nimport \"testing\"\n\n// @verifies refund:INV-1\nfunc TestGuard(t *testing.T) {\n\tif !Guard(1, 2) {\n\t\tt.Fatal(\"guard\")\n\t}\n}\n")
	r.write("api/handler.go", "package api\n\n// @module-feature api\n\n// Handle serves the refund command.\nfunc Handle() bool {\n\treturn true\n}\n")
	r.write("other/unrelated.go", "package other\n\n// @module-feature api\n\n// Unrelated is not part of the change.\nfunc Unrelated() int {\n\treturn 0\n}\n")
	base := r.commit("base")

	r.git("checkout", "-q", "-b", "change")
	r.write("refund/guard.go", guardHead)
	r.git("rm", "-q", "refund/gone.go")
	r.mv("refund/moved.go", "vendor/moved.go")
	r.mv("refund/kept.go", "elsewhere/kept.go")
	r.write("lattice/features/refund.yaml", featureHead)
	r.mv("lattice/features/notes/old.yaml", "lattice/features/notes.yaml")
	r.mv("lattice/features/legacy.yaml", "lattice/archive/legacy.yaml")
	r.write("lattice/features/added.yaml", "id: added\nversion: 1\nstatus: proposal\npurpose: New.\nowners:\n  business: b\n  engineering: e\n")
	r.write("lattice/brds/brd.refunds.yaml", approvedHead)
	r.write("lattice/entry-points/ep.cli.refund.yaml", `id: ep.cli.refund
version: 2
status: production
kind: cli
purpose: Issue or preview a refund from the command line
trigger:
  command: refund issue
handler:
  symbol: api.Handle
  file: api/handler.go
flow:
  - feature: refund
`)
	r.commit("change")

	// A later commit on main that the merge base excludes.
	r.git("checkout", "-q", "main")
	r.write("other/unrelated.go", "package other\n\n// @module-feature api\n\n// Unrelated changed on main only.\nfunc Unrelated() int {\n\treturn 1\n}\n")
	r.commit("main moves on")
	r.git("checkout", "-q", "change")

	// Left uncommitted: the report covers the working tree.
	r.write("refund/tagged.go", taggedHead)
	return r, base
}

func findSymbol(rep Report, fqn string) (ChangedSymbol, bool) {
	for _, s := range rep.ChangedSymbols {
		if s.Symbol == fqn {
			return s, true
		}
	}
	return ChangedSymbol{}, false
}

func findCriterion(list []AffectedCriterion, brd, sc string) (AffectedCriterion, bool) {
	for _, c := range list {
		if c.BRD == brd && c.Criterion == sc {
			return c, true
		}
	}
	return AffectedCriterion{}, false
}

func findField(mc ManifestChange, field string) (FieldChange, bool) {
	for _, f := range mc.Fields {
		if f.Field == field {
			return f, true
		}
	}
	return FieldChange{}, false
}

func findChange(list []ManifestChange, id string) (ManifestChange, bool) {
	for _, c := range list {
		if c.ID == id {
			return c, true
		}
	}
	return ManifestChange{}, false
}
