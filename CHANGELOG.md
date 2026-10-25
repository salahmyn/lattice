# Changelog

All notable changes to Lattice are documented in this file.

## [Unreleased]

### Added

- **Block map explorer** (`/map` and `lattice export map`), still one file
  that works offline:
  - **Focus** replaces Relations. A block sits in the centre with what links
    to it on the left and what it links to on the right, 1 to 3 hops out,
    each block once ("⇄ both ways" when linked in both directions).
    Click to re-centre, with back and forward history. **System context**
    lifts edges to systems and externals and merges them with counts.
  - **Lenses** on the Diagram group blocks by system, owner, kind, tier,
    truth or proposal verdict, with merged edges between the groups.
  - **Kind colours and icons** by family, in every view, with a legend that
    counts the kinds on screen and hides a kind on click.
  - **Discover**: a filterable, sortable table of every block, with chips
    per column; a row opens Focus.
  - **Deep links**: the URL hash keeps the tab, block, hops, lens, context,
    proposal and hidden kinds, and the back button walks them. Feature,
    BRD and entry point pages in `lattice serve` link to their block.
  - **Canvas toolbar**: fit, zoom, levels, a minimap (shown while part of
    the drawing is off screen; Fit keeps clear of it), the view as Mermaid
    text, and export as SVG, PNG or `.mmd`.
  - Blocks only in a proposal are counted apart in the header, and Discover
    names them with a link that selects the proposal.
  - **Flows** group their steps into swimlanes by system, owner or tier.
    The walkthrough steps with the arrow keys and takes another way at a
    fork with 1–9.
  - A right-click menu on any block, and `@owner` on boxes and cards.
- **`owner`** on system, external and group judgment entries, kept as
  `attrs.owner`. Feature blocks carry their manifest's owner too.
- **`examples/block-map/`**: a fictional bookshop workspace for the map,
  and `scripts/blockmap-smoke.js`, a Playwright smoke test of the exported
  page (run by a Go test that skips without node or Playwright).

- **`lattice fmt [--check] [path...]`** rewrites feature manifests, BRDs
  and entry points in canonical form: their schema's field order and a
  2-space indent, as Lattice's own writers produce.
  - Normalising a hand-written file becomes its own change, not noise in
    the next `brd approve` or `brd link` diff.
  - It never loses information. A file with a YAML comment, an anchor or
    alias, or a key its schema does not know is skipped with the reason.
  - `--check` writes nothing and exits non-zero when a file would change,
    for CI.
- **`BRD_LINK_ONE_SIDED`** (warning): the two halves of a BRD ↔ feature
  link disagree. That is a BRD's `implements_via` naming a feature with no
  `implements_brd`, or one naming another BRD, or a feature's
  `implements_brd` naming a BRD that does not list it.
  `lattice brd link <brd> <feature>` writes both halves.
- **`lattice impact`, a read-only change-impact report**:
  `lattice impact --since <ref> [--format text|markdown|json]
  [--max-symbols N] [--include-drafts=true]` compares
  merge-base(`<ref>`, HEAD) with the working tree and names the base commit
  it used. It replaces the glue of `backprop --json`, per-symbol
  `blast-radius` and hand-written manifest diffs in a PR comment job.
  - **Changed code by line range**: the symbols whose own lines a hunk
    touches, on the base or the working-tree side. A symbol that only
    shifted, or moved with its text unchanged, is not changed.
  - **Affected criteria with detail**: each criterion whose invariant has a
    changed enforcer, with its invariants and the enforcers that changed.
    Approved BRDs come first; draft and proposed ones have their own
    section (`--include-drafts=false` hides it). Direct-wired criteria
    count.
  - **Removed enforcement**: an enforcer deleted, its `@enforces` tag
    dropped (the tag sits above the symbol, so a line-range test alone
    misses it), or its file deleted or moved out of the scanned tree. The
    base side is parsed from `git cat-file` with the same adapters; nothing
    is checked out.
  - **Manifest diff, field by field**, for features, BRDs and entry points:
    added, removed, renamed (paired by id) and changed, walked generically
    over each schema struct so no field is left out.
  - **Code roll-up** (optional): the SCIP blast radius of each changed
    enforcer, rolled up to modules, test files and entry points, capped by
    `--max-symbols` (default 50). Without a code graph it says so and the
    report still succeeds.
  - It writes nothing: no ledger entry, no flag, no tracked file, and git
    runs on a private copy of the index so `.git/index` is left alone too.
    The markdown form starts with `<!-- lattice-impact -->` so a job can
    update its own sticky comment.
- **`extract --with-code-graph --index-root <dir>`**: runs the SCIP
  indexers in a directory instead of the code root (repeatable), for a
  monorepo with a `tsconfig.json` per package and none at the root. The
  runs are merged into one index per language with paths relative to the
  code root. Without the flag nothing changes.
- **`backprop` documents its ledger writes**: every run records a check run,
  as `validate` does. The help and the user guide now say so and point to
  `lattice impact` for a read-only report.
- **Coverage ingest and the `exercised` level** (docs/rnd/0009, phase 1):
  Lattice can now tell a verifier that runs its enforcer from one that only
  passes, for example with the guard stubbed. See the user guide, section 4f.
  - `lattice results ingest` reads coverage reports next to test reports,
    told apart by content: LCOV, `go test -coverprofile`, istanbul
    `coverage-final.json`, coverage.py JSON (with `--show-contexts`, every
    test's lines from one run), cobertura and clover XML, and a JSON or CSV
    export of Postgres `pg_stat_user_functions` call counts.
    `--per-test <dir>` reads one report per test, named by its slug;
    `--per-file <dir>` one per test file. One run gets one commit stamp and
    one ledger event. The result goes to
    `lattice/.cache/results/coverage.json`: the commit, a dirty flag, the
    source reports with their hashes, per-symbol lines, and per test the
    symbols it executed.
  - Reports map onto symbols by line extent. The declaration line is not
    evidence, a function record decides whether a function ran, and a file a
    report does not list has no data unless it is in the report's `include`
    set.
  - `lattice results tests --verifiers [--lang] [--runner] [--json]` lists
    each verifier with its report slug and its runner's filter (Go `-run`,
    Node `--test-name-pattern`, vitest/Jest `-t`, Deno `--filter`, pytest node
    ids and `-k`, PHPUnit `--filter`, `pg_prove` files), so CI can collect
    per-test coverage in parallel. Lattice does not run tests.
  - A new top rung in the RTM, `exercised`: demonstrated, and for every
    invariant behind the criterion, a verifier's own per-test run executed an
    enforcer. Only per-test coverage promotes. An exercised row counts as
    demonstrated everywhere, so existing counts do not change. Rows carry
    `evidence`, `enforcer_coverage`, `exercised_by` and `coverage_reason`;
    summaries carry `exercised`. `lattice rtm` shows why a demonstrated row
    is not exercised.
  - New findings, all off until `config.yaml` has a `coverage:` block:
    `VERIFIER_MISSES_ENFORCER` (warning), `COVERAGE_STALE` (warning),
    `VERIFIER_UNPROVEN` (info), and `COVERAGE_BELOW_FLOOR` (warning) for each
    declared floor (`coverage.floors: {feature, invariant, tier}`). Each fires
    only on ingested data and states its evidence basis.
  - `validate.require_level` accepts `exercised`, and a per-tier form:
    `{default: demonstrated, tier: {3: exercised}}`. The tier-2+
    `MUTATION_REQUIRED_TIER` gate is unchanged.
  - `examples/coverage-prototype/` is a Node sample workspace with real
    coverage files, where one verifier stubs its guard.
- **Static reachability** (docs/rnd/0009, phase 2): a call graph, roots and
  a reach class for every runtime symbol, so dead code, test-only code and
  unused exports are findings. See the user guide, section 4g.
  - The TypeScript/JavaScript, Go, Python and Postgres adapters record raw
    references from the parse they already do (`ir.Module.Imports`,
    `ir.Symbol.Refs`, and top-level code as a `<module>.<top>` node). The
    core resolves each one through the module's imports, its own scope, and
    a member name unique in the language. Ambiguous and missing references
    stay unresolved and are counted, never guessed; for the reach classes,
    an ambiguous method call reaches only the candidates whose type reached
    code uses (Rapid Type Analysis). A SCIP index, when one exists,
    replaces this resolution for its language.
  - SQL edges: `.rpc('fn')` to `sql.<schema>.fn`, `.from('t')` to the
    table's policies and triggers, trigger to function, function body calls,
    policy expressions, and `cron.schedule` bodies.
  - Roots are automatic (entry-point handlers, `package.json`
    `main`/`module`/`exports`/`bin`, `index.html` module scripts, Deno
    `serve` handlers, cron jobs, SQL triggers and policies, Go's
    `func main`, Python's `__main__` block) plus `coverage.roots`
    (`{symbol | file: <glob>, reason}`).
  - `lattice.json` gains `symbols[].reach`: `entry`, `test_only`,
    `unreached` or `unknown`. The resolved edges go to
    `lattice/.cache/callgraph.json`, not to `lattice.json`.
  - New findings, every one off until enabled in `validate.severity` (a
    `coverage:` block does not turn them on): `SYMBOL_UNREACHED` (warning),
    `SYMBOL_TEST_ONLY`, `SYMBOL_UNTESTED` (needs suite coverage) and
    `UNUSED_EXPORT` (info). Enabling any of them brings
    `REACHABILITY_NO_ROOTS`, `RPC_UNRESOLVED` and `IMPORT_UNRESOLVED`
    (info). Runtime coverage only moves a symbol up: one that ran is never
    unreached. Interfaces and type aliases get no class.
  - Modules loaded at run time (`import('./x')`, an unbound `require`) are
    edges of the code that loads them and reach the module's top level and
    every export. A Deno function's module top level, tool config files
    (`vite.config.ts`, `playwright.config.ts`, ...) and a migration's
    top-level calls (a seed `select f(...)`) are roots.
  - `SUITE_OVERLAP` (info, off until enabled): two test suites whose
    `@verifies` targets and scenario entry points have a Jaccard index at
    or above `coverage.overlap` (default 0.8). Suites come from
    `coverage.suites`, or are the top-level test directories.
  - `examples/coverage-prototype/` shows `sql.public.debit_v1` unreached
    while `debit` is reached through `.rpc("debit")`, a test-only
    function, and a trigger-run function reached through the trigger.

### Behaviour changes to plan for

- **`FEATURE_UNREACHED` follows the call graph when it has roots.** It
  used to fire for every production feature on no entry-point flow as soon
  as the first entry point existed. Now a feature also counts as reached
  when a root reaches any of its symbols, so a project with entry points
  and resolvable calls gets fewer of these findings. A project with roots
  but no declared entry point (a Go `func main`, a SQL trigger, a
  `package.json` entry) now gets the check, where it got none before. The
  message names its evidence. Without roots nothing changes.
- **`lattice.json` grows by one field per runtime symbol** (`reach`), and
  `extract` takes longer: on this repository about 0.35 s to 0.5 s, nearly
  all of it the adapters' reference walk.

- **A half-made BRD link now warns.** Until now a feature listed in a BRD's
  `implements_via` counted as linked without its own `implements_brd`.
  It still counts as linked, but `BRD_LINK_ONE_SIDED` now warns. With
  `fail_on: warning`, run `lattice brd link` for each finding, or lower
  the code in `validate.severity`.

## [0.13.0] — 2026-10-24

### Fixed

- **SCIP flow tracing credits code to the function that contains it**:
  the call graph gave each reference to the nearest definition above it,
  without checking where that definition ends. Top-level code after a
  function counted as that function's calls, and a function's own calls
  after a nested definition went to the nested one, so entry-point flows
  could list features they never reach. A definition's `enclosing_range`
  now bounds it when the indexer records one. Without it, the old rule
  still applies.

- **A BRD an agent drafts needs review like an LLM draft**: `lattice brd
  new` run by an agent (`--actor agent:…`) recorded `source: human`, so
  `BRD_UNAPPROVED_LLM` never fired for it and a `fail_on: warning` gate let
  an unreviewed agent draft through.
  - The rule now also fires for a draft whose `provenance.authored_by` is an
    agent, and names the author.
  - `brd new` sets `human_review_required: true` for an agent.
  - `brd show` and `next` say who wrote it.
  - An agent is an actor starting with `agent`, the test that already gates
    deciding a CR and clearing a flag.

- **`adapters.yaml` is strict**: an adapter block outside `adapters:` (for
  example `postgres:` at the top level) or a misspelt adapter name was
  ignored without a word, so a language silently went unscanned. Both are
  now errors that name the fix.
- **Structural rules skip migrations**: `FILE_LINE_CAP`, `METHOD_LINE_CAP`
  and `FEATURE_NOT_COLOCATED` no longer count database migrations. An
  applied migration cannot be split, and a feature that owns rules in the
  database next to its code is not sprawling.
- **`validate` text output separates the line**: a finding's line now reads
  `(line 463)` after the message instead of being glued onto it.
- **Node's built-in test runner reports can be ingested**:
  `node --test --test-reporter=junit` writes its `<testcase>` elements
  directly under `<testsuites>`, so `lattice results ingest` failed with
  `expected element type <testsuite>`. These cases are now read, and since
  the runner names no file (classname `test`), a case titled
  `every tool description follows the template` joins the TypeScript
  adapter's `….test.every-tool-description-follows-the-template` by its tail.
  Only titles with a space get this key. A title shared by two files stays
  ambiguous and is not demonstrated.
- **`results ingest` counts test cases, not lookup keys**: each case is
  indexed under several keys, so the summary, the `--json` `total` and the
  ledger entry overstated a run (339 `go test` cases read "674 pass").
  `results ingest` and `results show` now count each case once (a case
  that failed in any report counts as a failure), via `results.Set.Tally`.

### Added

- **A success criterion can map to several invariants**:
  `maps_to_invariant` takes a list as well as a single ref
  (`maps_to_invariant: [checkout.refund:INV-1, checkout.refund:INV-2]`).
  - The criterion holds only when all of them do. In `lattice rtm` it takes
    the worst status of its invariants, and the reason, `validate` and
    `next` name the invariant that decided it.
  - Its enforcers and verifiers are the union of theirs, and the
    tier-2+ mutation gate checks each invariant.
  - This lets a criterion that spans several guards be traced guard by
    guard. A single invariant that joins them is enforced as soon as any one
    carries `@enforces`, which hid a guard that lost its tag.
  - A single ref is still written as a plain string, so existing files keep
    their shape. Graphs extracted by earlier versions still load.
- **Go adapter** (on by default, `go` in `adapters.yaml`): Lattice now reads
  `.go` files, including its own engine.
  - Functions, methods, struct and named types, and interfaces are symbols,
    named by package directory (`pkg/lattice/results` → `pkg.lattice.results`):
    `pkg.lattice.results.Lookup`, `pkg.lattice.results.Set.Lookup`. A
    package-level `const` or `var` is a symbol only when tagged. `Exported`
    follows Go's upper-case rule; a `var` initializer or an `init` function
    marks the file as runtime code (`has_code`).
  - Tags go in the doc comment directly above a declaration
    (`// @enforces feature:INV-1`), with the TypeScript tag names; module tags
    (`// @module-feature x`) in the package comment. A tag separated from its
    declaration by a blank line, trailing code, inside a function body or on
    `init` raises `ANNOTATION_ORPHANED`. A wrapped doc line that starts with
    `@` but reads as prose is not a tag.
  - Functions in `_test.go` files are tests. A `t.Run("name", ...)` subtest
    is a test of its own, named as `go test` names it
    (`pkg.lattice.results.TestLookup.a_failure_wins`), so a tag above the call
    verifies that subtest. Tags above a table test's `t.Run(tc.name, ...)` or
    its `for` loop attach to the enclosing test.
  - JUnit reports of `go test` (gotestsum, go-junit-report) join these FQNs
    with no configuration: the case `<import path>` / `TestLookup/a_failure_wins`
    ends in the test's FQN.
  - Names Go allows twice in one directory (files for different builds, an
    external test package) stay unique: a later file's symbol is
    `<fqn>@<file stem>`.
  - No SCIP indexer or mutation runner yet.

### Behaviour changes to plan for

- **Repositories with `.go` files start seeing Go modules.** The Go adapter
  is on by default, so `extract` now parses them: coverage denominators grow,
  and with `UNATTRIBUTED_MODULE` raised to an error, each untagged Go
  production file fails `validate`. Exclude what is not product code
  (`exclude:` in `workspace.yaml`), or switch the adapter off in
  `adapters.yaml` (`go: {enabled: false}`). `vendor/` is never scanned;
  files under `testdata/` are test code. Comments that start a line with
  `@enforces x:INV-1` and the other tag names now attach to the declaration
  below them.
- **A draft BRD an agent wrote now raises `BRD_UNAPPROVED_LLM`.** The
  warning used to fire only for `provenance.source: llm_from_code`. It now
  also fires for a draft whose `provenance.authored_by` is an agent (an
  actor starting with `agent`). With `fail_on: warning`, such a draft fails
  `validate` until a human approves it, or until `validate.severity`
  lowers `BRD_UNAPPROVED_LLM`.

## [0.12.0] — 2026-10-24

### Added

- **PostgreSQL adapter** (opt-in, `postgres` in `adapters.yaml`, with
  `migrations` and `tests` globs): rules enforced in the database are traced
  and demonstrated like application code.
  - Functions, row-level security policies and triggers are symbols
    (`sql.<schema>.<name>`, `sql.policy.<schema>.<table>.<name>`,
    `sql.trigger.<schema>.<table>.<name>`), tagged in a SQL comment directly
    above the statement (`-- @enforces feature:INV-N`). A feature-qualified
    `@enforces` attributes the symbol to that feature.
  - Migrations apply in filename order: the last `create or replace` wins, a
    `drop` removes the symbol, and tags count only on the effective
    definition. `SQL_TAG_DROPPED` (warning) reports a redefinition that
    loses an earlier definition's tags. Migrations are left out of module
    attribution (change history, not modules).
  - pgTAP assertions are tests named `<file>.test.<slug of the description>`,
    tagged `-- @verifies` above the assertion or above `select plan(...)`.
  - `lattice results ingest` reads TAP streams (pg_prove `--verbose` output,
    TAP 14 subtests; `# SKIP` / `# TODO` count as skips) next to JUnit.
  - Adapters can now resolve definitions across files (`adapters.Resolver`),
    run once after every file is parsed.

- **Council approval of BRDs**: in council mode (`autonomy.default_mode:
  council`), and under a `brd_approve` mandate that a human pins in
  `config.yaml`, a council of agent seats may approve a BRD. Each seat
  votes with a reason (`lattice brd vote`), and `lattice brd decide`
  approves the version when every seat approves. The approval records the
  mandate, the human it acts for, the votes, the summary and the engine's
  evidence. The decision is written to
  `lattice/decisions/brd-council/<id>-v<N>.md` and the ledger, for that
  human to `lattice brd ack` or `lattice brd overturn`. Some BRDs stay
  human-only: a tier-3 criterion, or a regulatory, legal or financial
  constraint. No seat may be held by the agent that wrote the version
  (`provenance.authored_by`), nor may one agent hold two seats.
  `BRD_COUNCIL_UNREVIEWED` (info) lists decisions not yet acknowledged.
  `BRD_COUNCIL_INVALID` (error) catches a council approval that no pinned
  mandate, floor or unanimous vote supports. `lattice next` points at the
  council path in council mode.

### Fixed

- **An unquoted `: ` in the allowlist says how to fix it**: a value such as
  `reason: enforced in SQL: see ADR-12` makes the YAML invalid, so every
  entry is dropped at once. The `ALLOWLIST_SCHEMA` error now says to quote
  the value.

### Changed

- The user guide states which modules are production code for attribution.

## [0.11.0] — 2026-10-24

### Fixed

- **Modules with top-level code are production code**: a module counted for
  attribution (and `UNATTRIBUTED_MODULE`) only when it declared a function,
  method, class, trait or const symbol. An entry file whose only statement is
  `createRoot(...).render(...)`, or a module of plain value consts, was
  invisible: removing its `@module-feature` still read 100%. The TypeScript
  adapter now records `has_code` on a module with top-level runtime code
  (anything but imports, comments, type declarations, directives and
  re-exports; never on test files), and such a module is production code.
- **A misspelt allowlist key is an error**: the allowlist was decoded
  leniently, so `allow:` instead of `entries:` (or a misspelt entry field)
  loaded an empty list with no finding, and `ALLOWLIST_STALE` could never
  fire. Unknown keys now raise `ALLOWLIST_SCHEMA`.
- **`brd approve` is recorded in the ledger**: an approval is a gate
  decision, but the command wrote only the BRD's `approval` block, so which
  identity ran it (`--actor` / `LATTICE_ACTOR`) was kept nowhere. It now
  appends a `gate` entry with the actor, the BRD and the approver.

### Added

- **Allowlist entries carry `kind` and `adr`**: an entry may classify its gap
  (`kind: generated`, `debt`, ...) and name the decision record behind it
  (`adr:`, a path relative to the directory holding `lattice/`, or a URL; it
  must exist). `validate.allowlist_kinds` in `config.yaml` makes the kinds a
  closed set, and `require_adr: true` on a kind makes its entries name an
  ADR. Accepted findings in `--json` carry `kind` and `adr`.
- **`validate.require_level`**: `verified` or `demonstrated` (or
  `lattice validate --require-level`). Every BRD success criterion and user
  scenario below the level raises `BRD_BELOW_LEVEL` (error), with the
  subject `brd.<id>:SC-N` / `brd.<id>:US-N` for the allowlist. Before, the
  RTM computed each one's level but nothing failed on it: a verified
  criterion raised no finding and an unmapped one only an info, so a gate
  on "every criterion demonstrated" had to be scripted.

## [0.10.0] — 2026-10-24

### Fixed

- **Agent worktrees are no longer scanned**: code extraction skips `.claude/`
  and any linked git worktree (a directory whose `.git` file points into
  `.git/worktrees/`). Before, an agent's checkout under `.claude/worktrees`
  was parsed as a second copy of the code, doubling symbol counts and adding
  false colocation warnings. Submodules are still scanned, and a workspace
  rooted in a worktree still works.
- **`.mjs`, `.cjs`, `.mts` and `.cts` files are parsed**: the TypeScript
  adapter now owns these extensions, and `*.spec.cjs` / `*.test.mjs` (and the
  `.mts` / `.cts` forms) count as test files. CommonJS e2e scripts can carry
  `@verifies` and join their JUnit results; the results join already accepted
  these extensions.
- **Playwright tests inside `test.describe` are found**: `test.describe(...)`
  and its `.serial` / `.parallel` / `.only` / `.skip` forms are now groups
  whose bodies are walked. Before, the group was recorded as one test named
  after the group and every test inside it was lost, so `@verifies` on
  idiomatic Playwright specs never attached. Hooks, `test.use`, `test.step`,
  `test.describe.configure` and untitled `test.skip(cond, …)` are no longer
  mistaken for tests.
- **Playwright results reach `demonstrated`**: Playwright's JUnit reporter
  names a case by its title path joined with ` › `; the join now takes the
  last part as the test title, as it already did for vitest/jest's ` > `. A
  case that fails in any browser project counts as failing.
- **React components in `.tsx` / `.jsx` are extracted**: those files were
  parsed with the plain TypeScript grammar, which cannot read JSX, so a file
  holding a function component came out with no symbols and every
  `@feature`, `@capability` or `@enforces` on a component was dropped. JSX-capable
  extensions now use the tsx grammar; `.ts` / `.mts` / `.cts` keep the
  TypeScript grammar, where `<T>expr` is a type assertion.
- **Orphaned annotations are reported**: an annotation that attaches to no
  symbol (above an object literal, a bare statement, or at the end of a block)
  now raises `ANNOTATION_ORPHANED` (warning) instead of vanishing.
- **Method length is the declaration's own extent**: symbols now carry
  `end_line`, and `METHOD_LINE_CAP` measures from it. Before, a symbol ran to
  the next symbol's start or the end of the file, so a short function last in
  its file was reported at the file's length.
- **`e2e/` is test code**: files under an `e2e/` (or `__e2e__/`) directory,
  or named `*.e2e.*`, are test files in every adapter. Browser and device
  scripts no longer count as production code.
- **Git-ignored files are not scanned**: code extraction asks git which
  untracked paths its ignore rules exclude (every `.gitignore`, nested files
  and negations included) and skips them; tracked files are always scanned.
  Outside a git work tree nothing changes.
- **Repeated check runs no longer churn the ledger**: `validate`,
  `results ingest`, `runs-clean` and `backprop` append a check-run entry only
  when its outcome differs from the latest entry for the same unit and actor.
  Before, every run appended a line to the tracked ledger, so parallel
  branches conflicted on it.
- **`init` makes the ledger merge cleanly**: it writes
  `lattice/.ledger/ledger.jsonl merge=union` to `.gitattributes` (when the
  file does not exist yet), so git keeps both sides' lines on a merge.
- **`validate` counts info findings as info**: the text output printed info
  findings as `warn` and the summary (and `--json` `warnings`) counted them
  as warnings, while `--json` gave their severity as `info`. Text, summary
  and JSON now agree: `N error(s), M warning(s), K info`, with `infos` in
  `--json`.
- **`detect` / `init` understand JS/TS monorepos**: a pnpm workspace
  (`pnpm-workspace.yaml`) or npm/yarn `workspaces` keeps its `tsconfig.json`
  and framework dependencies in the packages, so a TypeScript monorepo was
  detected as "javascript, low confidence". Detection now reads the
  workspace packages (listed as `packages`), so TypeScript and the framework
  are recognised.
- **`lattice next` on an empty workspace**: it said "every unit is
  demonstrated" when nothing was declared; it now says nothing is declared
  yet and how to start (`--json` carries `empty: true`).
- **Result lookup is deterministic and refuses to guess**: when several
  report cases match a verifier, the most specific match wins (the longest
  shared path). When two different cases match equally (the same short test
  name in two files and nothing else in common), the result is not counted
  and `validate` raises `RESULT_AMBIGUOUS` (warning) naming them. Before,
  such a verifier took a pass or a fail depending on map order. The ingested
  set records which case each key came from (`cases`).

### Added

- **`lattice next` ranks BRD approvals**: draft and proposed BRDs appear
  after every unit that needs work, as `awaiting-approval`, with the human
  approval step (an agent never approves).
- **`lattice coverage` works without `lattice import`**: it refused to run
  unless an import session existed, and documentation coverage counted only
  accepted import candidates, so a hand-written workspace read 0%. Coverage
  now comes from the graph: documentation is the share of production symbols
  attributed to a feature (plus accepted candidates, when there are any).
- **Table-driven tests are tests**: `it.each(table)("title", fn)`,
  `test.each` and `describe.each` were not recognised (the callee is itself a
  call), so their `@verifies` was dropped. They are now test symbols whose
  FQN marks each title placeholder (`%s`, `$name`, `${x}`) with `_`; the
  result join matches every row's case in the same file, and the test is
  demonstrated only when all rows pass.
- **Deno test files are test code**: `x_test.ts` (Deno's naming) is a test
  file, like `x.test.ts`; its helpers no longer count as production code.
- **More TypeScript annotation targets**: an annotated non-function `const`
  (`export const client = createClient(…)`) is a symbol; annotated methods of
  an object literal (`export const store = { get() {} }`) are method symbols
  of that const; annotated functions declared inside a function body (a
  handler inside a React component) are symbols enclosed by it. Unannotated
  ones stay out of the graph. `// @module-feature x` on one line works like
  the JSDoc form (line comments never carry symbol tags).
- **A malformed entry-point file is an error, not a silent gap**: a
  persisted entry point whose YAML does not parse (for example an unquoted
  `: ` inside a value) or that has no `id` was skipped without a word; the
  only symptoms were downstream (`FEATURE_UNREACHED`, a missing entry point).
  It now raises `ENTRYPOINT_PARSE_ERROR` (error) with the file and line.
- **Attribution coverage**: `lattice coverage` reports the share of
  production modules that belong to a feature and lists the ones that do
  not. Modules holding only tests, types or nothing are not production code,
  nor are ambient declarations (`*.d.ts`) and tool configuration
  (`*.config.*`). It is the same definition `UNATTRIBUTED_MODULE` uses.
- **Test-support files are test code**: `__mocks__/`, `__fixtures__/`,
  `testdata/`, and files named `testing.*`, `test-utils.*` /
  `test_utils.*` or `test-helpers.*` / `test_helpers.*` are test files in
  every adapter.
- **Validation policy in `config.yaml`**: `validate.severity` sets any
  finding code to `error`, `warning`, `info` or `off`; `validate.fail_on`
  (or `lattice validate --fail-on`) sets the lowest severity that fails the
  run. An unknown severity name is reported as `VALIDATE_POLICY`.
- **Allowlist**: `lattice/allowlist.yaml` accepts known findings by code and
  `subject` / `file` / `feature`, each entry with a required `reason`.
  Accepted findings are reported apart (`allowlisted` in `--json`) and never
  fail the run; an entry that matches nothing raises `ALLOWLIST_STALE`, so
  the list only shrinks. Malformed entries raise `ALLOWLIST_SCHEMA`.
- **`UNATTRIBUTED_MODULE`** (off unless enabled in `validate.severity`): a
  production module with no `@module-feature` and no feature on any symbol.
- **Findings carry a `subject`**: a stable id of what they are about
  (`brd.<id>:SC-N`, `brd.<id>:US-N`, a module file, a symbol FQN) for
  allowlists and tooling.
- **`exclude:` in `workspace.yaml`**: slash-path globs, relative to each code
  root, of files and directories never scanned. An invalid glob fails to open
  the workspace.

### Changed

- **Line caps need opting in**: `FILE_LINE_CAP` and `METHOD_LINE_CAP` run
  under `architecture.ama_mode`, or when the workspace sets `file_line_cap` /
  `method_line_cap`. AMA's 150/25 defaults fired on nearly every component of
  a project that never chose AMA. Files holding only tests are skipped.
- **`lattice results ingest` takes several reports**: `ingest unit.xml
  e2e.xml` merges them into one set, a failure in any report winning.
  Previously each call replaced the set, so unit and e2e results could not
  both count toward one criterion.

### Behaviour changes to plan for

- `validate` counts `info` findings as info, not warnings, so
  `BRD_CRITERION_UNMAPPED`, `BRD_SCENARIO_UNMAPPED`, `FEATURE_UNREACHED` and
  the other info-level codes leave the `warnings` total (and `--json`
  `warnings`). Set `validate.fail_on: info`, or raise a code with
  `validate.severity`, to keep failing on them.
- `FILE_LINE_CAP` / `METHOD_LINE_CAP` need `architecture.ama_mode` or an
  explicit cap.
- A malformed entry-point file is now an error (`ENTRYPOINT_PARSE_ERROR`).

## [0.9.1] — 2026-10-23

TypeScript test discovery and result-joining fixes.

### Fixed

- **TypeScript test names keep escape sequences**: an
  `it('the customer\'s cart …')` description was cut at the first escape (only
  the first string fragment was read), so its test FQN and name were truncated.
- **`Deno.test(...)` is recognised as a test**: `Deno.test`, `Deno.test.ignore`
  and `Deno.test.only` calls now produce test symbols, so `@verifies`
  annotations above them attach instead of being dropped.
- **vitest / jest results reach `demonstrated`**: JUnit cases are now also
  indexed as `<file without extension>.test.<slug of the it() text>`, the FQN
  shape the TypeScript adapter gives `it()` blocks. Before, `lattice rtm`
  could never upgrade a TypeScript-verified criterion past `verified`.

## [0.9.0] — 2026-10-22

Block maps. Lattice can now build, keep current and serve a block map of
any system from the catalogues its repos already publish: one graph of
blocks (a controlled logic path: input, logic, output, recursive) and the
typed edges between them, stamped with the commit each source reflects.
Everything is additive; workspaces without block sources are unchanged.

### Added — the block graph

- **`blocks`, `block_edges`, `sources`, `flows` in `lattice.json`**: blocks
  carry kind, parent, tier, system, truth (`declared` or `wired`),
  provenance and free-form attrs.
- **`lattice refresh`**: reads each source's `catalogue/v1` files (from a
  clone at its pinned ref, through git only, or from a directory), applies
  the workspace's judgments (systems, externals, groups, edges, labels,
  aliases, with id patterns and config/event rules), groups backend areas
  by endpoint, converts features to composite blocks, cuts simulated walks
  (`flows/v1`) at their first loop, and prints a one-line change summary.
- **Freshness**: each source stamp says whether its commit is `current`,
  `behind N` or `ahead N` of the live release (`release/v1`), or a
  `snapshot`.
- **Verified routes**: a source with `laravel_routes` and `verifies:` is
  scanned by a scope-aware Laravel route reader (nested groups, prefixes,
  closures mounted from variables, invokable controllers, resources);
  matching route blocks become `wired`, with the file and line.

### Added — storage and records

- **`pkg/lattice/store`**: authored records and the latest graph in files
  or PostgreSQL behind one interface; search and relation queries; stale
  writes refused. Configure with `storage:` in `workspace.yaml`.
- **`lattice record list|get|put`**, **`lattice store search|relations|export`**.

### Added — proposals and requirements

- **Initiatives as proposals**: `lattice initiative add|evidence|status`;
  `evidence/v1` verdicts, gaps and payload differences are drawn over the
  graph (verdict overlays, gap and replaces edges); `initiative show`
  checks each pin against its clone.
- **BRD records with scenarios**: Given/When/Then steps and the blocks
  that realise them; the engine rolls status and tests up per feature and
  finds the blocks two features share.

### Added — serving

- **`lattice serve`**: `/map` (lanes, systems as boxes of blocks with
  their stamp, nested diagram, relations, flow playback, proposal
  overlays) and `/views` (features with Gherkin, authoring, actors,
  services); JSON at `/api/graph`, `/api/search`, `/api/relations/{id}`,
  `/api/business`; drafts saved with `POST /api/brds`. Loopback binds
  refuse foreign `Host` headers.
- **`lattice export map|views`** (one self-contained HTML file) and
  **`lattice export features`** (Gherkin `.feature` files).

## [0.8.1]

Lifecycle governance. v0.8.0 made the graph honest about *what* is
true; v0.8.1 makes it honest about *who established it, at what
criticality, and what happens when grounded intent changes*. Everything
is opt-in and additive; new rules are info/warning only.

### Added — tiers & direct wiring

- **`BRDCriterion.tier`** (`1|2|3`) — pinned criticality: 1 default,
  2 auth/permissions/data-loss, 3 money/regulatory. Surfaced as the `T`
  column on `lattice rtm`; tier 2+ gates mutation evidence
  (**`MUTATION_REQUIRED_TIER`**, warning) and is never delegable in CR
  decisions.
- **`direct_wire: true`** — a criterion that already IS an enforceable
  sentence carries `enforced_by`/`verified_by` directly; checks treat it
  as its own invariant. Cuts 1:1 restatement-invariant padding.

### Added — flagged state (stale green never rides)

- **`pkg/lattice/flags`** + **`lattice flag raise|list|clear`** — open
  meaning flags on units, stored under `lattice/.flags/`. A flag rides
  ALONGSIDE the computed RTM status (`demonstrated⚑`), is raised by
  anyone, and is cleared only by a human (`--by`, agent actors
  refused). **`CRITERION_FLAGGED`** (info) keeps `validate` from
  reading clean while a meaning question is open.

### Added — change-request flow (CR-1…CR-5)

- **`schema.Revision`** + **`pkg/lattice/revision`** —
  `lattice/revisions/CR-<n>.yaml`: targets, verbatim previous text,
  computed impact, decision, demotions, work/retirement items.
- **`lattice cr propose|price|decide|reconverge|list|show`** — grounded
  artifacts are never edited in place. `price` computes the forward
  blast radius (criterion → invariants → enforcers → verifiers →
  scenarios → entry points, plus in-flight lease conflicts) and
  classes the change (`wording|widening|narrowing|contradiction`,
  strictest wins). `decide` is the human gate: approval applies the
  text (re-grounding), **demotes** every touched criterion to flagged,
  and spawns work items (widening) or **retirement items** (narrowing —
  the only thing that makes a test deletion legal). Contradictions are
  blocked until `--supersedes-decision` retires the conflicting
  Decision. `reconverge` closes the loop once every demotion is
  human-cleared. **`REVISION_SCHEMA`** (error) on malformed CR files.
- **`autonomy.mandates`** — human-pinned delegations with class list,
  tier ceiling, and expiry. An agent may decide only wording-class CRs
  under a covering mandate; narrowings and tier-2+ radii are
  hard-coded non-delegable.

### Added — back-propagation & the forbidden-move sweep

- **`lattice backprop [--since <ref>] [--flag]`** — after a merge, maps
  changed files → enforcer symbols → invariants → grounded criteria.
  No grounded impact → a ledgered "no doc impact". Impact → the
  affected criteria are listed (and optionally flagged) for the human
  amendment gate; the requirement never silently follows the code.
- **`pkg/lattice/sweep`** + **`lattice sweep [--update-baseline]`** — a
  verifier present at the recorded baseline that disappears without an
  approved CR retirement item is **`TEST_RETIRED_ILLEGALLY`** (warning,
  exit 1).

### Added — composed demonstration & V8

- **`lattice demonstrate [--brd <id>]`** — the QAE sign-off ritual in
  gate order: V0 runs-clean → V4 (every verifier *passed on ingested
  results*, existence is not evidence) → V5 (every scenario reaches a
  declared entry point AND is demonstrated) → V10 (tier-2+ mutation
  evidence) → sweep. Findings are the work list; open flags are
  reported alongside and routed to humans, never cleared by the
  sign-off. Green runs ledger a named `sign-off` event.
- **`AUTHOR_NOT_SEPARATED`** (info) — V8 from the ledger: a unit whose
  every attributed transition (including the move to demonstrated)
  names one actor is self-verified and needs an independent pass.

### Added — single event stream & lite profile

- Ledger **event vocabulary** (`transition|check-run|gate|cr|flag|sign-off`)
  — `validate`, `runs-clean`, `results ingest`, `backprop`, `sweep`,
  the CR flow, and `demonstrate` all append evidence lines; views are
  derived from the one stream, never from parallel logs. Pre-v0.8.1
  ledger files remain valid (kind defaults to `transition`).
- **`profile: lite`** — the immediate-payoff subset: links, grounding,
  annotations, leases, governance; deep families deferred and the RTM
  honestly capped at `verified` (lite's ceiling is *wired*;
  `demonstrate` refuses to sign off). The upgrade path is the architect
  pass over the already-grounded BRDs.

### Deferred

- **V11 `observed`** (runtime assertions against staging/production
  traffic) remains deferred — it needs a monitor integration story, not
  a schema field.

## [0.8.0]

The agent-steerable knowledge graph. v0.6 let Lattice say "this is
wired"; v0.8 lets it say "this is correctly meant **and** was
demonstrated" — and turns that honest graph into the substrate a fleet
of autonomous agents steers by. Opt-in and additive end to end: a v0.7
workspace upgrades with only new info/warning rows and an unchanged
`validate` exit code.

### Added — α. Executable scenario verifiers

- **`UserScenario.verified_by`** (schema, optional) — a BRD
  `user_scenario` becomes a verifiable unit peer to a
  `success_criterion`, listing verifier test FQNs and/or declared
  entry-point ids.
- RTM now walks scenarios (`Matrix.Scenarios`) alongside criteria; a
  test tagged `@verifies brd.<id>:US-N` resolves through the existing
  `Test.Verifies` edge — no new extraction path.
- **`BRD_SCENARIO_UNMAPPED`** (info), **`BRD_SCENARIO_UNVERIFIED`**
  (warning).

### Added — β. Entry-point journey coverage

- 6th coverage number — **journey coverage** = scenarios whose verifier
  reaches a declared entry point / total. Surfaced on `lattice rtm` and
  `lattice next`.
- **`FEATURE_UNREACHED`** (info) — production feature on no entry-point
  flow. **`SCENARIO_NO_ENTRYPOINT`** (info) — scenario verified only at
  pure-logic altitude.

### Added — γ. Demonstration (ingested test results)

- **`pkg/lattice/results`** + **`lattice results ingest <junit.xml>`** —
  parses junit/pytest/phpunit XML into `lattice/.cache/results/`.
- New top status **`demonstrated`** (verifier *passed* on the generated
  commit) above today's **`verified`** (= declared, the test merely
  *exists*); **`failing`** when an ingested result is red. Mutation
  testing continues to gate `partial` exactly as before — γ composes
  with it, it does not re-implement it. **`VERIFIER_FAILING`** (warning).

### Added — δ. Meaning fidelity

- **`ENFORCER_NOT_GUARD`** (info) — `@enforces` symbol carries the tag
  but is not a guard (function/method with a reject path).
- **`INVARIANT_UNFALSIFIABLE`** (info) and **`INVARIANT_MUTANT_SURVIVED`**
  (warning) — an invariant with no demonstrated violating path.
- **`CRITERION_INVARIANT_NARROWER`** (info, assisted) — the invariant
  does not entail its criterion. LLM-backed only when
  `agentic.llm.enabled`; a deterministic no-op otherwise, and skipped
  entirely for regulatory/legal/financial BRDs.

### Added — steering layer (§4–§7)

- **`lattice next`** — ranks the highest-value next actions by weakest
  link (furthest from demonstrated/correctly-meant), withholds units
  under another actor's lease, and points at the goal skill.
- **`pkg/lattice/lease`** + **`lattice lease acquire|release|list`** —
  file-and-git-backed work claims under `lattice/.leases/`, TTL expiry,
  cross-actor scope-overlap detection. **`LEASE_SCOPE_OVERLAP`** (info);
  **`CONCURRENT_SURFACE_CONFLICT`** code reserved.
- **`pkg/lattice/ledger`** + **`lattice ledger [rebuild]`** — append-only
  attribution ledger under `lattice/.ledger/`, every truth-level
  transition stamped with actor, evidence, and autonomy mode.
  **`UNATTRIBUTED_CHANGE`** (warning) under `require_actor`.
- **`--actor` / `LATTICE_ACTOR`** — the agent identity on every lease and
  ledger entry.
- **`config.Autonomy`** — `default_mode` (`gated|autonomous|tiered`),
  `require_actor`, per-mode `agent_may_advance`/`human_gate`. A floor no
  mode lowers: LLM-BRD approval and regulatory/legal/financial
  constraints stay human-only.
- Goal-aware `relevant_skills`: a task that verifies a BRD scenario or
  criterion surfaces `achieving-goals-with-lattice` and
  `verifying-meaning` first.

### Added — governance (gate zero & attestation)

- **`lattice runs-clean`** (alias `lattice v0`) + **`pkg/lattice/runsclean`**
  — the V0 "gate zero": clean install → build → boot → HTTP smoke
  probes, driven by a new opt-in `runtime:` config block. A green test
  suite atop an app that won't start is a critical finding, not
  progress; nothing is demonstrated while V0 fails (exit 1).
- **`autonomy.attestation`** (`self | isolated | bound`) — records who
  ran the checks. Reported on the `lattice rtm` header (and JSON);
  `self` prints the honest banner `SELF-ATTESTED RUN, governance
  simulated`. Claims must never exceed the attestation level.

### Changed

- Module moved to **`github.com/salahmyn/lattice`** (and the MCP server
  to `@salahmyn/mcp-server`) for publication.
- Dependencies refreshed: cobra 1.10, fsnotify 1.10, scip 0.6.1
  (newest release under the canonical module path); Go 1.24.

### Architecture notes

- `rtm` stays dependency-light (imports only `schema`); results are fed
  in via an `Options.ResultOf` callback wired by the CLI to
  `results.Set.Lookup`, so validation, RTM, and coverage share one
  demonstration source.
- `.cache/results/`, `.leases/`, `.ledger/` are git-tracked, regenerable
  directories — deleting them loses no source of truth.

## [0.7.0]

AMA support. Opt-in enforcement of the AI-Agentic Modular
Architecture: vertical slices, no cross-feature imports, file
and method line caps, CQRS separation, and a ≤500-word
`.ai-spec.md` per feature. Brownfield projects are unaffected
unless they explicitly turn `ama_mode` on.

### Added — α. Five built-in structural checks

- **`CROSS_FEATURE_IMPORT`** (warning / error in `ama_mode`) —
  feature A's symbols depend on feature B. Dedupes per
  (src, dst, file) so one cross-import row per edge, not per
  symbol.
- **`FEATURE_NOT_COLOCATED`** (warning) — feature's
  implementations span more than one top-level directory.
- **`FILE_LINE_CAP`** (warning) — source file exceeds the
  configured cap (default 150).
- **`METHOD_LINE_CAP`** (warning) — function/method footprint
  (approximated by start-line distance to next symbol in the
  same file) exceeds the configured cap (default 25).
- **`MIXED_COMMAND_QUERY`** (warning, `ama_mode` only) —
  capability is `mixed`. Silent outside AMA mode where `mixed`
  is the legacy default.

Schema additions (backward-compatible):
- `Capability.Kind: command | query | mixed` — defaults to `mixed`.
- `ir.Module.LineCount` + `GraphModule.LineCount` — counted from
  raw source by the extractor; adapters need no change.
- `config.Architecture` — `{ ama_mode, file_line_cap,
  method_line_cap }` with AMA-spec defaults.

### Added — β. `lattice feature spec` (.ai-spec.md emitter)

- **`pkg/lattice/featurespec`** — `Render(m)` produces the AMA
  spec §3 markdown: Title, Purpose, Inputs, Outputs, System
  Side Effects, Invariants, Errors, Capabilities. Empty
  sections are omitted; long fields collapse to single-line
  bullets; output is byte-stable + alpha-sorted across re-runs.
- **`lattice feature spec <id>`** — prints to stdout by default;
  `--out <path>` writes to a file (typically the feature folder
  alongside the code).
- **`FEATURE_SPEC_TOO_LARGE`** (info) — fires when the
  rendered spec exceeds the AMA 500-word ceiling. Decomposition
  signal, not a contract violation.
- Capability kind annotates the spec as `[command]` / `[query]`
  when set; `mixed` (legacy default) stays unannotated.

### Added — γ. `--architecture ama` scaffold

- **`lattice init --architecture ama`** scaffolds:
  - `src/Core/Contracts/README.md`, `src/Core/SharedUtils/README.md`,
    `src/Features/README.md` — code-side READMEs that describe
    the AMA rules in place, so an AI agent reading the tree
    learns the architecture without external docs.
  - `lattice/architecture/ama.md` — full cheat-sheet covering
    the four pillars and the five enforced rules.
  - `lattice/config.yaml` — appends an `architecture` block
    with `ama_mode: true` and the AMA-spec defaults.
- The interactive wizard offers the architecture choice when the
  operator picks the greenfield scope; brownfield is never
  auto-converted (Strangler Fig only — documented in the proposal).
- Never clobbers: existing files (including the `architecture:`
  block itself) are left untouched.

### Architecture notes

- AMA enforcement is fully opt-in. With `ama_mode: false` (the
  default), `CROSS_FEATURE_IMPORT` downgrades to warning,
  `MIXED_COMMAND_QUERY` stays silent, and the three line-cap
  rules fire as warnings only when files genuinely exceed the
  caps — a hygiene signal, not a contract violation.
- `pkg/lattice/featurespec` imports only `schema` so `validate`
  can reuse it for `FEATURE_SPEC_TOO_LARGE` without forming a
  cycle.
- No breaking changes. Existing v0.6 manifests, BRDs, and the
  full RTM walk all validate unchanged on the v0.7 binary.

## [0.6.0]

Verification & Visibility. Closes the three gaps in Lattice's
stated goals: full visibility (business + tech), retrieve interactions
without re-reading code, and verify "what's implemented = what was
required" at the BRD-goal level.

### Added — α. Requirements Traceability Matrix (RTM)

- **`pkg/lattice/rtm`** — walks each BRD success_criterion via
  `maps_to_invariant` to the backing invariant, its enforcer symbols,
  verifier tests, and (when present) mutation score. Per-row Status:
  `verified` / `partial` / `unenforced` / `unverified` / `unmapped` /
  `phantom`. Per-BRD `BRDSummary` with verification ratio + worst-status
  roll-up. `ComputeCoverage` gives the single "% SCs verified" number.
- **Validation rules** (shared with the dashboard via `rtm.Build`):
  - `BRD_CRITERION_PHANTOM_INVARIANT` (error) — maps_to_invariant misses
  - `BRD_CRITERION_UNVERIFIED` (warning) — covers unenforced/unverified/partial
  - `BRD_CRITERION_UNMAPPED` (info) — SC declares no invariant ref
- **`/rtm` UI page** — per-BRD summary table + full row matrix with
  enforcer/verifier counts and status badges.
- **`/coverage` 5th card** — "BRD goals" ratio.
- **`/brds/{id}` inline RTM** — per-BRD verification table on the
  detail page so the business reader sees status next to the goals.
- **`lattice rtm`** CLI — full matrix, per-BRD summaries, `--status`,
  `--brd`, `--summary` filters, `--json` for agents.
- **`lattice coverage`** gains a 5th line.

### Added — β. Journey + Actor views

- **`/journeys/{brd-id}`** — aggregates every entry point whose flow
  visits any feature in `brd.implements_via`. Single mermaid graph:
  BRD anchor → in-scope features → triggers. Answers "show me the
  X flow" with one click instead of N entry-point pages.
- **`/actors`** + **`/actors/{id}`** — reads `context.yaml` actors,
  resolves `actor.uses` against the feature graph (exact-id match
  + substring fallback), surfaces every EP they can trigger and the
  BRDs those features implement.
- **`lattice journey <brd-id>`** — JSON output (mermaid embedded).
- **`lattice actor list`** + **`lattice actor show <id>`** — same shape
  as the UI/API.
- **`pkg/lattice/views.BuildJourney`** is the canonical builder; the
  CLI, UI, and MCP all call it so journeys can never disagree.
- BRD detail page links to the journey from the features panel.
- "Actors" nav entry added to the layout.

### Added — γ. Agent surface (MCP) + semantic search

- **7 new MCP tools** in `lattice-mcp/src/tools/index.ts`
  (catalog: 23 → 30):
  - `lattice_list_brds`, `lattice_get_brd`, `lattice_get_journey`
  - `lattice_list_actors`, `lattice_get_actor_touchpoints`
  - `lattice_verify_brd`, `lattice_list_unverified_criteria`
- **Semantic search corpus** extended to BRDs (title +
  business_problem), BRD goals, success criteria, user scenarios,
  and entry-point purposes (with trigger-text fallback). Queries
  like "developers portal" or "consent scope" now surface the
  right artifact even when keyword overlap is zero.

### Architecture notes

- **No schema changes.** The RTM walks existing `maps_to_invariant`,
  enforcer/verifier graph edges, and `MutationScores`. v0.5 BRDs
  validate unchanged.
- **`pkg/lattice/rtm` imports only `schema`** so `validate` can use
  it without forming a cycle. CLI / UI / MCP / validation all branch
  on the same `rtm.Status`.
- The v0.6 surfaces deliberately don't ingest test pass/fail — that's
  v0.7+ territory. The RTM shows *declared* verification (enforcer +
  verifier exist + mutation OK). Whether the verifier passes on
  HEAD is a separate concern.

## [0.5.0]

BRD layer, onboarding wizard, Crystal brand, auto-detect.

A four-stream release that adds the missing layer above the Feature
axis (Business Requirements Documents), a first-touch experience for
new users (interactive `lattice init` → UI wizard), automatic project
fingerprinting (language + framework + SCIP plugin install), and a
full brand identity (Crystal direction from the design handoff).

### Added — BRD axis (β)
- **`pkg/lattice/schema.BRD`** — the v0.5 top-of-stack artifact.
  One BRD points at many features (`implements_via`); one feature
  has at most one BRD parent (`implements_brd`). Fields: id,
  version, status, title, business_problem, business_goals,
  stakeholders, user_scenarios, success_criteria, constraints,
  out_of_scope, approval (with `approved_version` pinning), and
  provenance (`human` | `llm_from_code`).
- **`pkg/lattice/brd`** loader: tolerates a missing `brds/` dir
  (BRD adoption is opt-in); parse failures surface as
  `BRD_SCHEMA` violations.
- **`lattice brd new|list|show|link|approve`** — full CLI surface.
  `link` writes both sides of the BRD ↔ Feature relationship;
  `approve` pins the version so a subsequent edit raises drift.
- **`/brds` and `/brds/{id}` UI pages** with status/approval/
  provenance side panels and a drift badge when the BRD version
  advances past its last approval.
- **Coverage dashboard 4th ratio** — BRD coverage = features with
  an approved upstream BRD. Same line on `lattice coverage`.
- **Validation rules** (all conservative — adoption is opt-in):
  - `BRD_PHANTOM_FEATURE` (error)
  - `FEATURE_BRD_MISSING` (error)
  - `BRD_SCHEMA` / `BRD_ID_FORMAT` / `BRD_ID_DUPLICATE` (errors)
  - `FEATURE_NO_BRD` (warning) — suppressed by reverse `implements_via`
  - `BRD_UNAPPROVED_LLM` (warning)
  - `BRD_UNREFERENCED` / `BRD_DRIFT` (info — new severity tier)

### Added — `brd from-code` (γ)
- **`lattice brd from-code <feature-id>`** and
  **`--all-unbrided`** — LLM-regenerates a draft BRD from a feature
  manifest plus the entry points that reach it, using the same
  `agentic.ToneContract` that steers feature and EP prose.
- **Hard-coded safety contract** (set by the package, never the
  model): `status=draft`, `provenance.source=llm_from_code`,
  `human_review_required=true`, `implements_via=[feature.id]`,
  `constraints=[]`. The prompt forbids inventing constraints,
  inventing stakeholders, and inventing invariant references —
  fabricated `maps_to_invariant` is silently dropped.
- **Owner fallback** — an LLM that returns "" for `business_owner`
  falls back to `feature.owners.business`.

### Added — Onboarding wizard (δ)
- **Interactive `lattice init`** by default: runs `detect`, prompts
  for scope (`greenfield` / `brownfield_full` /
  `brownfield_incremental`), writes `lattice/onboarding.yaml`,
  prints the URL to continue in the browser.
- `--no-wizard` restores the v0.4-era one-shot scaffold for
  scripts/CI; `--scope <name>` pre-answers the prompt for
  non-interactive runs.
- **`/onboarding` UI page** dispatches on `State.Step` and walks
  the user through four steps: project metadata → confirm code
  roots → install plugins → scope-specific final action.
- **`GET/POST /api/v1/onboarding`** with server-side defence:
  only the current step's owned fields are writable, and the
  install endpoint refuses any package name not in
  `detected.needs_packages` (no arbitrary subprocess from the
  browser).
- `Completed=true` redirects `/onboarding` to `/` — the wizard
  self-deletes from active nav once setup is done.

### Added — Auto-detect (`pkg/lattice/detect`)
- Inspects manifest files (`composer.json`, `package.json`,
  `requirements.txt`, `go.mod`, `Gemfile`, `Cargo.toml`,
  `pom.xml`/`build.gradle`) plus signature paths to guess
  language + framework with high/medium/low/none confidence.
- Coverage: Laravel, Symfony, Django, FastAPI, Flask, Next.js,
  NestJS, Express, Rails, Spring, Go, Rust.
- Returns suggested code roots and the SCIP indexer packages
  the stack needs (`sourcegraph/scip-php`, `scip-python`,
  `@sourcegraph/scip-typescript`, etc).
- **`lattice detect [path]`** prints the report; `--install`
  runs the package-manager commands; `--dry-run` previews them.

### Changed — Crystal brand (α)
- **UI re-skinned to the v0.5 Crystal direction.** Cool slate
  (#0F1218) base with mica-violet (#8C7DD6) accent; Geist sans,
  JetBrains Mono for code/FQNs, IBM Plex Serif for long-form
  business prose. Uppercase `LATTICE` wordmark with hex unit-cell
  mark (`⬡`) — both in the header and footer.
- **Tailwind config re-points the slate ramp to Crystal neutrals,
  so existing `bg-slate-*` / `text-slate-*` classes flip
  dark-by-default with no template rewrite.**
- **`pkg/lattice/ui/assets/static/styles.css`** is now the brand
  stylesheet: CSS variables for every Crystal token, `.badge.is-*`
  lifecycle/severity classes, `.glyph.is-*` primitive classes,
  defensive fallbacks for offline / CDN-down operation.

### Fixed
- **`coverage.html`** referenced a stale `.AttachedSymbols` field
  on the documentation stats; corrected to `.DocumentedSymbols`.
  (Latent template bug exposed by the v0.5 re-render.)

### Architecture notes
- `pkg/lattice/brd` and `pkg/lattice/onboarding` are deliberately
  agnostic of `pkg/lattice/agentic` so they can be loaded by
  `extract` without forming an import cycle. The CLI bridges
  `agentic.Provider` into `brd.LLMProvider` with a small adapter.
- Schema additions are backward-compatible: existing manifests
  validate unchanged; `Manifest.ImplementsBRD` is optional and
  defaults to empty.

## [0.4.2]

UI live updates + HTTP Basic auth.

### Added
- **SSE live updates.** `lattice serve` watches `lattice/` (skipping
  `.cache/` and `.rejected/`) via fsnotify and fans events out over
  `GET /api/v1/events` as `text/event-stream`. Pages open an
  `EventSource` on load; a default 400ms-debounced reload fires on
  any change, or a page may override `window.onLatticeChange` to
  refresh more surgically. A live-indicator dot in the footer turns
  emerald on connect, rose on disconnect.
- **HTTP Basic auth.** `lattice serve --basic-auth user:pass` adds a
  second credential path alongside `--token`. Either credential
  satisfies the non-loopback security requirement; both work
  independently so a reverse-proxy-fronted deploy can use Basic
  while CLI scriptlets keep `X-Lattice-Token`. Constant-time
  comparison; `WWW-Authenticate: Basic` on the 401 path so browsers
  re-prompt naturally.

### Notes
- OIDC and other SSO flows remain explicitly out of scope for v0.4.x.
  The recommended pattern for those deployments is a reverse proxy
  in front of `lattice serve --host 127.0.0.1`.

## [0.3.2]

EP review loop — `lattice ep` + UI accept/reject.

### Added
- **`lattice ep` subcommand tree:**
  - `lattice ep list [--status proposal|production|deprecated]`
  - `lattice ep show <id>` — full manifest plus the flow
  - `lattice ep accept <id>` — flip `status: proposal` to `production`
  - `lattice ep reject <id>` — move the manifest under
    `lattice/entry-points/.rejected/<same path>.yaml`
- **UI: accept/reject per row** on `/entry-points`. Status column +
  Accept / Reject buttons (only shown for `proposal`-status rows).
  Buttons PUT `/api/v1/entry-points/{id}/decision` which reuses the
  same `Decide()` helper as the CLI — write paths are byte-identical
  across the two surfaces.
- **`.rejected/` archive** rather than delete. Rejection is
  reversible: `mv .rejected/<path> <path>` and the next extract picks
  it back up.

## [0.4.1]

UI polish: schema-driven form, candidate drawer, diff preview.

### Added
- **Reflect-driven schema form generator** on `/config`. New
  `/api/v1/config/schema` reflects over `config.Config` and emits a
  nested `FieldSpec` tree; the page renders an `<input>` per field
  (bool→checkbox, int→number, string→text, []string→textarea, struct→
  `<details>`). PUT `/api/v1/config/fields` accepts `{paths: {a.b.c:
  value}}`, round-trips through the current YAML, applies via
  reflection, validates KnownFields-strict, and writes — type
  mismatches return HTTP 422 with a precise per-path error before any
  file is touched. Adding a new yaml-tagged field anywhere in
  `pkg/lattice/config` surfaces in the UI on next restart, no template
  change required.
- **Candidate bundle drawer** on `/import`. Clicking a candidate id
  fetches `/api/v1/import/candidates/{id}` and slides in a right-edge
  drawer with symbols + evidence + draft + Accept/Reject buttons.
  Bookmarkable `/import/{id}` route still works (Shift-click any row
  to navigate normally).
- **Diff preview on config save.** Pure-JS LCS-based unified diff
  below each textarea highlights added / removed / unchanged lines as
  the user types, with a +X / -Y summary in the action row.

## [0.3.1]

Entry-points + flows hardening on top of v0.3.0. See
[docs/v0.3.0-entry-points-proposal.md](docs/v0.3.0-entry-points-proposal.md).

### Added
- **Laravel cron detector.** Parses `$schedule->command/job/call(...)`
  chains in Console kernels; recognises the Laravel chain shortcuts
  (`->daily()` / `->everyMinute()` / `->cron('expr')` / 19 others) and
  maps them to cron expressions so `EntryPoint.Trigger.Schedule` is
  always machine-readable.
- **FastAPI HTTP detector** — proves the v0.3.0 detector framework is
  genuinely cross-framework, not Laravel-only. Matches `@app.<verb>` /
  `@router.<verb>` decorators above (optionally async) function defs;
  handles multi-line decorators with nested-paren args like
  `dependencies=[Depends(auth)]`.
- **SCIP-backed transitive flow tracer.** When SCIP indexes exist in
  `lattice/.cache/scip/`, BFS the call graph from each EP handler
  through transitive callees. New `pkg/lattice/scip/calls.go` builds
  caller→callees via "most recent definition before line L wins"
  attribution; depth-capped at 8 hops; falls back to v0.3.0
  module-proximity so the graph is never worse than before.
- **EntryPoint persistence + LLM labelling.** `lattice/entry-points/`
  becomes a peer of `lattice/features/`. `extract` merges detected EPs
  with on-disk persisted EPs (persisted purpose wins, detector flow
  fills in) and LLM-labels any EP that lacks a purpose, using the
  v0.2.1 `agentic.tone` contract — one config knob steers both feature
  and entry-point prose.
- **`lattice doctor --probe-llm`** sends one tiny round-trip via the
  configured provider and reports the verbatim response (elapsed ms,
  tokens, reply) or the raw error with a targeted suggestion
  (`upgrade_required` → "upgrade plan", `no such host` → "check
  base_url for typos or VPN", etc).
- **Doublestar exclude globs.** `import.coverage.exclude` now accepts
  `**` recursive patterns (`Modules/**/Database/Migrations/**`) via
  `github.com/bmatcuk/doublestar/v4`. The old `path.Match` was
  single-segment-only.

## [0.4.0]

Web UI — `lattice serve`. See
[docs/v0.4.0-ui-proposal.md](docs/v0.4.0-ui-proposal.md).

### Added
- **`lattice serve`** boots an HTTP server on `127.0.0.1:7070` (default)
  rendering server-side HTML and exposing a `/api/v1/*` JSON tree that
  mirrors CLI `--json` shapes. Single binary, embedded assets via
  `go:embed`, no Node, no Electron, no build pipeline.
- **Security model.** Loopback bind needs no token. `--host <non-loopback>`
  refuses to start without `--token <X>`; every request then must
  carry `X-Lattice-Token: <X>` or returns 401.
- **Pages.**
  - `/` — workspace overview (mode, code roots, last extract) + counts
    (features, entry points by kind, symbols, tests, violations).
  - `/features` and `/features/{id}` — table + detail with capabilities,
    invariants, implementations, and **reached-by entry points** (the
    inverse of EP→features).
  - `/entry-points` and `/entry-points/{id}` — grouped tables (HTTP/CLI/
    queue/...) with click-through to feature pages.
  - `/flows/{id}` — embedded Mermaid flowchart per entry point.
  - `/coverage` — three-ratio adoption dashboard + per-package table
    sorted by adoption ascending (the gaps surface first).
  - `/validation` — violations grouped by code, severity badges,
    `next_action` chip on each row.
  - `/search?q=` — global search across features / capabilities /
    invariants / entry points / symbols, ranked exact > prefix >
    substring.
  - `/import` and `/import/{candidate}` — candidates table with filters
    (package, decision) and inline Accept / Reject buttons that POST
    to `/api/v1/import/decisions` (reuses the v0.2.1 batch driver, so
    auto-promote-parents and "no draft" skip semantics are identical
    to the CLI).
  - `/config` — side-by-side textareas for `config.yaml` and
    `workspace.yaml`. PUT validates with `yaml.Decoder.KnownFields(true)`:
    typos and type mismatches return HTTP 422 with the exact line
    number; the file is never half-written.
- **JSON API.** Read endpoints (`overview`, `features`, `entry-points`,
  `coverage`, `validation`, `search`, `import/candidates`, `config`) plus
  two write endpoints (`POST /api/v1/import/decisions`, `PUT /api/v1/config`).

### Notes
- A real frontend SPA / reflect-driven schema form generator / live diff
  preview / candidate bundle drawer are v0.4.1 ideas; v0.4.0 uses
  textareas because they round-trip exactly and surface YAML parse
  errors verbatim.

## [0.3.0]

Entry-points & flows — the second axis of the knowledge graph. See
[docs/v0.3.0-entry-points-proposal.md](docs/v0.3.0-entry-points-proposal.md).

### Added
- **`EntryPoint` schema.** First-class artefact for every trigger of a
  running system — HTTP route, CLI command, scheduled job, queue worker,
  event consumer. Persists under `lattice/entry-points/` and joins the
  knowledge graph as `entry_points`.
- **Per-framework detectors** (`pkg/lattice/entrypoints/<framework>/`).
  v0.3.0 ships three Laravel detectors:
  - **HTTP routes** — parses `routes/*.php` and `Modules/*/Routes/*.php`,
    recognising `Route::<verb>('path', 'Class@method')`,
    `Route::<verb>('path', [Class::class, 'method'])`, and
    `Route::resource/apiResource(name, Ctrl::class)`. Resolves array-form
    short names through the file's `use` map.
  - **CLI commands** — classes extending `Illuminate\Console\Command`;
    command name comes from `$signature`.
  - **Queue jobs** — classes implementing `ShouldQueue`; queue name from
    `$queue` property when present.
- **Module-proximity flow tracer.** Joins entry points to features: a
  feature is reached when its implementation symbols share the handler's
  class, file, or 2-level enclosing module. Per-step capability assignment
  reuses the v0.2.1 token-overlap matcher. SCIP-backed transitive tracing
  lands in v0.3.1.
- **`lattice view entry-points`.** Markdown table per kind (HTTP shows
  method+path, cron the schedule, queue the queue name).
- **`lattice view flows [<ep-id>]`.** Mermaid flowchart per entry point —
  trigger → handler → reached features (with capability sublabel) → side
  effects.
- **Four new validation rules.** `UNCLASSIFIED_ENTRY_POINT` (warning when
  a handler reaches no feature), `DUPLICATE_TRIGGER` (warning on routing
  collisions), `PHANTOM_FLOW` (error when a flow step names a non-
  existent feature), `HANDLER_MISSING` (warning when a handler FQN is
  absent from the IR).

### Notes
- Cron/scheduler detection and full `lattice import` pipeline integration
  for entry points are deferred to v0.3.1.

---

## [0.2.1]

Brownfield ergonomics + LLM tone control. Follow-on to v0.2.0 from
real-world dogfooding on a 1900-PHP-file Laravel codebase.

### Added
- **Honest per-candidate draft outcomes + streaming progress.** `lattice
  import draft` now reports `provider: llm (openai)   outcomes: 50 LLM,
  3 cached, 2 fallback` instead of the misleading single `[llm (openai)]`
  label that hid silent fallbacks. A per-candidate line streams to stderr
  during the run so a 50-minute LLM pass is no longer silent.
- **`lattice import promote-parents`** + automatic ancestor promotion on
  every accept. A dotted feature id like `accounts.api.wrappers.subscription`
  auto-materialises its missing parents (`accounts`, `accounts.api`,
  `accounts.api.wrappers`) as umbrella manifests, eliminating the cascade
  of `SUBFEATURE_PARENT_MISSING` errors that hit the v0.2.0 dogfood.
- **Multi-`--scope`.** Every stage that targets candidates now accepts
  repeated `--scope` values: `lattice import scan --scope modules/X
  --scope modules/Y`. Promotes `scope` from string to `[]string` across
  `Options`, `CandidatesFile`, `Session` (with a backward-compat
  unmarshal). `draft` and `review` filter the candidate set at runtime —
  no more hand-filtering candidates.json.
- **Bulk review.** Two new paths replace shell-loop review:
  - `--from-file decisions.yaml` applies a YAML batch atomically.
  - `--accept-all|--reject-all --where 'package=modules/X' --where
    'confidence>=0.7'`. Predicates compose with AND. `PromoteParents`
    runs once at the end of a batch instead of once per accept.
- **`lattice import reset`** + **`lattice import undo <candidate-id>`**.
  Session-level rollback. `reset` clears decisions + draft manifests
  (`--also-features` wipes accepted features too). `undo` reverts one
  decision and deletes the generated manifest if it was an accept.
- **Heuristic capability-level sidecar.** `lattice import inscribe`
  (sidecar mode) now attempts a token-overlap match between each
  candidate symbol and the manifest's capability names+summaries+rules.
  Successful matches emit per-capability edges in `annotation-map.yaml`.
  Cut `UNIMPLEMENTED_CAPABILITY` warnings on the dogfood from 172 → 46
  (73% reduction). Inscribe message now explicit: "Sidecar mode: NO
  source files were modified." with a pointer to `--inline --apply`.
- **`agentic.tone` config block.** One knob steers the voice of every
  LLM-generated prose field (feature purposes, capability summaries,
  business narratives, annotation rationales):
  ```yaml
  agentic:
    tone:
      audience: business           # business | product | engineering | mixed
      reading_level: simple        # simple | intermediate | expert
      avoid_jargon: true
      extra_instructions: |
        Refer to merchants, not "users".
  ```
  CLI override per run: `lattice import draft --audience product`.

## [0.2.0]

Brownfield adoption complete — Inscribe. See
[docs/v0.2.0-brownfield-proposal.md](docs/v0.2.0-brownfield-proposal.md).

### Added
- **`lattice import inscribe`.** Stage 5 — the annotation writer, in two modes:
  - **Sidecar (default):** writes `lattice/import/annotation-map.yaml`, a
    feature↔symbol map the graph builder merges on every `extract` and
    `validate` as if the annotations were written in source. Adopting Lattice
    no longer requires a code-mod PR — the full graph appears with no source
    change.
  - **Inline (`--inline`):** inserts real `@feature` decorators / JSDoc /
    attributes above each accepted feature's top-level symbols.
    Placement-correct, idempotent (a symbol already annotated is skipped), and
    compile-checked — a touched file that fails to re-parse is rejected, never
    half-written. `--inline` previews the plan; `--inline --apply` writes it.
- **`lattice import uninscribe`.** Reverses an inline inscribe, removing
  exactly the blocks it wrote (recorded in `import/inscribed.yaml`),
  compile-checked. An inscribe→uninscribe round-trip restores files
  byte-for-byte.
- **Grounded LLM labeling.** `lattice import draft` now uses the configured
  LLM to name and describe each candidate from its evidence alone, falling
  back to the deterministic labeler per candidate on any failure and caching
  results by candidate id. `--no-llm` forces the deterministic path.
- **Verification coverage.** The third adoption ratio — invariants with both
  an enforcer and a verifier — reported by `lattice coverage` and
  `lattice import verify`.

## [0.2.0-beta]

Brownfield adoption, phase β — Label + Decide. See
[docs/v0.2.0-brownfield-proposal.md](docs/v0.2.0-brownfield-proposal.md).

### Added
- **`lattice import draft`.** Stage 2 — the deterministic labeler. Each
  candidate becomes a draft manifest skeleton: a `proposal`-status feature
  with a dotted id derived mechanically from the package path and `TODO`
  prose. No LLM, works air-gapped. Drafts are written to
  `lattice/import/drafts/`. (Grounded LLM labeling is a later step; the
  deterministic fallback is the structural baseline.)
- **`lattice import review`.** Stage 3 — the per-candidate review loop.
  Lists candidates with their decision state, shows a candidate's bundle
  (evidence, symbols, files, draft manifest), and records `--accept` /
  `--reject` in the session. Accepting writes the draft into
  `lattice/features/` as a real `status: proposal` manifest; the write is
  conflict-guarded so an import never clobbers an existing manifest.
- **`lattice import verify`.** Stage 4 — runs the v0.1 validation engine
  over the generated substrate, the mechanical fact-check that an import is
  correct.
- **Documentation coverage.** The share of production symbols attached to an
  accepted feature, reported by `lattice coverage` and `lattice import
  status` alongside discovery coverage.

## [0.2.0-alpha]

Brownfield adoption, phase α — Discover. See
[docs/v0.2.0-brownfield-proposal.md](docs/v0.2.0-brownfield-proposal.md).

### Added
- **`lattice import scan`.** Stage 1 of the import pipeline: pure static
  analysis, no LLM. It clusters the parsed code into **feature candidates** —
  symbol sets grouped by source directory, each carrying its evidence
  (package structure, harvested surfaces, co-located tests, shared
  supertypes), a confidence score, and a stable hashed ID. Output is written
  to `lattice/import/candidates.json` and is deterministic: identical code
  produces a byte-identical file.
- **Import session.** `lattice/import/session.yaml` persists the scan scope,
  status, and per-candidate decisions; a re-scan reconciles rather than
  discards review work. `lattice import status` reports session progress.
- **`lattice coverage`.** Reports **discovery coverage** — the share of
  production symbols clustered into candidates — overall and per package, the
  brownfield analogue of test coverage. Documentation and verification
  coverage land in phase β.
- **`--scope`** restricts `import scan` to a code-root-relative subtree so a
  large repo is adopted one bounded context at a time.
- **`import` config section** — `min_candidate_symbols` and
  `coverage.exclude` globs.

## [0.1.1]

### Added
- **Consolidated `lattice/` directory.** Every Lattice-maintained artifact —
  manifests, initiatives, tasks, decisions, schemas, config, skills, and the
  knowledge graph — now lives under one visible `lattice/` directory. The
  gitignored runtime cache moved to `lattice/.cache/`.
- **`lattice migrate`** moves a v0.1.0 flat-layout repository into the new
  `lattice/` layout.
- **Knowledge-graph sharding.** With `knowledge.sharding.enabled`, `lattice.json`
  becomes a shard index and per-group graphs are written to `lattice/graph/`.
  Strategies: `by_feature_group` and `by_size`. `graph.Load` transparently
  reassembles either form.
- **Workspace model.** A `lattice/workspace.yaml` selects `embedded` (lattice/
  inside one code repo) or `standalone` (lattice/ as its own repo governing
  external code roots) — supporting multi-repo projects and use as a git
  submodule.
- **Review mode.** When no code root is accessible, Lattice runs manifest-only
  validation (schema, dependency, initiative/task integrity), deferring the
  code-coupled checks. Auto-detected, or forced with `--review`. This lets
  PMs/QA interact with Lattice without access to code.
- **C4 architecture view.** `lattice view c4` emits all three C4 levels —
  System Context, Container, and Component — as Mermaid diagrams, or as a
  Structurizr DSL workspace with `--format structurizr`. Containers come from
  code roots, components from top-level feature groups, relationships from
  `depends_on` and surface emit/consume pairs.
- **`lattice/context.yaml`** declares the C4 Level-1 elements that code cannot
  reveal — people (actors) and external systems — so the System Context
  diagram is first-class rather than inferred.

### Changed
- The flat layout (`features/`, `work/` at the repo root) is no longer read;
  run `lattice migrate` to upgrade an existing repository.
- SCIP indexes and embeddings now live under `lattice/.cache/`.

## [0.1.0]

### Added
- Initial implementation: Go core engine and TypeScript MCP server — schema,
  CLI, tree-sitter adapters (Python, TypeScript, PHP), extract/graph/validate
  pipeline, patch engine, conflict/impact analyzer, SCIP and mutation
  orchestration, agentic capabilities, views, shipped agent skills.
