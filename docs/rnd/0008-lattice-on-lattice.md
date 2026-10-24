# 0008 — Lattice on Lattice

- **Status:** experiment, done. Branch `rnd/lattice-on-lattice`, based on
  `feat/go-adapter` (PR #35, since merged).
- **Question:** with the latest CLI and the new Go adapter, can Lattice
  describe its own codebase: every module attributed, every invariant
  enforced, verified and demonstrated, and every BRD traced? Where does
  Lattice get its own code wrong, or fail to express it?

## What was done

1. **Workspace.** `lattice init --no-wizard` at the repository root
   (embedded mode). `lattice detect` reads the repo as Go (medium
   confidence, from `go.mod`). `lattice doctor` lists SCIP indexers and
   mutation runners for Python, TypeScript and PHP, and says nothing about
   Go. `workspace.yaml` excludes `examples/**`, the sample project used by
   the docs and tests, which carries its own workspace. No other directory
   holds generated or vendored code that the scan would reach.
2. **Discovery.** `lattice import scan` found 44 candidates, one per
   package directory, clustering 99.7% of production symbols (1765/1770).
   The candidates mapped onto the package layout, so they were used as
   the feature boundaries and not imported as drafts.
3. **Features (reverse chain).** There are 39 features under
   `lattice/features/`, one per area:
   - the adapter contract and one feature per language;
   - extraction, the graph, the schema, validation, the RTM and
     results;
   - BRDs and council approval, the ledger, leases and steering;
   - the CLI, the UI and the MCP server;
   - the remaining engine packages.

   Each production module has a `// @module-feature` tag right after its
   `package` clause, for 222 module tags in 222 modules.
   - There are 94 invariants. Each states a behaviour the code holds, and
     each was read from an existing test.
   - Each invariant has `// @enforces` on the guard function that holds it
     (100 tag lines) and `// @verifies` on the tests that check it (113
     lines).
   - No production code changed: the only source edits are comment tags.
     The touched files were run through gofmt, so whitespace differs in 38
     files that were not gofmt-clean before.
   - Every spec line (statement, enforcer, verifier) is in a generator
     script. The script refused to run until every named function
     resolved.
4. **Entry points.** There are 45 `kind: cli` entry points under
   `lattice/entry-points/`: one per top-level command, plus `lattice-mcp`.
   Each has a handler symbol and a hand-declared flow (see F2).
5. **Results.** `go test -json` cannot be ingested (F1). `gotestsum` could
   be installed through the module proxy, so the run used it:

   ```sh
   go install gotest.tools/gotestsum@v1.12.0
   gotestsum --junitfile unit.xml ./...     # 339 cases, all pass
   lattice results ingest unit.xml
   ```

   The MCP server's tests (Node's built-in runner) produce a report Lattice
   could not read (F4).
6. **BRDs.** There are five BRDs. Each takes `status: draft`,
   `provenance.source: llm_from_code` and `human_review_required: true`.
   - `brd.code_graph`: read the code.
   - `brd.verification`: prove the rules.
   - `brd.intent`: BRDs and approval.
   - `brd.agent_coordination`: leases, the ledger and next.
   - `brd.adoption`: import, workspace, views, UI, CLI and MCP.

   About their content:
   - There is one success criterion per invariant, so 94 in all, each
     with `maps_to_invariant`.
   - There is one user scenario per BRD. Each is tagged from its test
     (`// @verifies brd.x:US-1`).
   - No constraints were written.
   - Nothing was approved: no `brd approve`, `vote` or `decide`.
   - `lattice brd from-code` was not usable, because no LLM provider is
     configured. The BRDs were written by hand from the code.
   - The links were made canonical by letting `lattice brd link` rewrite
     each pair (F12).
7. **Validate.** `lattice validate` reports 0 errors, 5 warnings and 6
   info, and exits 0.

## Numbers (commit `ba16bff`)

| Measure | Value |
|---|---|
| Attribution coverage | 100.0% (222/222 production modules) |
| Documentation coverage | 100.0% (1770/1770 production symbols) |
| Verification coverage | 100.0% (94/94 invariants enforced and verified) |
| BRD goal coverage | 100.0% (94/94 criteria traced to a verified invariant) |
| BRD coverage | 0.0% (0/5 BRDs approved, by design: agent-written drafts) |
| `rtm --summary` | 94/94 criteria verified. 93 are demonstrated; `mcp:INV-1` stays verified (F4). With PR #37, all 94 are demonstrated. |
| Scenarios | 5, all verified. Journey coverage 0/5: none reaches a declared entry point (F7). |
| `validate` | 0 errors. 5 warnings, all `BRD_UNAPPROVED_LLM` (expected, the drafts await a human). 6 info: 5 `SCENARIO_NO_ENTRYPOINT`, 1 `FEATURE_UNREACHED` (`library`). |
| Graph | 39 features, 1770 symbols, 415 tests, 300 modules, 45 entry points |

`lattice next` ranks the five BRDs as awaiting a human's approval and says
an agent never approves.

## Findings

They are ranked by how much each one blocks Lattice from describing
itself. Each has a minimal reproduction and a proposed fix.

### F1 — `go test -json` cannot be ingested (blocks `demonstrated` offline)

Lattice's own test runner output is not a format Lattice reads. Without
network access to fetch a converter, no Go invariant can be demonstrated.

```sh
$ go test -json ./pkg/lattice/ledger/ > ledger.json
$ lattice results ingest ledger.json
error: ledger.json: parse tap: no test lines with a description
```

The report is not XML, so it falls through to the TAP parser, and the
error names TAP, not JSON.

**Fix:** add a `results.ParseGoTestJSON` reader. Pick it when the report's
first non-space byte is `{` and its lines carry an `Action` field. Fold the
events per `(Package, Test)`, letting the last `pass`/`fail`/`skip` win, and
record the case as classname `Package`, name `Test`. That is the shape the
Go adapter's FQNs already join (as `TestGoTestResultsJoin` shows for
gotestsum). A package-level `fail` with no test (a build failure) should
fail every case of that package. Let `results ingest` take `-` for stdin so
`go test -json ./... | lattice results ingest -` works.

### F2 — Go flows cannot be traced, and CLI handlers cannot be named

There is no Go SCIP indexer wired in (the adapter returns
`ErrIndexerNotConfigured`) and no detector for Go CLI frameworks. So every
one of the 45 entry points has a hand-declared flow that nothing checks.
The handler is also the wrong symbol:
- a cobra command's work runs in an anonymous `RunE: func(...)` literal;
- the only named symbol is the constructor `internal.cli.newValidateCommand`;
- `HANDLER_MISSING` is satisfied, but the flow tracer would start from code
  that only builds the command.

`lattice detect` recommends `scip-go`, but nothing uses it.

```sh
$ lattice ep show ep.cli.validate
ep.cli.validate  [production]  v1
  kind:       cli
  trigger:    lattice validate
  handler:    internal.cli.newValidateCommand     # builds the command; RunE does the work
  flow:
    -> cli
    -> validation                                  # exactly what the YAML says
```

**Fix:**
1. A `cobra` entry-point detector that reads `&cobra.Command{Use: …}`
   literals and their `AddCommand` tree. It would emit
   `ep.cli.<path>` with `trigger.command`.
2. The Go adapter records a func literal assigned to a `RunE`/`Run`
   field as a symbol `<enclosing func>.RunE`, the real handler.
3. Wire `scip-go` (`scip-go --output <out>`, run in the module root) so
   the existing SCIP tracer computes flows.

### F3 — Prose in Go comments was read as tags (fixed in PR #35)

The first extract of Lattice's own Go source reported `ANNOTATION_ORPHANED`
and phantom error codes. Wrapped doc lines in `graph/errors.go`,
`graph/surfaces.go` and `schema/violation.go` start with a tag name:

```go
// buildErrors fuses the error contracts feature manifests declare with the
// @error annotations found in source into one error inventory.
```

The same `@`-at-line-start rule works for JSDoc, where `@` lines are tags
by convention. In Go doc comments it is ambiguous. The Go adapter now
treats a tag line whose arguments contain spaces as prose (`tagLike`).
`@surface` keeps its spaced form and must start with an interaction type.

**Residual risk:** a real tag written with spaced arguments, such as
`// @feature checkout refund`, is dropped silently. **Fix:** emit an info
diagnostic, `ANNOTATION_PROSE_SKIPPED`, naming the line. Unlike a warning,
it would not fire on every prose line.

### F4 — Node's built-in runner reports are rejected (fixed in PR #37)

The MCP server's tests use `node --test`. Its JUnit reporter writes
`<testcase>` elements directly under `<testsuites>`:

```sh
$ node --test --import tsx --test-reporter=junit --test-reporter-destination=mcp.xml test/*.test.ts
$ lattice results ingest unit.xml mcp.xml
error: mcp.xml: parse junit xml: expected element type <testsuite> but have <testsuites>
```

There is a second problem behind the first. The runner writes
`classname="test"` and no file, so a case could never join the TypeScript
adapter's `lattice-mcp.test.descriptions.test.test.<slug>` FQN. PR #37
reads root-level cases. It also indexes a title-shaped case under
`test.<slug>`, which joins the FQN by its tail, and a title shared by two
files stays ambiguous. With it, `mcp:INV-1` reaches `demonstrated` and the
RTM is 94/94.

### F5 — The embedded workspace is git-ignored in Lattice's own repository

`.gitignore` has `/lattice`, meant for the binary that README and the user
guide build with `go build -o lattice ./cmd/lattice`. It also hides the
`lattice/` workspace directory: `git status` shows nothing after
`lattice init`, and every workspace file needs `git add -f`. Once
`lattice/` exists, the documented build command writes the binary to
`lattice/lattice`, inside the workspace. `lattice init` says nothing about
either.

```sh
$ lattice init --no-wizard && git status --short       # only .gitattributes
$ git check-ignore -v lattice/workspace.yaml           # .gitignore:3:/lattice
```

**Fix:**
- *Repository:* build to `dist/lattice`, which is already ignored, in
  README, the user guide and CI. Drop `/lattice` and `/lattice.exe` from
  `.gitignore`.
- *Engine:* `lattice init` and `lattice doctor` run `git check-ignore` on
  `lattice/workspace.yaml` and warn that the workspace will not be
  committed. A small fix, but it touches the repository's build docs and
  CI, so it is left for the owner to choose.

### F6 — `results ingest` overstates the run (fixed in PR #38)

```sh
$ gotestsum --junitfile unit.xml ./...      # DONE 339 tests
$ lattice results ingest unit.xml
ingested 674 test results (674 pass, 0 fail, 0 skip)
```

The ledger recorded the same `674 pass`. The CLI counted lookup keys, two
or more per case, instead of cases. PR #38 adds `Set.Tally`, which counts
each case once with its worst outcome.

### F7 — No scenario reaches an entry point: the CLI is barely tested end to end

Journey coverage is 0/5, and every scenario is `SCENARIO_NO_ENTRYPOINT`.
`internal/cli` (49 files) has 3 tests, and none runs a command. So no test
exercises a declared entry point, and naming one in `verified_by` would
overclaim. This is a real product gap that Lattice surfaced about itself;
it is not an engine bug.

**Fix:** add golden CLI tests that run `lattice extract`, `validate`,
`results ingest` and `rtm` through `cli.Execute` against
`examples/sample-project` (or a temp copy). Tag each with
`@verifies brd.<id>:US-N` and list the entry point id in the scenario's
`verified_by`.

### F8 — An agent's `lattice brd new` is stamped `source: human`

```sh
$ lattice --actor agent:x brd new brd.probe && tail -3 lattice/brds/brd.probe.yaml
provenance:
  source: human
  authored_by: agent:x
```

A BRD an agent scaffolds claims a human source, so `BRD_UNAPPROVED_LLM`,
which keys on `llm_from_code`, never fires for it. The BRDs here were set
to `llm_from_code` by hand. This is a governance hole, not a crash.

**Fix:** when the actor is an agent (an `agent:` prefix, or no human email
given), stamp `source: llm_from_code` and `human_review_required: true`.
Alternatively, have `BRD_UNAPPROVED_LLM` also fire on any unapproved BRD
whose `authored_by` is an agent. Left out of a fix PR because it decides
how actors are classified.

### F9 — Feature ids may use the `brd.` namespace

`brd new` says its `brd.` prefix keeps BRD ids apart from feature ids, but
`MANIFEST_ID_FORMAT` (`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`) accepts a
feature `brd.checkout`. Then `@verifies brd.checkout:SC-1` and
`brd.checkout:INV-1` refs resolve through `hasInvariant` before
`hasBRDUnit`, by lookup order, not by intent. This run named its BRD
features `brds` and `brds.council` to stay clear of the prefix.

**Fix:** `MANIFEST_ID_FORMAT` rejects a first segment `brd`, with a next
action to rename the feature.

### F10 — Results for a dirty tree are stamped with HEAD

`results ingest` stamps `kg.GeneratedFromCommit`, the HEAD at the last
extract. During this run the first ingest covered uncommitted annotations
and was ledgered `@ bd972c26`, a commit those tests never ran on.

**Fix:** record `dirty: true` on the set when `git status --porcelain` is
non-empty, at both extract and ingest time. Show it in `rtm`, and keep a
dirty set from upgrading anything to `demonstrated`, or at least flag it.

### F11 — Behaviours with no test cannot become invariants

Several real guarantees in the code have no test, so they could not be
declared without failing `UNVERIFIED_INVARIANT`. Seven features have no
invariants for this reason:
- `patch.Apply` rolls back when new error-severity violations appear;
- the UI refuses a non-loopback bind without `--token`
  (`enforceTokenPolicy`);
- `mutation`, `analysis`, `structural_checks`, `library` and `skills`;
- the LLM providers in `agentic`.

**Fix:** add tests for `patch` rollback and the UI token policy. Those are
the two security- and integrity-relevant ones. Then declare them.

### F12 — No canonical writer for hand- or agent-written YAML

Manifests, BRDs and entry points written by a script are only canonical
(quoting, list order) after a Lattice command rewrites them. This run made
them canonical by removing each `implements_via` / `implements_brd` pair
and running `lattice brd link` again. Entry-point files have no rewriter
at all.

**Fix:** add `lattice fmt [paths]`, which loads and saves manifests, BRDs
and entry points through their schema structs, plus a `--check` mode for
CI.

### F13 — The Go library API has no entry-point kind

`pkg/lattice/lattice.go` is the public embedding API. No `kind` describes
a library call, so the `library` feature is `FEATURE_UNREACHED`.

**Fix:** add an entry-point kind `library` (handler: an exported
function), or let a feature declare `surface: {type: module}` to count as
reached.

### F14 — Invariants enforced by data, not a guard

The MCP tool catalog (`tools` in `lattice-mcp/src/tools/index.ts`) must
use unique, `lattice_`-prefixed names, and a build-time test checks it.
The enforcer is a const table, which `ENFORCER_NOT_GUARD` (info) would
flag, so the invariant was left out.

**Fix:** let an invariant say `enforced_by: data` (or reuse
`verifiable_by: structural`). The rule then expects a tagged const and
does not call it a non-guard.

### F15 — Smaller items

- **Stale discovery.** `lattice coverage` reads `import/candidates.json`
  as of the last scan. After `exclude:` was added it still reported 99.4%,
  counting `examples/`, until `import scan` was re-run. Fix: recompute
  discovery from the graph, or warn when `candidates.json` is older than
  `workspace.yaml`.
- **Doctor and Go.** `lattice detect` recommends `scip-go`, while
  `lattice doctor` lists no Go row. Fix: when the Go adapter is enabled,
  have doctor report "no indexer wired" so the gap is visible.
- **Subtests inherit their test's tags.** A `t.Run` subtest has its test
  as `EnclosingFQN`, so through the annotation chain it inherits the
  test's `@verifies`. That makes every subtest a verifier of the parent's
  invariants. It is harmless for `demonstrated`, because the parent fails
  when a subtest fails, but it inflates verifier lists. Fix: skip
  `verifies` when climbing from a test to its enclosing test.
- **Nested Go modules and multi-root workspaces.** The Go FQN is the
  package directory relative to the code root. A `go.mod` below the root,
  or a multi-root prefix, puts extra segments in front of the import-path
  tail, and the result join falls back to last-segment matching. Fix: let
  the adapter read the nearest `go.mod` and drop its directory from the
  FQN prefix.
- **Docs disagree with the CLI on `view flows`.** The user guide's command
  list says `lattice view flows [<ep-id>]`, but the CLI takes one argument:
  `lattice view flows ep.cli.validate` fails with `accepts 1 arg(s),
  received 2`. The guide's §4b has it right: `--task <ep-id>`. Fix: correct
  the command list. Better, accept the id as a second argument, since
  `--task` is an odd flag name for an entry point.
- **Criteria mirror invariants.** With no LLM, each success criterion
  restates its invariant, so the BRDs are traceability scaffolding until
  a human rewrites them in business terms. `CRITERION_INVARIANT_NARROWER`
  never runs without an LLM.

## Left undone, and why

- **No BRD approved.** By rule. The five drafts await a human (`lattice
  next` lists them).
- **The engine fixes for F1, F2, F5, F8 to F15 were not built.** They are
  larger than a small fix, or they decide design or repository policy, so
  they are proposals here. F4 and F6 are PRs #37 and #38, kept off this
  branch.
- **Derived files are not committed:**
  - `lattice/lattice.json` (1.2 MB, rebuilt by `lattice extract`);
  - `lattice/import/` (rebuilt by `lattice import scan`);
  - the exported skill copies under `lattice/skills/` (rebuilt by `lattice
    init` / `skills export`);
  - `.cache/`.
- **The MCP report is not on this branch's RTM.** Ingesting it needs PR #37,
  and the RTM numbers above come from this branch's CLI.

## Reproducing

```sh
go build -o dist/lattice ./cmd/lattice
dist/lattice extract && dist/lattice validate
gotestsum --junitfile unit.xml ./... && dist/lattice results ingest unit.xml
dist/lattice coverage && dist/lattice rtm --summary
```
