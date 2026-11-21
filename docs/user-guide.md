# Lattice user guide

This guide covers the full Lattice workflow and how to wire it into an AI
coding assistant (Claude Code, Claude Desktop, OpenAI Codex, Gemini CLI, or any
agent that can run a shell command).

- [1. What Lattice gives you](#1-what-lattice-gives-you)
- [2. Installation](#2-installation)
- [3. Core concepts](#3-core-concepts)
- [4. The project flow](#4-the-project-flow)
- [5. Day-to-day commands](#5-day-to-day-commands)
- [6. Using Lattice with an AI service](#6-using-lattice-with-an-ai-service)
- [7. The agent loop](#7-the-agent-loop)
- [8. Adopting an existing codebase](#8-adopting-an-existing-codebase)

---

## 1. What Lattice gives you

Lattice stores the **meaning** of a codebase — features, capabilities,
invariants, dependencies, decisions, work-in-flight — as structured YAML that
lives next to the source and is kept in sync mechanically.

The payoff: any engineer or AI agent can get the full context for a change
(what it does, what it must preserve, what it affects) without re-reading the
whole repo, and every change is checked against that recorded meaning.

---

## 2. Installation

The core is a single Go binary. Build it (Go 1.23+, a C toolchain for the
tree-sitter bindings):

```sh
git clone <this-repo> && cd lattice
CGO_ENABLED=1 go build -o dist/lattice ./cmd/lattice
sudo mv dist/lattice /usr/local/bin/        # or anywhere on PATH
lattice version
```

Check optional tooling (SCIP indexers, mutation runners, LLM keys) and, in a
workspace, the preflight checks of what `runtime:` and `break:` declare:

```sh
lattice doctor
```

The MCP server is a separate Node package (used in section 6):

```sh
cd lattice-mcp && npm install && npm run build
```

---

## 3. Core concepts

| Concept | What it is |
|---|---|
| **Feature** | A unit of system meaning, with a dotted id (`checkout.refund`). |
| **Manifest** | One YAML file under `lattice/features/` declaring one feature. |
| **Capability** | A named behavior of a feature, with prose rules. |
| **Invariant** | A constraint a feature must always hold (`INV-1`). |
| **Annotation** | A decorator / JSDoc tag / PHP attribute / Go doc-comment tag linking code to a manifest. |
| **Initiative** | Coordinated work proposing manifest changes, split into tasks. |
| **Knowledge graph** | `lattice.json` — the fused, queryable graph of all of the above. |

Every declared invariant needs an **enforcer** (code annotated
`@enforces_invariant`) and a **verifier** (a test annotated `@verifies`), or
`lattice validate` fails.

---

## 4. The project flow

```
  init ──▶ declare ──▶ annotate ──▶ extract ──▶ validate ──▶ commit
            │             │                         │
            │             │                         └─▶ analyze proposals
            │             └─▶ patch (edit manifests safely)
            └─▶ new feature / initiative / task / adr
```

### Step 1 — Initialize

```sh
lattice init                 # new project
lattice migrate              # upgrading a v0.1.0 flat-layout repository
```

`init` scaffolds a single `lattice/` directory holding config, the 8 shipped
agent skills, and `features/`, `initiatives/`, `decisions/`, `schemas/`. Your
source code stays where it already is. `lattice/workspace.yaml` selects
**embedded** mode (lattice/ inside one code repo) or **standalone** mode
(lattice/ as its own repo governing external code roots).

Code extraction skips what git ignores (every `.gitignore`, so build output
and agent worktrees stay out) and `.claude/`. To leave out more, list globs
relative to the code root:

```yaml
# lattice/workspace.yaml
mode: embedded
exclude:
  - "apps/legacy/**"
  - "**/*.generated.ts"
```

### Step 2 — Declare a feature

Write `lattice/features/checkout/refund.yaml` (or `lattice new feature checkout.refund`):

```yaml
id: checkout.refund
version: 1
status: production
purpose: Lets customers refund completed orders.
owners:
  business: payments
  engineering: checkout-eng
capabilities:
  - id: self_service_refund
    summary: A customer can refund their own order.
    rules:
      - The refund amount never exceeds the original charge.
invariants:
  - id: INV-1
    statement: A refund never exceeds the original charge.
```

### Step 3 — Annotate the code

Link the implementing code to the manifest. (Python / TypeScript / PHP / Go —
see [annotations.md](annotations.md).) TypeScript example:

```typescript
/**
 * @feature checkout.refund
 * @capability self_service_refund
 * @enforces INV-1
 */
export function validateRefund(amount: number, original: number): boolean {
  return amount > 0 && amount <= original;
}
```

And a verifying test:

```typescript
/** @verifies checkout.refund:INV-1 */
export function testRefundWithinCharge(): void { /* ... */ }
```

### Step 4 — Extract and validate

```sh
lattice extract            # builds lattice.json
lattice validate           # exits 0 when clean, non-zero on violations
```

`validate` reports every problem with a structured `next_action` telling you
(or an agent) exactly what to do.

Findings are `error`, `warning` or `info`; by default only errors fail the
run. A workspace tunes this in `config.yaml`:

```yaml
validate:
  fail_on: warning              # or info; `--fail-on` overrides per run
  severity:                     # per code: error | warning | info | off
    UNATTRIBUTED_MODULE: error  # opt-in: production code in no feature
    BRD_SCENARIO_UNMAPPED: warning
```

To gate on the truth level, set `require_level` (or pass
`--require-level`): every BRD success criterion and user scenario below it
raises `BRD_BELOW_LEVEL` (error), with the subject `brd.<id>:SC-N` or
`brd.<id>:US-N` for the allowlist. A red verifier raises both
`VERIFIER_FAILING` (the test fails) and `BRD_BELOW_LEVEL` (the gate is not
met); an accepted finding in `--json` `allowlisted[]` keeps its live
message, next to the entry's `reason`.

`VERIFIER_FAILING` (warning) fires once per red unit, from the ingested
results:

| Subject | Fires when |
|---|---|
| `brd.<id>:SC-N` | the criterion reads `failing` in `lattice rtm`; the message names the red tests of its invariant |
| `brd.<id>:US-N` | a verifier of the user scenario is red |
| `<feature>:INV-N` | a verifier of the invariant is red, and no criterion already reports that invariant as failing. It fires whether or not a BRD maps the invariant; a test tagged on a case (`@verifies f:INV-N#c`) counts for its invariant. It names up to three tests, then "and N more" |

The per-invariant finding reads only results for the graph's commit (or a
set, or a graph, with no commit); results ingested at another commit raise
nothing. `RESULTS_STALE` (info) names that case once: criteria and scenarios
still read `demonstrated` from such results, and no level changes. Two
commits that git shows differ only in Lattice's own ledger and cache
(`.ledger/`, `.cache/`), such as an empty commit or one that records a gate,
count as the same commit; if git cannot compare them (no git, a shallow clone)
they do not. A warning
fails the run only with `fail_on: warning` (or `--fail-on warning`); the
default is `error`.

```yaml
validate:
  require_level: demonstrated   # or verified, or exercised (section 4f)
```

The level can differ per criterion tier. A criterion takes its tier's level,
else the default; scenarios have no tier and take the default.
`--require-level` replaces the default and keeps the tier levels.

```yaml
validate:
  require_level: {default: demonstrated, tier: {3: exercised}}
```

A value `validate` cannot use is itself a `VALIDATE_POLICY` error naming the
key: an unknown `validate.severity` name or `require_level`, or a
`confirm.routes` entry that is not `review` or `signature` (section 4l).

`UNATTRIBUTED_MODULE` (off unless set) flags a production module with no
`@module-feature` and no feature on any of its symbols, so a coverage figure
over annotated code cannot hide the code nobody annotated.

A module is production code when it declares a function, method, class,
trait or const, or has top-level runtime code (a `render()` call, a plain
value const). Not production: test files and test support (`*.test.*`,
`*_test.*`, `e2e/`, `__mocks__/`, `test-utils.*`, ...), files holding only
imports and type declarations, ambient declarations (`*.d.ts`) and tool
configuration (`*.config.*`). Attribution coverage counts the same modules.

Known gaps go in `lattice/allowlist.yaml`, each with a reason:

```yaml
entries:
  - code: BRD_CRITERION_UNMAPPED
    subject: brd.billing:SC-5          # findings carry a stable subject
    reason: enforced outside the analysed languages; see decisions/ADR-0012.md
  - code: UNATTRIBUTED_MODULE
    file: src/generated/**
    reason: generated
```

Accepted findings are listed apart and never fail the run. An entry that
matches no finding is reported as `ALLOWLIST_STALE` (error), so the list
only shrinks as gaps close. An unknown key (`allow:` for `entries:`, a
misspelt entry field) is an `ALLOWLIST_SCHEMA` error, never an empty list.

An entry may say what kind of gap it is and which decision record stands
behind it. `adr` is a path relative to the directory holding `lattice/` (or
a URL), and it must exist. To make kinds a closed set, list them in
`config.yaml`; a kind can require an ADR:

```yaml
validate:
  allowlist_kinds:
    generated: {}
    debt: {}
    sql-out-of-scope: {require_adr: true}
```

```yaml
entries:
  - code: BRD_CRITERION_UNMAPPED
    subject: brd.billing:SC-5
    kind: sql-out-of-scope
    adr: docs/adr/0012-sql-invariants.md
    reason: enforced by a database constraint
```

`--json` carries each accepted finding's `reason`, `kind` and `adr`.

### Step 5 — Evolve safely

- Edit manifests through **typed patches**, never by hand:
  `lattice patch --from-file change.json --preview` then `--apply`. The engine
  refuses an apply that would introduce new errors.
- Assess a proposed change before committing:
  `lattice analyze proposal lattice/features/.../proposals/new-thing.yaml`.
- Coordinate larger work as an **initiative** with tasks and streams.
- Keep hand-written YAML canonical with `lattice fmt`. It rewrites
  features, BRDs and entry points in their schema's field order, as
  Lattice's own writers do. Run it in its own commit, so that a later
  `brd approve` or `brd link` diff shows only the change it makes.
  `lattice fmt --check` writes nothing and exits non-zero when a file would
  change, for CI. A file with a YAML comment, an anchor or alias, or a key
  its schema does not know is skipped with the reason and never rewritten.
- Keep both halves of a BRD link: a BRD's `implements_via` and the
  feature's `implements_brd`. When they disagree, `validate` warns
  `BRD_LINK_ONE_SIDED`, and `lattice brd link <brd> <feature>` writes both.
  A BRD's scope is every feature on either half, so completing a half the
  other side already declares is bookkeeping: `brd link` reports
  "reconciled an existing link: approval kept", the version stays, and the
  approval, council decision and confirmation stand (the review packet
  hashes the scope, not the list as written). Adding a feature neither half
  named is a scope change: a version someone reviewed is bumped, which voids
  its approval (`BRD_DRIFT`) and sends it back for review; a draft nobody
  reviewed keeps its version. `lattice brd link --reconcile` completes every
  one-sided link whose other half is declared and uncontested, prints what it
  changed, and leaves a feature that names another BRD for you to decide.

### Step 6 — Commit

The `lattice/` directory (manifests, config, skills, the ledger) is committed
to git. Two paths in it are not: `lattice/.cache/` (the runtime cache) and
`lattice/lattice.json` (the graph). `lattice extract` rebuilds the graph from
the source and manifests on every run, stamping the time and commit, so a
tracked graph keeps the tree permanently dirty and `lattice break` refuses to
run. `lattice init` makes `.gitignore` ignore both: it creates the file, or
appends only the missing lines to an existing one under a short comment
(your lines are never rewritten), and re-running it adds nothing. `lattice
validate` warns `GRAPH_TRACKED` or `CACHE_TRACKED` when git tracks either;
untrack with `git rm --cached lattice/lattice.json` (or `git rm -r --cached
lattice/.cache`). Wire `lattice validate` into CI with the templates in
`ci-templates/`; they rebuild the graph and upload it as a build artifact.

> **Parallel branches.** `lattice.json` is rebuilt by `lattice extract` from
> the source and manifests on every run, so teams (or agent fleets) working on
> many branches at once can gitignore it and treat it as build output. The
> ledger (`lattice/.ledger/ledger.jsonl`) is append-only evidence of
> **decisions and state changes**: gates (BRD approvals, council votes and
> decisions), CR events, meaning flags, sign-offs and truth-level
> transitions. `init` adds `merge=union` for it to `.gitattributes`, so a
> merge keeps both sides' lines. Existing workspaces can add the same line to
> `.gitattributes`.
>
> Routine check runs (`validate`, `results ingest`, `runs-clean`, `backprop`,
> `sweep`) are not decisions, so they are **not** appended to the ledger:
> each run overwrites its entry in `lattice/.cache/last-runs.json`, which is
> gitignored, and `lattice ledger runs` shows the latest one per check. Running
> a check never dirties a tracked file. Ledgers written by older versions may
> still hold `check-run` lines; they are read as before and never rewritten.

> **Multi-repo / review mode.** In standalone mode the `lattice/` directory is
> its own repository — add it to code repos as a submodule, or keep it free-
> standing. When code roots are not accessible (the PM/QA case), Lattice runs
> **review mode**: it validates manifests, dependencies, and initiatives, and
> defers the code-coupled checks to CI. Force it with `lattice validate
> --review`.

---

## 4b. Entry points & flows (v0.3.0)

The feature axis answers *what does the system know about?* The
**entry-point axis** answers *what happens when X is triggered?* It
makes HTTP routes, CLI commands, scheduled jobs, queue workers, and
event consumers first-class artefacts and traces each one through the
features its handler reaches.

Entry points are discovered automatically on every `lattice extract` by
per-framework detectors. v0.3.0 ships Laravel HTTP, CLI, and queue;
cron and other frameworks come in v0.3.1.

```sh
lattice extract                          # also detects entry points
lattice view entry-points                # table by kind
lattice view flows                       # mermaid flowcharts for all reachable EPs
lattice view flows --task ep.http.post.api.v2.refunds  # one flowchart
```

The flow tracer joins entry points to features by module proximity: a
feature is "reached" when one of its implementation symbols lives in the
handler's class, file, or 2-level enclosing module. Coarser than a SCIP
call-graph walk but works without an indexer; SCIP-backed transitive
tracing is on the v0.3.1 roadmap.

Four new validation rules surface entry-point integrity issues:
`UNCLASSIFIED_ENTRY_POINT` (handler reaches no feature),
`DUPLICATE_TRIGGER` (routing collision), `PHANTOM_FLOW` (flow step
names a non-existent feature; its next action names the entry point),
and `HANDLER_MISSING` (handler FQN not in the IR).

### Proposed entry points from what the runtime invokes

Some code runs because the platform calls it, with no route file or
command table to detect: a Deno `serve` / `Deno.serve` handler, a
`cron.schedule` job in a SQL migration, a route registered in TypeScript
(`app.get('/orders', listOrders)`). `lattice extract` turns each of these
that no declared or detected entry point covers into a **proposed entry
point** (`status: proposal`) and records the fact that produced it in
`detected_by`. Proposals are derived: extract recomputes them every run and
writes nothing to `lattice/entry-points/`, so `lattice ep list`, the views
and `validate` see them, and a root that moves simply stops being proposed.
Accepting one writes this manifest:

```yaml
id: ep.cron.nightly_x
version: 1
status: proposal
kind: cron
trigger:
  schedule: 0 3 * * *
  job: nightly-x
handler:
  symbol: sql.cron.nightly_x
  file: supabase/migrations/001_reports.sql
  line: 4
detected_by: runtime_root:cron
```

A `serve` handler takes kind `http_route`; a cron job takes kind `cron`
with its schedule and job name when they are string literals. Proposals are
never promoted by themselves: `lattice ep accept <id>` writes its manifest
with `status: production` (the flow stays derived), and `lattice ep reject
<id>` writes it straight into `.rejected/` so extract does not propose it
again. Nothing is written until you accept or reject; a manifest that
already exists is never rewritten.

The flow of a proposal comes from the call graph, not from file proximity:
one step per feature with a symbol the handler reaches through resolved
calls, sorted by feature, each step marked `basis: reach` with the symbols
it reached in `via_symbols`. A feature that sits beside the handler but is
never called gets no step. A step found by module proximity (the laravel
tracer) is marked `basis: proximity`.

A root that cannot become an entry point is not dropped silently. A route
whose handler is not a symbol of the graph (an inline function, a name the
module neither defines nor imports), and a cron job whose name or schedule
is an expression, each raise `RUNTIME_ROOT_UNPROPOSED` (info), with the
file and line. Name the handler as a function, write the job as literals, or
declare the entry point yourself in `lattice/entry-points/`.

```
info  RUNTIME_ROOT_UNPROPOSED  the cron job at supabase/migrations/002_dynamic.sql:2 has a computed name or schedule, so it has no stable key and cannot be proposed as an entry point
```

---

## 4c. The web UI (v0.4.0)

`lattice serve` boots a local HTTP server that consumes the same
KnowledgeGraph the CLI produces. Single binary, no Node, no build step.
The pages work offline: their stylesheet, fonts and the Mermaid renderer are
served from the binary, and no page requests another origin (see
[third-party notices](../THIRD_PARTY_NOTICES.md)).

```sh
lattice serve              # http://127.0.0.1:7070
lattice serve --port 8080
lattice serve --host 0.0.0.0 --token <X>   # non-loopback requires --token
```

The browser tabs map to the artefacts you already know:

| Page | What it shows |
|---|---|
| `/` | Workspace + counts (features, entry points by kind, violations) |
| `/features` · `/features/{id}` | All features; click-through to detail + *reached-by entry points* |
| `/entry-points` · `/entry-points/{id}` | HTTP/CLI/queue tables; click into the flow |
| `/flows/{id}` | Mermaid flowchart per entry point |
| `/coverage` | Three-ratio adoption dashboard + per-package gaps (the adoption roadmap) |
| `/validation` | Violations grouped by code, severity, `next_action` chips |
| `/search?q=` | Global search across features / caps / invariants / EPs / symbols |
| `/import` · `/import/{candidate}` | Candidates table with filters + inline Accept/Reject |
| `/config` | `config.yaml` + `workspace.yaml` editor with strict-validation save |

A `/api/v1/*` tree mirrors every page as JSON, same shape the CLI emits
with `--json`. The import-decisions endpoint reuses the v0.2.1 batch
driver so a UI accept and a CLI accept produce byte-identical state.

`/api/graph` and `/api/v2` reuse the last read-only graph and audit while
nothing they read has changed: git HEAD, every changed or untracked file,
everything under `lattice/` (`.cache/` included), and the files the
entry-point detectors read. Any change computes them again, and when that
state cannot be named the request computes afresh. The `X-Lattice-Cache`
response header says `hit`, `miss` or `bypass`.

Security defaults: loopback bind needs no token. Any non-loopback host
refuses to start unless `--token <X>` is supplied, and every request
then must carry `X-Lattice-Token: <X>`.

---

## 4d. Database rules (PostgreSQL)

Rules a database enforces count like rules in application code: a function,
a row-level security policy or a trigger can enforce an invariant, and a
pgTAP assertion can verify it, up to `demonstrated`.

The adapter is opt-in in `lattice/adapters.yaml`. Globs are relative to the
code root:

```yaml
adapters:
  postgres:
    enabled: true
    migrations: ["supabase/migrations/*.sql"]
    tests: ["supabase/tests/**/*.sql"]
```

With no globs, files under a `migrations/` directory are migrations and
files under a test directory are pgTAP tests. Any other `.sql` file is read
as schema definitions.

**Enforcement.** Tag the statement in a SQL comment directly above it:

```sql
-- @enforces refunds:INV-1
create or replace function public.refund(p_payment uuid) returns void ...;

-- @enforces refunds:INV-2
create policy "Owners read own refunds" on public.refunds for select using (owner = auth.uid());
```

Symbols are `sql.<schema>.<function>`, `sql.policy.<schema>.<table>.<name>`
and `sql.trigger.<schema>.<table>.<name>`. A feature-qualified `@enforces`
attributes the symbol to that feature; `@feature` overrides it.

**Migrations are applied in filename order.**
- The last `create or replace` is the effective definition, and a
  `drop` removes the symbol.
- Tags count only on the effective definition. A redefinition that loses
  a tag raises `SQL_TAG_DROPPED`, naming the older file, so a reader of an
  outdated definition is not misled.
- Migrations are change history, not modules: they are left out of
  module attribution, and their symbols carry their own tags.

**Verification.** pgTAP assertions are tests named by their description:

```sql
-- @verifies refunds:INV-1
select throws_ok($$ select public.refund(...) $$, 'P0001', 'already refunded', 'a payment cannot be refunded twice');
```

`-- @verifies` above `select plan(...)` applies to every assertion in the
file. An assertion without a description cannot be joined to a result; if
it is tagged, Lattice says so.

**Results.** Ingest the TAP stream together with the other reports:

```sh
pg_prove --verbose supabase/tests/*.sql > db.tap   # or capture `supabase test db`
lattice results ingest db.tap unit.xml e2e.xml
```

A `.tap` file, or any report that does not start with `<`, is read as TAP.
A `# SKIP` or `# TODO` line counts as a skip.

---

## 4e. Go

The Go adapter is on by default (`go` in `lattice/adapters.yaml`). Tags go in
the doc comment directly above a declaration, with the TypeScript tag names;
module tags go in the package comment. See [annotations.md](annotations.md#go).

```go
// @enforces checkout.refund:INV-1
func Settle(r Refund) error { ... }

func TestSettle(t *testing.T) {
    // @verifies checkout.refund:INV-1
    t.Run("rejects a double refund", func(t *testing.T) { ... })
}
```

Symbols are named by package directory (`pkg.checkout.refund.Settle`), tests
and subtests as `go test` names them
(`pkg.checkout.refund.TestSettle.rejects_a_double_refund`). A JUnit report of
`go test` therefore joins without configuration:

```sh
gotestsum --junitfile unit.xml ./...     # or: go test -v ./... | go-junit-report > unit.xml
lattice results ingest unit.xml
```

Lattice does not read `go test -json` directly; convert it to JUnit first.
The join assumes `go.mod` sits at the code root, so the package directory is
the tail of the import path. There is no Go SCIP indexer or mutation runner
yet.

---

## 4f. Coverage: does the verifier run the enforcer?

A passing `@verifies` test makes a criterion `demonstrated`. It does not
show that the test ran the `@enforces` guard: the guard may be stubbed, or
the test may return early. Coverage shows what ran. Lattice reads the
coverage reports your CI writes, maps them onto symbols, and answers one
question per invariant: did a verifier's own run execute an enforcer?
Lattice never runs your tests.

### Ingest

Coverage reports go in with the test reports of the same run. Each file is
told apart by its content:

| Format | Written by | Notes |
|---|---|---|
| LCOV (`TN:` / `SF:` records) | c8, nyc, vitest, Jest, `node --test --test-reporter=lcov`, `deno coverage --lcov`, `coverage lcov` | `FN`/`FNDA` records decide whether a function ran |
| Go profile (`mode:` first line) | `go test -coverprofile` | paths are import paths; Lattice joins them by suffix |
| istanbul `coverage-final.json` | nyc, c8 `--reporter=json`, vitest, Jest | `fnMap` gives function records |
| coverage.py JSON | `coverage json` | with `--show-contexts`, one run gives every test's lines |
| cobertura XML | coverage.py `xml`, c8/nyc `--reporter=cobertura` | `<method hits>` gives function records |
| clover XML | c8/nyc `--reporter=clover`, PHPUnit `--coverage-clover` | `type="method"` lines give function records |
| Postgres call counts | your CI, from `pg_stat_user_functions` | see below |

```sh
lattice results ingest junit.xml coverage/lcov.info --per-test coverage/per-test
```

- `--per-test <dir>` reads one report per test. Each file is named by its
  test's slug, as `lattice results tests` prints it
  (`coverage/per-test/rejects-an-overdraft.lcov`). The extension is
  ignored.
- `--per-file <dir>` reads one report per test file, named by the file's
  slug (`wallet-test.json` for `tests/wallet.test.js`). This is weaker
  evidence. It can show that no test in the file ran the guard, but it can
  never make a criterion exercised.
- Test reports still go to `lattice/.cache/results/results.json`.
  Coverage goes to `coverage.json` beside it, stamped with the commit and a
  dirty flag. The flag is set when tracked files under the workspace have
  uncommitted changes; untracked files and Lattice's own `.ledger/` and
  `.cache/` do not count.
- After the totals, the ingest lists the failed tests (up to ten, then
  "and N more"), each with the `@verifies` refs of the tests it is the
  result of. `--json` carries all of them in `failed`, as
  `{"case", "verifies"}`:

  ```
  ingested 11881 test results (11878 pass, 3 fail, 0 skip)
  failed:
    e2e/orders.spec.ts::orders › cancels a paid order  (@verifies orders:INV-9)
    tests/fees.test.ts::fees › rounds to the cent  (@verifies fees:INV-1#a)
    tests/statement.test.ts::statement › prints a header
  ```
- An ingest with only coverage reports leaves the test results in place.
  An ingest with no paths reads the reports listed in `config.yaml`.
- Reports and contexts that name no test, or fit several, are listed as
  warnings and left out. Nothing is guessed.

How a report maps onto a symbol:

- A symbol's coverage is its instrumented lines after its declaration
  line, up to its end line. V8, istanbul and coverage.py mark a
  declaration line as run when the module loads, so that line is no
  evidence. Go blocks never do, so a one-line Go function counts its own
  line.
- A function record at the declaration line decides whether the symbol
  ran, when the format has one.
- A file a report does not list has no data, unless it is inside the
  report's `include` set, where it ran no line. Node's runner lists only
  the files a run loaded; vitest with `coverage.include` lists the rest
  with zero hits.

### Configure

```yaml
coverage:
  reports:                              # read by `results ingest` with no paths
    - path: apps/web/coverage/lcov.info
      format: lcov                      # optional: detected from the content
      root: apps/web                    # prefix the report's paths are relative to
      include: ["src/**"]               # files the run instrumented, relative to root
    - path: coverage.json               # coverage.py json --show-contexts
  per_test:
    only_verifiers: true                # default: keep per-test reports of @verifies tests only
    dirs:
      - dir: apps/web/coverage/per-test # one report per test, named by its slug
        root: apps/web
        include: ["src/**"]
      - dir: coverage/per-file
        basis: per_file                 # one report per test file
  floors:                               # percent of body lines; each declared floor is opt-in
    feature: 60                         # a feature's runtime symbols, over the suite
    invariant: 80                       # an invariant's enforcers, over its verifiers' runs
    tier: {2: 90, 3: 100}               # a BRD criterion's weakest invariant, by tier
```

Paths are relative to the directory holding `lattice/`. When a path given
on the command line matches an entry here, the entry's `format`, `root` and
`include` apply to it. `coverage: {}` turns the findings on with nothing
else configured. It does not turn on the reachability codes; the same
block's `roots`, `suites` and `overlap` keys belong to section 4g.

### Collect per-test coverage in CI

`lattice results tests --verifiers` lists each verifier with its report
slug and the arguments that make its runner run it alone. Text output is
one tab-separated line per test and runner (runner, slug, file or Go
package, arguments); `--json` gives the same as data. Use `--lang` and
`--runner` to narrow it.

| Runner | Filter | Example |
|---|---|---|
| `go` | `-run '^TestX$'` (subtests `^TestX$/^name$`) | `go test ./pkg/refund -run '^TestSettle$' -coverpkg=./... -coverprofile=cov/testsettle.cover` |
| `node` | `--test-name-pattern '^title$'` | `node --test --experimental-test-coverage --test-reporter=lcov --test-reporter-destination=cov/<slug>.lcov --test-name-pattern '^rejects an overdraft$' tests/*.test.js` |
| `vitest`, `jest` | `-t 'title$'` | `vitest run tests/wallet.test.ts -t 'rejects an overdraft$' --coverage` |
| `deno` | `--filter '/^title$/'` | `deno test --coverage=cov/raw --filter '/^rejects an overdraft$/'` |
| `pytest` | a node id (`pytest-k`: `-k name`) | coverage.py contexts need no loop (below) |
| `phpunit` | `--filter '/::testX$/'` | |
| `pg_prove` | the test file | pgTAP runs a file as one unit |

A parameterized TypeScript test (a template-literal title, a test in a loop,
`test.each`) is listed once, with `"parameterized": true`. Its filter
matches every expanded name: each placeholder becomes `.+?`, so
`` header fits on ${view.name} `` gives `-t 'header fits on .+?$'`. The
`{filter}` of a `break.runners` command (vitest `-t`, Playwright `-g`) is
built the same way.

A loop over Go verifiers, eight at a time:

```sh
lattice results tests --verifiers --lang go --json |
  jq -r '.tests[] | [.package, .filters.go[1], .slug] | @tsv' |
  xargs -P 8 -L 1 sh -c 'go test "$0" -run "$1" -count=1 -coverpkg=./... -coverprofile="coverage/per-test/$2.cover"'
lattice results ingest unit.xml coverage/all.cover --per-test coverage/per-test
```

Python needs no loop. coverage.py records the test behind each line in one
run:

```ini
# .coveragerc
[run]
dynamic_context = test_function
```

```sh
coverage run -m pytest && coverage json --show-contexts -o coverage.json
lattice results ingest junit.xml coverage.json
```

pytest-cov's `--cov-context=test` works too. Only the `run` phase of each
test counts. Every context is kept, whatever `only_verifiers` says, since
the data is already there.

`examples/coverage-prototype/regen.sh` is a complete Node example: one
suite run, then one run per test.

### What it changes

The RTM gains a top rung, `exercised`. A criterion is exercised when it is
demonstrated and, for every invariant behind it, at least one verifier's
own per-test run executed at least one enforcer. A direct-wired criterion
uses its `enforced_by` and `verified_by`. A user scenario is exercised when
a verifier's run executed the handler of an entry point it names.

- Only per-test coverage promotes. Per-file and suite coverage are reported
  as the row's `evidence`, but never promote.
- `demonstrated` keeps its meaning. An exercised row counts as demonstrated
  in every count: 35/35 demonstrated stays 35/35, and "exercised N/35" is a
  new number beside it.
- `lattice rtm` shows, under each row with coverage, who exercised it, or
  why a demonstrated row is not exercised, and how much of the enforcers
  ran. `--json` rows carry `evidence` (`per_test`, `per_file`, `suite` or
  `none`), `enforcer_coverage`, `exercised_by` and `coverage_reason`; BRD
  summaries carry `exercised`.
- Floors are separate. A row can be exercised and still below its floor.

Findings. Each one but `RESULTS_STALE` is off until the workspace has a
`coverage:` block (or sets its severity); each fires only when its data was
ingested, and states the evidence basis it rests on:

| Code | With `coverage:` | Fires when |
|---|---|---|
| `VERIFIER_MISSES_ENFORCER` | warning | every verifier of an invariant has evidence (its own run, its file's run, or a suite that never ran the enforcer) and none executed an enforcer |
| `VERIFIER_UNPROVEN` | info | per-test coverage was ingested, and a verifier has none; the finding names the tests |
| `COVERAGE_STALE` | warning | the coverage was ingested at another commit than the graph's, or from a dirty tree |
| `RESULTS_STALE` | info, always on | the test results were ingested at another commit than the graph's (one that differs only in Lattice's ledger and cache counts as the same). Criteria and scenarios still read `demonstrated` from them; the per-invariant `VERIFIER_FAILING` and the audit ignore them. It changes no level; raise it with `validate.severity: {RESULTS_STALE: warning}` |
| `COVERAGE_BELOW_FLOOR` | warning, per declared floor | a feature, invariant or criterion tier is under its floor; an invariant is measured over its verifiers' runs when each has per-test data, else over the suite, and the finding says which |

`validate.severity` entries always win, for example
`VERIFIER_MISSES_ENFORCER: error`. With `require_level: exercised` (or a
tier level), a demonstrated criterion that is not exercised raises
`BRD_BELOW_LEVEL`, and the reason says why ("no per-test coverage
ingested", "no verifier's per-test coverage executed an enforcer"). The
tier-2+ `MUTATION_REQUIRED_TIER` gate is unchanged, so a tier-3 criterion
under `tier: {3: exercised}` needs both a verifier that runs its guard and
mutation evidence that the test notices when the guard is wrong.

Known gaps go in the allowlist, under the invariant (`checkout:INV-2`), the
feature id, or the criterion (`brd.<id>:SC-N`). To keep kinds apart,
declare them, for example:

```yaml
validate:
  allowlist_kinds:
    generated: {}                # generated clients and types
    framework: {}                # reached by reflection, DI or routing the graph cannot see
    debt: {require_adr: true}    # a verifier that knowingly stubs its enforcer, awaiting a fix
```

### Postgres: function call counts

Postgres counts calls per function when `track_functions` is on. That is
enough to say whether a pgTAP test (or test file) executed a function, so
it maps onto `sql.<schema>.<fn>` symbols as per-test or per-file evidence.
No extension is needed. Line and branch coverage would need
`plpgsql_check`'s profiler; Lattice does not read it yet.

Set it up once in the local or CI test database, as a superuser:

```sql
alter system set track_functions = 'pl';   -- or 'all' to count SQL-language functions too
select pg_reload_conf();
```

Then, per test file: reset the counters, run the file, and export the
counts. A backend flushes its counters when it exits, so the export runs in
a new session after `pg_prove` ends:

```sh
echo "file,test,schema,function,calls" > pg-calls.csv
for f in supabase/tests/*.sql; do
  psql "$DB" -qAt -c "select pg_stat_reset()"
  pg_prove --verbose "$f" >> db.tap
  psql "$DB" -qAt -c "\copy (select '$f', '', schemaname, funcname, calls from pg_stat_user_functions) to stdout with csv" >> pg-calls.csv
done
lattice results ingest db.tap pg-calls.csv
```

The export is CSV with a header, or JSON:

| Column | |
|---|---|
| `schema`, `function`, `calls` | required: one row per function and run |
| `file` | the test file the run covers: per-file evidence |
| `test` | the test it covers, as a test FQN or `<file>::<description>`: per-test evidence |
| `calls_before` | optional: subtracted, when you snapshot before a run instead of resetting |

```json
{
  "format": "pg_stat_user_functions",
  "runs": [
    {"test": "supabase/tests/wallet_test.sql::a debit lowers the balance",
     "functions": [{"schema": "public", "function": "debit", "calls": 1}]},
    {"file": "supabase/tests/wallet_test.sql",
     "functions": [{"schema": "public", "function": "debit", "calls": 3}],
     "before":    [{"schema": "public", "function": "debit", "calls": 1}]}
  ]
}
```

Per test inside one file, snapshot `pg_stat_xact_user_functions` before and
after each assertion in the test's own transaction, and export both as
`calls_before` and `calls`. Every run in the export is kept, whatever
`only_verifiers` says.

Limits:

- Postgres inlines simple SQL-language functions into the calling query
  and never counts them, even with `track_functions = 'all'`. An enforcer
  that is such a function cannot reach `exercised`; write it in PL/pgSQL,
  or allowlist the criterion with a reason.
- Call counts carry no lines, so Postgres symbols add nothing to floors.
- This format was defined without a running Postgres server. The SQL
  above follows the Postgres documentation and has not been run here.

## 4g. Reachability: what does nothing reach?

`lattice extract` builds a call graph from the code it already parses,
finds the roots the runtime starts from, and gives every runtime symbol a
reach class. Four findings then report dead code, test-only code,
untested code and unused exports. Each one is off until you enable it.

### The call graph

Each adapter records the references in a symbol's body as written. The
core resolves them.

| Adapter | References | Resolution |
|---|---|---|
| TypeScript / JavaScript | imports, `export … from`, `export default`, `require('./x')` bound to names; `import('./x')` and an unbound `require('./x')` as modules loaded at run time; calls, `new C()`, JSX `<C />`, named values (a callback, a default argument); `.rpc('fn')`, `.from('table')`, `.schema('s')`, and a project's own `rpc('fn', …)` wrapper | relative paths (with or without an extension, or a directory's `index`); `@/`, `~/` and `#/` aliases by a unique path suffix |
| Go | imports; calls, composite literals, types, function values; a method's receiver; a local's declared type (`func f(kg schema.KnowledgeGraph)`, `var x T`, `x := &T{}`) | an import path is local when it is `<module>/<dir>`; the module path is inferred from the imports, not read from `go.mod`. An unexported method name (`x.run()`) only matches the caller's own package. A name declared again in a build-constrained file (`open_unix.go` beside `open_other.go`) is reached with the first declaration |
| Python | `import`, `from … import` (relative too), calls, `self.m()`, named values, supabase-py `.rpc()` / `.table()` | exact module, `__init__.py` for a package, or a unique `src/`-layout suffix |
| Postgres | a function body's calls (`f(`, `perform f(`, `select f(`; comments skipped), a trigger's `execute function f()`, a policy's `using` / `with check` calls, `cron.schedule` bodies, and the calls of a file's other top-level statements (a seed `select f(…)`, a `do` block, a check or default) | `schema.name` as written; an unqualified name in `public` |
| PHP | none yet | its symbols are `unknown` |

A reference resolves, in order, through the module's imports, through its
own scope (the enclosing function, then the module or package), and, for a
method called on a value of unknown type, through a member name that is
unique in the language. Anything else stays unresolved and is counted,
never guessed:

- **ambiguous**: several members share the name (`x.Parse()` with five
  adapters that have `Parse`), so no edge is drawn;
- **missing**: it names a module of the workspace, or a database function,
  that has no such symbol;
- **dynamic**: `.rpc(name)` with a computed name, or `import(x)` /
  `require(x)` of a computed module. With the reachability codes on, each
  one is an `RPC_UNRESOLVED` or `IMPORT_UNRESOLVED` info, so it cannot
  hide a live function.

A module loaded at run time (`lazy(() => import('./admin/App').then(…))`,
an `import('./boot')` inside a function, an unbound `require('./x')`) is an
edge of the code that loads it. Which of its exports that code then uses
is not told apart, so the module's top level and every export are reached:
it errs towards reached, never towards dead. A `require` bound to names
(`const { a } = require('./x')`) stays as precise as an `import`.

A reference to the standard library, a dependency, a builtin or a local
variable resolves to nothing and is not counted as unresolved. A method
called on the result of an outside call (`time.Now().Add(d)`) never falls
back to a workspace method of the same name.

A few runtime conventions are edges too: Go's `String`, `Error`,
`MarshalJSON`, `UnmarshalYAML`, `Len/Less/Swap` and the like are reached
with their type; a Python class reaches its `__dunder__` methods; `new C()`
reaches C's constructor; a module's top-level code runs when another
module imports it.

When a SCIP index exists for a language (`extract --with-code-graph`), it
replaces this resolution for that language: each reference goes from its
innermost enclosing definition (bounded by `enclosing_range`) to the
referenced symbol.

The resolved edges, the unresolved references, the roots and the counts
go to `lattice/.cache/callgraph.json`. `lattice.json` keeps only each
symbol's class, in `symbols[].reach`.

### Roots

Automatic roots, and nothing guessed in between:

- the handlers of declared and detected entry points;
- entry files named by build configuration: a `package.json`'s `main`,
  `module` and `exports` (a library: the file's exports are roots) and
  `bin` (a script: its top-level code), and an `index.html`'s
  `<script type="module" src>`. A build output maps back to its source
  (`dist/index.js` to `src/index.ts`, or through `tsconfig.json`'s `outDir`
  and `rootDir`);
- a top-level `serve(...)` or `Deno.serve(...)` handler, and the top-level
  code of its module (what it builds for the handler);
- tool config files, which a build or test tool loads by name:
  `<tool>.config.{js,ts,mjs,cjs,mts,cts}` (vite, vitest, playwright,
  eslint, tailwind, postcss, capacitor, ...). Their top-level code and
  exports are roots, so what they import is reached. They stay outside
  attribution, as before;
- `cron.schedule` bodies;
- SQL triggers and policies (the database runs them), and the other
  top-level statements of a migration or schema file that call a function
  (a seed `select f(…)`, a `do` block, a check constraint, a default): the
  database runs them when it applies the file. `grant`, `revoke`,
  `comment`, `drop` and `alter function` only name functions and are no
  roots;
- Go's `func main` in package `main`, and Python's
  `if __name__ == "__main__":` block.

Declare the rest: route tables, framework conventions, a library's
exported API.

```yaml
coverage:
  roots:
    - symbol: "pkg.lattice.*"             # an FQN, or a glob over FQNs
      reason: the Go library API, called from outside the workspace
    - file: "src/routes/**"               # a glob over graph file paths
      reason: route modules, loaded by the router by convention
```

A `file:` root makes the file's top-level code and its exported symbols
roots.

### Reach classes

| Class | Means |
|---|---|
| `entry` | a root reaches it through resolved edges, or it is a method an ambiguous call from reached code names and reached code also uses its type |
| `test_only` | no root reaches it, a test does |
| `unreached` | nothing reaches it, not even through an ambiguous reference that names it |
| `unknown` | there are no roots; or its language has no reference data; or only an ambiguous reference reaches it and nothing reached uses its type |

Only runtime symbols get a class: functions, methods, classes, consts and
traits. Interfaces and type aliases (kind `interface`) run nothing, so
their `reach` is empty; tests are in `tests[]`, not `symbols[]`, and have
none either.

An ambiguous call stays unresolved in the call graph. For the classes,
it reaches the candidates whose type reached code builds or names, and no
others: Rapid Type Analysis, the rule of Go's own `deadcode` tool. So
`ad.Parse(...)` through the `LanguageAdapter` interface reaches each
adapter's `Parse`, because the registry builds every adapter, while a type
nothing builds keeps its methods `unknown`.

Runtime coverage (section 4f) only moves a symbol up: a symbol the suite
ran is never `unreached` (it counts as `test_only`), and one that did not
run keeps its class. `lattice.json` holds the static class; `validate`
folds coverage in.

### Findings

All are off until enabled in `validate.severity`; the severity you set
there is the one reported. A `coverage:` block does not turn them on,
because they depend on how complete the call graph is. The second column
is the severity each is designed for.

| Code | Severity | Fires when |
|---|---|---|
| `SYMBOL_UNREACHED` | warning | reached from no root, no test and no reached symbol, and it did not run |
| `SYMBOL_TEST_ONLY` | info | only tests reach it (statically, or it ran only under the suite) |
| `SYMBOL_UNTESTED` | info | a root reaches it, but suite coverage says no test executed it; needs ingested coverage |
| `UNUSED_EXPORT` | info | exported, top-level, and no other module imports or references it; roots are exempt |
| `REACHABILITY_NO_ROOTS` | info, with any of the four above | the graph has no roots; it replaces every per-symbol finding |
| `RPC_UNRESOLVED` | info, with any of the four above | an `.rpc()` with a computed name |
| `IMPORT_UNRESOLVED` | info, with any of the four above | an `import()` or `require()` of a computed module |
| `SUITE_OVERLAP` | info | two test suites verify nearly the same targets (below) |

```yaml
validate:
  severity:
    SYMBOL_UNREACHED: warning
    SYMBOL_TEST_ONLY: info
    UNUSED_EXPORT: info
```

Every message states its evidence: the static call graph and its root
count, plus suite coverage when it was used. Subjects are FQNs, so the
allowlist takes them as they are:

```yaml
entries:
  - code: SYMBOL_UNREACHED
    subject: "src.generated.**"
    kind: generated
    reason: the API client is generated; only part of it is used
```

**`FEATURE_UNREACHED` changes when the graph has roots.** A feature also
counts as reached when a root reaches any of its symbols, and the rule
runs even with no entry point declared. Without roots it keeps its
flow-based rule.

### Suite overlap

`SUITE_OVERLAP` compares what two test suites claim to verify: the
`@verifies` targets of their tests, plus the entry points named in
`verified_by` of the scenarios their tests verify. It fires when the
Jaccard index of the two sets is at or above `coverage.overlap` (default
0.8).

```yaml
coverage:
  suites:                          # name -> globs over graph file paths
    checkout-e2e: ["e2e/checkout/**"]
    journeys: ["tests/journeys/**"]
  overlap: 0.8
```

Without `coverage.suites`, each top-level test directory (`tests/`,
`test/`, `e2e/`, `__tests__/`, `spec/`, `cypress/`, `playwright/`) is one
suite, and tests elsewhere belong to none. This is a static check on
declared tags. Per-test end-to-end coverage (Playwright) would compare what
the suites actually run; that is future work.

### The example

`examples/coverage-prototype/` enables the four codes:

```sh
cd examples/coverage-prototype
lattice extract
lattice results ingest coverage/junit.xml coverage/all.lcov --per-test coverage/per-test
lattice validate
```

```
warn  SYMBOL_UNREACHED   sql.public.debit_v1 is reached from no root, no test and no reached symbol (evidence: static call graph, 2 root(s); no coverage for its file)
info  SYMBOL_UNTESTED    src.cli.main is reachable from a root (entry point ep.cli.wallet), but no test executed it (evidence: static call graph, 2 root(s); suite coverage)
info  SYMBOL_TEST_ONLY   src.statement.formatStatement is reached only from tests: no root reaches it (evidence: static call graph, 2 root(s); suite coverage: it ran)
info  SYMBOL_UNTESTED    src.wallet.assertSufficientFunds is reachable from a root (entry point ep.cli.wallet), but no test executed it (evidence: static call graph, 2 root(s); suite coverage)
info  SYMBOL_UNTESTED    src.wallet.debitInDatabase is reachable from a root (entry point ep.cli.wallet), but no test executed it (evidence: static call graph, 2 root(s); suite coverage)
info  UNUSED_EXPORT      src.wallet.assertPositive is exported, but no other module imports or references it (evidence: static imports and references)
info  UNUSED_EXPORT      src.wallet.assertSufficientFunds is exported, but no other module imports or references it (evidence: static imports and references)
```

`sql.public.debit` is reached through `client.rpc("debit")`; its replaced
`debit_v1` is not. `public.touch_account` is called by no code, but the
database runs it through a trigger, so it is reached. The run writes
`lattice/lattice.json`, `lattice/.cache/` and `lattice/.ledger/` there, so
run it in a copy to keep the example as committed.

### Limits

- Calls through an interface, and methods on a value whose type the
  adapter could not read, are ambiguous whenever two types share the
  method name. Rapid Type Analysis takes the candidates whose type is
  used; that over-approximates, so a method of a used type can read
  `entry` when no call of its interface could hold that type. The other
  candidates are `unknown`, never `unreached`.
- Go's local types come from declarations only (parameters, `var`,
  composite literals); a function's result type is not followed.
- Reflection, dependency injection and registration by string are
  invisible. Declare their targets in `coverage.roots` or allowlist them
  with `kind: framework`.
- PHP has no reference extraction yet.

## 4h. The block map

`lattice refresh` builds the block graph from a workspace's sources and
judgments, its feature manifests (nested directories included, as extract
reads them) and its entry points. `lattice serve` shows it at `/map`, and
`lattice export map out.html` writes the same page as one file that works
offline. The page lays out with ELK (elkjs 0.9.3, shipped in the binary):
the export inlines it, and `serve` serves it itself, so neither makes a
network request. If the library cannot start, the page falls back to a
simple grid. The first view follows the shape of the window: when the
layered drawing would be much taller than the pane (many blocks, few links,
so most of them stack in the first column), each connected group keeps its
left-to-right layout, a large group with a cap on the blocks per column, and
the groups are packed in rows to the pane's width and height.

Every feature and invariant is a block. A dotted feature sits inside the
feature its id extends: `orders.refunds` inside `orders`, and
`orders.refunds.partial` inside `orders.refunds`. When no feature declares
the prefix (`reports.daily` with no `reports` feature), a group block
`feat-group:reports` holds it instead. A group judgment can still move any
of them.

`serve` and `export map` build the block graph themselves, as `refresh`
would, before they draw it. When it differs from the stored graph they save
it to the store (`lattice.json`, or the database), the only thing `refresh`
writes, so a workspace that never ran `refresh` still gets a full map.
When it cannot be built (a source clone missing, say), they draw the stored
graph, and the page and the command's output say why and what to fix.
`export map --no-refresh` draws the stored graph as it is.

`refresh` warns about a flow step that names no feature, with the code
`validate` reports for it (`PHANTOM_FLOW`); the two read the same features,
so they flag the same steps.

`examples/block-map/` is a fictional bookshop to try it on:

```sh
lattice --repo examples/block-map refresh
lattice --repo examples/block-map export map /tmp/bookshop-map.html
```

### Kinds and the legend

Each block's kind has a colour and a small icon, by family: system or
container, external, actor, screen or UI, API or route, action or step,
data or setting, and invariant or rule. A kind in no family keeps a colour
made from its name and a plain dot. The tree, the Diagram, Focus, Flows and
Discover all use the same encoding.

The legend under the tabs lists the kinds on screen, each with its count.
Open containers and lens groups are boundaries, so they are left out.
Clicking a kind hides those blocks in every view but the tree; clicking it
again, or "show all", brings them back.

### Diagram and lenses

The Diagram shows nested boxes. Double-click a box to open it, use L1–L3 in
the toolbar to open every box to that depth, or drill in from a block's
details. Edges are lifted to the nearest box on screen and merged, with a
count.

**Group by** changes the lens:

| Lens | Groups blocks by |
|---|---|
| Containment | their parents (the default) |
| System | the nearest `system` or external block above them |
| Owner | `attrs.owner` on the block or the nearest block above it |
| Kind | their kind |
| Tier | `tier` on the block or above it |
| Layer | the runtime layers the kernel computed for the block (see [Layers](#4o-layers-what-kind-of-runtime-is-this-code)) |
| Truth | `declared` or `wired` |
| Proposal verdict | the selected proposal's verdict (only with a proposal) |

Every lens except containment draws one box per group, with its size, kind
mix and links in and out. The edges between groups are merged with counts.
Click a group to list its blocks, and double-click it to open it. An open
group draws its 40 most connected blocks, and the rest share one "+N more"
box.

**Owners** come from `owner:` on a system, external or group judgment
entry, and from a feature manifest's `owners` (engineering, else business).
A block takes the nearest owner above it. Blocks with no owner group as
"unowned". Owned boxes and cards show `@owner`.

```yaml
# lattice/judgments/landscape.yaml
id: landscape
type: system
entries:
  - {id: sys.checkout, title: Checkout, owner: team-checkout}
```

### The assurance lens

**Assurance** in the header (or `a`) colours every block by its
assurance: the class `lattice audit` computed for the promises the block
holds. An invariant block holds its own promise, a feature block its
invariants, and any other block the promises of the blocks inside it.

- A block that holds several promises takes the **weakest** class, in the
  audit's order: fake, unenforced, unverified, not demonstrated, unknown,
  demonstrated only, exercised, break-tested. Its count per class shows on
  the box, in its tooltip and in its details, which also list each
  promise with the audit's reason and its acceptance cases' coverage.
- Each class has one colour. Only **break-tested** (solid green) and
  **exercised** (green outline) are drawn as a pass. **Unknown** is
  hatched grey, and so is a promise block the audit has no row for (a
  stored graph older than the features, drawn with `--no-refresh` or
  when the graph could not be rebuilt: run `lattice refresh`). A block
  that holds no promise is faded.
- While the lens is on, kind and wiring colours on the blocks go grey, so
  every colour on a block is an audit class.
- The legend under the tabs gives each class with its meaning and count,
  the evidence the audit read, and the rule: unknown is never a pass.
- Discover gains an **Assurance** column and chips; it sorts in the
  audit's order. A group made by a lens other than containment is not one
  block, so it is not coloured.

The page computes no class. `/api/graph` (and the data `lattice export
map` inlines) carries an `assurance` object beside the graph: the audit's
classes in order, the style of each class, the legend, every promise with
its class, and for every block its weakest class, count per class and
promises. `serve` audits the live workspace read-only, again as soon as
any input changes (section 4c); `export map` audits it once. When the audit cannot run, every promise reads
unknown and the legend says why. The lens only shows: it confirms and
prepares nothing.

### Focus

Focus puts one block in the centre, the blocks that link to it on the
left, and the blocks it links to on the right.

- **Hops** 1–3 add columns outward, and each column shows up to 40 blocks.
  Every block has one card, at its nearest hop, and the centre never comes
  back as its own neighbour. A block linked both ways at the same hop sits
  in the OUT column, marked "⇄ both ways", with both edges drawn. Its
  further neighbours extend the OUT side only. Arrows point at each edge's
  target.
- **Click** a card to centre it. ◀ ▶, the breadcrumb, `[` `]` and the
  browser's back button walk the history.
- **Double-click** a card, or use **Details**, to open its details.
- **Focus a block…** finds a block by name.
- **System context** lifts both ends of every edge to their nearest system
  or external and merges parallel edges with counts. On a system block that
  gives its context map: the systems, externals and actors around it. A
  block's details have a **System context** button too.

The edge-kind filter (**Edges**) is shared by the Diagram, Focus and
Discover.

### Discover

Discover is a table of every visible block. Its columns are title, kind,
system, tier, truth, owner, links in and out, the flows the block appears
in, and the proposal's verdict when one is selected.

- The text box filters on title, id, kind, system and owner.
- Chips filter per column. Each chip's count is what the other filters
  leave.
- Headers sort, and a row opens Focus on its block. System context turns
  off for this, unless the block is a system or external itself.

Blocks that exist only in a proposal show only while that proposal is
selected. The page header counts them apart ("159 blocks +8 in
proposals"). Discover names them beside its count, and the proposal's name
selects it.

### Flows: swimlanes and the walkthrough

The Flows tab draws the selected flow's steps left to right, grouped by
system, owner or tier. The groups show as full-width lanes or as boxes
around their steps; `g` cycles the grouping.

**Walk it on the diagram** starts the walkthrough. A card shows the step,
its condition, the block, the ways on from it and the steps walked so far.

- `→` and `←` step.
- At a fork, `1`–`9` take another way and leave the recorded walk.
- **Play** steps on its own, and `Esc` stops.

### Canvas tools

The bar at the bottom of the Diagram and Focus has:

- **Fit**, zoom (`+`, `-`) and the zoom level;
- the levels L1–L3;
- a **Minimap** (`m`), which you click or drag to move the view. It shows
  by itself while part of the drawing is off screen. The button shows or
  hides it on demand. While it is on, **Fit** keeps the drawing clear of
  it, either to its left or above it, whichever lets the drawing be larger;
- **Text** (`t`): the view as Mermaid source;
- **Export**: the view as an SVG or PNG image, or a `.mmd` file.

Right-click any block, in any view or the tree, to focus it, see its system
context, open its details, show it on the diagram, or copy its id.

### Links

The URL hash holds the view, so a link reopens it and the back button
walks it:

```
/map#tab=focus&id=co%3Aorders.place&hops=2&ctx=1
/map#tab=diag&lens=owner
/map#tab=discover&init=pay-sheet
```

The keys are `tab` (`diag`, `focus`, `flows`, `discover`), `id` (the
focused or selected block), `hops`, `ctx`, `lens`, `root` (the box drilled
into), `init` (the proposal), `flow`, `lanes`, `hide` (hidden kinds) and
`as` (`as=1`: the assurance lens on).
In `lattice serve`, the pages of a feature, a BRD and an entry point link
to `/map#tab=focus&id=<block>` when the graph has a block for them:
`feat:<id>`, `ep:<id>`, and for a BRD its features' blocks and the blocks
it names.

Keys: `/` search, `a` the assurance lens, `Esc` close, `f` fit; in Focus
also `[` `]` back and forward, `1` `2` `3` hops and `c` system context.

### Checking it in a browser

`scripts/blockmap-smoke.js` drives an exported map in Chromium with
Playwright, with every request that is not `file://` blocked, and fails on
any console error, any network request, or a grid layout. `go test ./internal/cli -run
BlockMapBrowserSmoke` runs it on the demo; the test skips when node or the
`playwright` package is missing. The script can also take screenshots
(`--shots dir`) and time a generated graph (`--perf 2000`).

## 4i. Break testing: does the verifier notice?

A verifier that runs the enforcing code and asserts nothing passes the
coverage checks and still proves nothing. `lattice break` finds it by
breaking the enforcer and running only that promise's verifiers:

```sh
lattice break                          # every promise with an enforcer
lattice break --promise cart:INV-2     # one promise
lattice break --promise cart:INV-2#b   # one acceptance case
lattice break --feature cart --json
lattice break --fail-on survived,unknown   # exit 1 when a shown run survived or is unknown
```

`--promise <promise>#<case>` breaks one acceptance case: the case's own `enforcer`,
or each enforcer of the invariant when it names none, against only the tests tagged
`@verifies <promise>#<case>`. Its runs are written under the case ref (`"promise":
"cart:INV-2#b"` in `breaks.json`) next to the promise's runs, which are kept. They count
for that case's coverage in the audit and never for the promise's break signal, which
still needs a run of all its verifiers. An unknown case is refused (`CASE_NOT_FOUND`).

For each enforcer it makes a temporary detached `git worktree` at HEAD,
runs the verifiers unbroken (the **baseline**), applies the break, runs
them again and removes the worktree. Your working tree is never edited. A
tree with uncommitted changes is refused; `--allow-dirty` copies the changes
into the worktree and records `dirty: true`.

| Outcome | Meaning |
|---|---|
| `killed` | The verifiers passed before the break and failed after it. |
| `survived` | They all ran and passed both times: the test cannot tell the code is broken. |
| `unknown` | Anything else. It is never a pass and never a fail. |

`unknown` covers: no generated break for the language or symbol ("add a
script"), a break that does not compile, a baseline that fails or executes no
test, a timeout (`--timeout`, default 120s, per test run), no runner
configured, and a **partial** run: the verifiers that ran passed after the
break, but some did not run (no runner matches their file, no test was
selected, or the JUnit report does not show them), so the ones that could have
noticed never saw the break. Its reason starts `partial:` and names them
("partial: the 4 verifier(s) that ran passed after the break; 6 verifier(s) did
not run: …"). A group that fails after the break still makes the run `killed`.

Only an explicit build marker reads as "does not compile": Go's `[build
failed]` or `[setup failed]`, an esbuild or transform `file:line:col: ERROR:`
line, Deno's `error: TS...`, `error: Module not found` or `Check file`,
pytest's collection and import errors, and PHP's `PHP Parse error` or `Parse
error: syntax error`. A runtime failure with a stack trace is a failing test,
so it reads `killed` when the baseline passed, and so is a TAP harness's
`Parse errors: Bad plan` (a pgTAP script that stopped partway). The reason
names the failing tests, else the first error line.

**Exit 125: the code could not be set up.** A runner that exits 125 says the
code under test could not be prepared, for example because a database reset
failed when a generated SQL break did not apply. As in `git bisect`, where 125
means "this revision cannot be tested", the run reads `unknown` ("did not
apply (runner exit 125)", quoting the first line that contains `ERROR:` or
`error:`, else the last non-empty line, cut at 120 characters), never `killed`,
whatever else the output says. A setup step that fails must not count as a
verifier noticing the break, so end such a step with `|| exit 125`.

**One break run per repository.** A run that executes runners (`lattice break`,
`lattice break --fails-on-base`) takes an exclusive lock, the file
`lattice/.cache/results/break.lock`, which holds its pid and start time and is
removed when the run ends, including on an error, Ctrl-C or SIGTERM. A second
run fails at once with `BREAK_LOCKED`: "another lattice break is running (pid
N, started <time>); wait for it or remove lattice/.cache/results/break.lock if
it is gone". Runners that share a resource, such as one database, would corrupt
each other's results. If the pid no longer exists on this machine the lock is
reclaimed, with a note on stderr. On Windows the pid is not checked, so a lock
left by a killed run is removed by hand. A run that has nothing to execute takes
no lock.

**The generated break** is an early return at the top of the enforcer's
body, with the rest left in place so imports and variables stay used. Go
returns zero values (`if true { return }` for named or no results);
TypeScript returns `undefined as any` (plain `undefined` in JavaScript),
for function declarations, methods, and functions or arrows with a block
body; Python gets `return None`. An arrow with an expression body,
`(a) => expr`, has its body wrapped as `(true ? (undefined as any) : (expr))`
(no `as any` in JavaScript), so `expr` stays referenced. Constructors, setters,
classes and consts that are not functions ("not a function") read unknown.

**SQL breaks.** A PostgreSQL enforcer (from the `postgres` adapter) is found by
the line its `CREATE` statement starts on and its name, and broken by an
insertion that keeps the statement valid SQL:

| Enforcer | Break |
|---|---|
| `CREATE POLICY … USING (expr) WITH CHECK (expr)` | each clause becomes `(true OR (expr))`, so the policy admits every row |
| `CREATE [CONSTRAINT] TRIGGER …` with no `WHEN` | ` WHEN (false)` before `EXECUTE FUNCTION` (or `PROCEDURE`) |
| a trigger with `WHEN (cond)` | `WHEN (false AND (cond))` |
| a `LANGUAGE plpgsql` function or procedure | an early return right after the top-level `BEGIN` (after any `DECLARE`), inside the dollar-quoted body |

The early return follows the declared result: `RETURNS trigger` gets `RETURN
COALESCE(NEW, OLD);` (the row goes through unchecked; `NEW` is NULL on DELETE
and `OLD` on INSERT); `void`, `SETOF …`, `TABLE (…)`, `OUT` parameters and
procedures get `RETURN;`; `boolean` gets `RETURN true;`, because a guard
function usually answers "is this allowed?" and the break should allow what
it would deny; any other type gets `RETURN NULL;`. Functions in any other
language (`LANGUAGE sql`, `c`, …), event trigger functions, string-quoted
bodies, `INSTEAD OF` triggers (which allow no `WHEN`) and anything the
generator cannot place exactly read unknown with the reason, never a break
that would fail for the wrong reason.

**A scripted break** is a patch at `lattice/breaks/<feature>/<INV>.patch`
(made with `git diff`), applied with `git apply` in the worktree. It
replaces the generated break for that promise, and is the way to break
anything with no generator.

**Runners.** Go verifiers run with `go test -run '^(TestA|TestB)$' -count=1`
and need no setup. Everything else needs a runner in `lattice/config.yaml`.
Runners are per framework, not per language: a promise's verifiers can span
vitest and Deno, both TypeScript.

```yaml
break:
  runners:
    - name: vitest
      match: ["apps/client/**/*.test.ts"]
      command: [npx, vitest, run, --root, apps/client, --reporter=junit,
                "--outputFile.junit={junit}", -t, "{filter}", "{files}"]
    - name: deno
      match: ["supabase/functions/**/*_test.ts"]
      command: [deno, test, --allow-all, --junit-path={junit}, --filter, "/{filter}/", "{files}"]
  commands:                      # fallback for a verifier no runner matches
    python: python -m pytest {files} -k {filter}
```

- `match` are doublestar globs over the verifier files, relative to the directory
  that holds `lattice/`. A promise's verifiers are grouped by the first runner
  that matches and **every group is run**. The promise is `killed` only if at
  least one group fails after the break while all passed at baseline.
- `command` is an argument list, run without a shell. `{files}` as its own
  element becomes one argument per file; `{filter}` (a regular expression over
  the test titles, a `-k` expression for Python) and `{junit}` are replaced
  where they appear; a SQL break also has `{patched_sql}` and `{original_sql}`
  (below). `dir` (optional) is the working directory, and file paths
  are then given relative to it.
- A `break.commands.<lang>` shell template (with `{files}`, `{filter}` and
  `{junit}`, shell-quoted) is the fallback for a verifier no runner matches, as
  before. A verifier that no runner or template matches is recorded as not run.
- `{junit}` is the path of a JUnit report the runner should write. With it,
  `lattice break` counts the tests that executed. A filter that selects none of
  the promise's verifiers reads `unknown` ("the filter matched no tests"), never
  `survived`: vitest's `-t` with no match exits 0. Without `{junit}` the executed
  count cannot be checked, and the reason says so.
- Paths in `{files}` are relative to the directory that holds `lattice/`, but a
  runner may resolve them against its own root. For vitest, pass `--root
  apps/client` rather than `cd apps/client && vitest {files}`, which finds
  nothing; or set `dir`.
- pgTAP verifiers need a runner that rebuilds a scratch database from the
  worktree's migrations, so the broken SQL is what the tests run against. For
  example, with a `db/reset.sh` of your own that drops and re-creates the
  database and applies `db/migrations/*.sql` in order:

  ```yaml
  break:
    runners:
      - name: pgtap
        match: ["db/tests/**/*.sql"]
        command: [sh, -c, '{ db/reset.sh || exit 125; } && pg_prove -d "$PGDATABASE" "$@"', sh, "{files}"]
  ```

  The reset exits 125 when it fails, so a break whose SQL does not apply reads
  `unknown`, not `killed`. pg_prove runs whole files, so `{filter}` does not
  apply. With Supabase, `{ supabase db reset || exit 125; } && supabase test db`
  fits the same way.
- **Applying only the broken object.** The full reset above rebuilds the whole
  database for every break, which is most of a SQL break's time. For a SQL
  break a runner may name two more placeholders instead:
  - `{patched_sql}` is the path of a temporary `.sql` file holding the broken
    object's whole statement, with the break applied;
  - `{original_sql}` is the same statement unbroken.

  A function or procedure is written as `CREATE OR REPLACE` (the keywords are
  added when the migration wrote a plain `CREATE`). A policy cannot be replaced
  in place, so its two files start with `DROP POLICY IF EXISTS <name> ON
  <table>;` before the `CREATE POLICY`; a trigger likewise starts with `DROP
  TRIGGER IF EXISTS <name> ON <table>;`. The files are written outside the
  worktree, and in a `break.commands` template the paths are shell-quoted. A
  runner applies the patched file, runs the tests, and always puts the original
  back. This one needs the database migrated once beforehand, here by a
  `break.setup` step with the `db/reset.sh` of the example above:

  ```yaml
  break:
    setup:
      - db/reset.sh
    runners:
      - name: pgtap-swap
        match: ["db/tests/**/*.sql"]
        command:
          - sh
          - -c
          - |
            psql -v ON_ERROR_STOP=1 -q -f "$1" || exit 125
            original=$2
            shift 2
            pg_prove -d "$PGDATABASE" "$@"
            status=$?
            psql -v ON_ERROR_STOP=1 -q -f "$original" || exit 125
            exit $status
          - sh
          - "{patched_sql}"
          - "{original_sql}"
          - "{files}"
  ```

  The baseline has no broken object, so a baseline command that names the
  placeholders gets `{original_sql}` for both. The temporary paths are not part
  of the baseline's cache key, so promises that run the same command still share
  one baseline. A command that names either placeholder for a break that is not
  SQL, for a SQL break with a patch script (`lattice/breaks/...`), or for a
  statement that cannot be isolated does not run: the break reads `unknown`
  ("{patched_sql} not available for this break"), never `survived`.

  The full reset stays the safe default. Swapping one object is not safe when
  other objects depend on its definition in a way that replacing it does not
  update (an index or generated column built on a function, a view that was
  materialised with its result), or when the tests depend on data that a
  migration seeded after the object, by calling the unbroken object. Use the
  reset for those.

**The worktree.** A temporary worktree has no `node_modules`, so `lattice break`
symlinks each `node_modules` directory of the main checkout into the same relative
path of the worktree (set `break.link_node_modules: false` to opt out). `break.setup`
lists shell commands run once in the worktree before the first baseline, with
`{repo}` set to the main checkout's path:

```yaml
break:
  setup:
    - ln -s {repo}/.env.test .env.test
```

A setup command that fails makes every run `unknown` ("setup failed").
`lattice results tests --verifiers` shows the filter each runner takes.

Each run in `breaks.json` records `ran_verifiers` (from the JUnit report, or Go's
`-v` pass lines), `not_run_verifiers` (no runner, the filter or the runner's
config left them out, or they did not run after the break) and `failed_tests`
(the tests that failed after the break).

**Cache.** An earlier killed or survived outcome is reused when the
enforcer's source, the verifiers' source, the method, the script and the
runner configuration (the resolved runner commands, the setup commands and the
generator version) all match, so a config change runs again; `unknown` is never
reused, so a timeout is always tried again.

A baseline runs once per distinct command: promises whose verifiers render the
same invocation (a runner that takes whole `{files}`, such as `pg_prove`, given
two tests of one file) share it, and each promise still reads its own verifiers
from its output. A passing baseline is also kept for the HEAD tree in
`lattice/.cache/results/break-baselines.json` and reused by the next `lattice
break` at that tree; a new commit, a dirty tree, a failure, a timeout or an
error runs it again. The summary line says `baselines: 12 run, 40 reused`, and
`--json` has `summary.baselines` (`run`, `reused`).

`--promise` and `--feature` print only the runs they selected; `breaks.json`
still keeps the rest of the commit's runs.

**Exit code.** Once the report is written, `lattice break` exits 0 whatever the
outcomes. `--fail-on survived`, `--fail-on unknown` or both (repeat the flag, or
a comma list) exit 1 when a shown run has a listed outcome, with code
`BREAK_OUTCOME` and the counts (`3 survived, 648 unknown; --fail-on
survived,unknown`). Shown means the `--promise` or `--feature` selection, so a
survived run of another promise in `breaks.json` does not count. Any other value
is a usage error (exit 2). With `--json` the output is one document: the report,
a `summary` of the shown runs (`runs`, `killed`, `survived`, `unknown`, `ran`,
the top `unknown_reasons` and `missing_runners`, and the invocation's
`baselines`), and `error` and `code` when
`--fail-on` matched. Runs that lack a runner count under one reason, `no runner
configured`, whatever languages each lacks, and partial runs under `partial:
some verifiers did not run`; `missing_runners` counts, per
language, the runs that need its runner (a run that needs two counts under
both), so each runner to configure is named once.

When no shown run was killed or survived, nothing was learned about the
verifiers, and the text output ends with a line that says so, names the three
most common reasons and lists the missing runners:

```text
no break ran: 651 of 651 runs unknown (no runner configured ×412, no generated break for php; add a script ×228, …); missing runners: typescript ×412, python ×68 (set break.runners or break.commands); break_tested stays unknown
```

`lattice check --base` stays the merge gate: there an unknown `break_tested` is
a gap, with or without `--fail-on`.

The result is `lattice/.cache/results/breaks.json` (`lattice.breaks/1`,
derived and gitignored). A promise with several enforcers has one run each.
Nothing blocks on it yet: it is evidence for `lattice audit`.

### Preflight: refuse in seconds, before a long run

`lattice break`, `lattice runs-clean` (`v0`) and `lattice demonstrate` (when V0
runs) first check, in seconds and without running a test, what their config
declares, and refuse when a check fails. `lattice check` runs the checks of
`break.runners` when it is declared, and only notes a failure (see below).
Nothing is guessed from the stack: a command the config does not name is not
checked.

| Check | `break` | `runs-clean`, `demonstrate` | `check` |
|---|---|---|---|
| executables on PATH (`EXEC_NOT_FOUND`) | `break.setup`, `break.runners`, `break.commands` | `runtime.clean_install`, `build`, `boot` | `break.runners` |
| a runner's `dir` exists (`PATH_NOT_FOUND`) | yes | | yes |
| Node meets `package.json` `engines.node` (`VERSION_TOO_OLD`) | for `node`, `npx`, `npm`, `pnpm`, `yarn` | the same | the same |
| the Docker daemon answers (`DOCKER_UNAVAILABLE`) | for a command that runs `docker` or `docker-compose` | the same | the same |
| each service answers on TCP (`SERVICE_UNREACHABLE`, `SERVICE_BAD_ADDRESS`) | `break.services` | `runtime.services` | |
| a clean tree (`DIRTY_TREE`) | unless `--allow-dirty` | | |

An executable is read from each simple command of a shell line (`FOO=1 npm ci
&& cd web && pnpm build` checks `npm` and `pnpm`); builtins, variables and
placeholders are not looked up, and a relative path such as
`node_modules/.bin/vitest` is checked only when no earlier step could have made
it. Only a Node minimum that reads for certain counts (`>=18`, `^20.11`, `18.x`);
a range with `||` is not checked. Each service dial times out after 2 seconds,
and the dials run together.

Services are declared, never started:

```yaml
runtime:
  services:                      # must answer before install, build and boot
    - { name: postgres, address: "localhost:5432" }
break:
  services:                      # must answer before the suites run
    - { name: postgres, address: "localhost:54322" }
```

In `break`, `runs-clean` and `demonstrate`, a failure refuses the command
before any step runs, lists every problem at once, and exits 2, the
setup-error code, never a verdict code:

```text
error: break preflight failed: 2 problems found; nothing was run
  EXEC_NOT_FOUND       break.runners[0].command: vitest is not on PATH
                       next: install vitest or put it on PATH, or change break.runners[0].command
  SERVICE_UNREACHABLE  break.services[0].address: postgres (localhost:54322) does not answer on TCP: ...
                       next: start postgres (localhost:54322) before running this command, or fix break.services[0].address
fix each problem, or pass --no-preflight to skip these checks
```

With `--json` it is one error, `{"error", "code": "PREFLIGHT_FAILED",
"next_action", "problems": [{"code", "key", "detail", "next"}]}`.

`lattice check` runs no test: it only lists the verifiers to run by
`break.runners`, and a verdict-only CI job may not have the runners'
toolchain. So it never refuses on a preflight problem. The report carries
the problems as an informational `preflight` field, `{"text", "problems":
[{"code", "key", "detail", "next"}]}` (absent when every check passes), and
shows them under `PREFLIGHT` in text and `### Preflight` in Markdown. The
verdict, `merge_allowed` and the exit code are those of the same check
without it. `GET /api/v2/check` carries the same field.

`--no-preflight` skips the checks, and check's note with them. `lattice doctor`
runs the same checks (all but the dirty tree) under the `preflight` category,
and a failed one makes its `ok` false.

### Prove a new test fails on the old code

For a fix, the proof that matters is that the new test fails on the code
before the fix. `lattice break` does not show that: it breaks the enforcer
crudely (an early return), not the way the old code was wrong.
`--fails-on-base` does it without stashing and rebuilding by hand:

```sh
lattice break --fails-on-base origin/main
```

It takes the verifiers the change from `merge-base(origin/main, HEAD)` to the
working tree adds or edits (as `lattice impact` finds them), runs each one at
head, then runs it again on the base commit's production code with the head's
test files copied on top. Both runs happen in temporary worktrees, with the same
`break.runners`, `break.commands`, `break.setup` and Go's built-in `go test -run`
as a break. A non-Go failure on base counts only when the test itself failed:
output that says an import did not resolve (`Cannot find module`,
`does not provide an export named`, `ERR_MODULE_NOT_FOUND`,
`Failed to resolve import`, `Module not found`, `has no exported member`,
`TS2305`/`TS2307`/`TS2724`, `ImportError`, `ModuleNotFoundError`,
`cannot import name`, pytest's `ERROR collecting`) reads `unknown`. An assertion
or a `TypeError` in the test body is a real failure. Each verifier gets one
outcome:

| Outcome | Meaning |
|---|---|
| `fails_on_base` | it passes at head and fails on the old code: it detects the old behaviour |
| `passes_on_base` | it passes on the old code too: it does not tell the old behaviour from the new |
| `unknown` | it fails at head, does not compile on base (it calls an API the change adds), does not load on base (a module or export it imports does not resolve there), has no runner, timed out or did not run; the reason says which. Unknown is never a pass |

The files copied onto the base are the verifier files, the changed test files in
their directories and the changed files under their `testdata/`; `overlay` in the
result lists them. Production code is never copied. Test helpers in other
directories (a shared test-utility package, a parent `conftest.py`, a test setup
file) and changed dependencies are not copied, so a test that needs them reads
`unknown` or runs against the base versions.

The result is `lattice/.cache/results/fails-on-base.json`
(`lattice.fails_on_base/1`), stamped with `base`, `head`, `ci_run` and `dirty`
like `breaks.json`. `--json` prints it. `--promise`, `--feature`, `--timeout` and
`--allow-dirty` work as for a break: a dirty tree is refused unless allowed, and
`dirty: true` is recorded. `--fail-on unknown` exits 1 when a shown verifier reads
`unknown`; `--fail-on survived` is refused there (exit 2), since this run has no
survived outcome and `passes_on_base` is not one. `lattice check --base` reads the file when its base
and head match the change, and adds a `PASSES_ON_BASE` note to each touched
promise of a verifier that passes on base. The note is information only: it
never changes the verdict, a gap or `merge_allowed`.

## 4j. The audit: is each promise kept, or does it only look done?

`lattice audit` gives every invariant one class. It computes nothing new about
your tests: it joins what the kernel already has, so it agrees with `lattice
rtm`, `lattice results` and `lattice validate`.

```sh
lattice extract
lattice results ingest junit.xml all.lcov --per-test coverage/per-test
lattice audit                 # summary by class, then one row per invariant
lattice audit --json          # lattice.audit/1
lattice audit --markdown      # for a PR comment or a report
lattice audit --class fake --feature wallet
lattice audit --promise wallet:INV-1          # one promise (repeat, or a comma list)
```

`--promise`, `--feature` and `--class` only narrow what is printed; the audit
is always computed over the whole workspace, so a row reads the same filtered
or not. An unknown `--promise` id is a usage error (exit 2).

`inputs.results.matches_head` and `inputs.coverage.matches_head` say whether
the evidence is of the graph's commit, by the rule validate uses for
`RESULTS_STALE`: commits that differ only in Lattice's ledger and cache count
as the same (for coverage, only when it was ingested from a clean tree).

The same rows are on the `/audit` page of `lattice serve`, filterable by class
and feature (and `?promise=`); each fake opens to its verifiers, enforcers, the
evidence behind the class and any reader's note. `GET /api/v1/audit` returns
the JSON, with the same filters; an unknown `promise` is a 400.

### Classes, in precedence order

| Class | Means |
|---|---|
| `fake` / `never_runs_enforcer` | per-test coverage exists for every verifier and none ran an enforcer |
| `fake` / `passes_when_broken` | a break of an enforcer left the verifiers green, and every verifier of the promise ran |
| `break_tested` | breaking the enforcer made the verifiers fail (positive) |
| `exercised` | a verifier passed and its own run executed an enforcer (positive, weaker) |
| `demonstrated_only` | a verifier passed; nothing shows whether it runs an enforcer (`no_tool`, `partial_coverage`) |
| `unenforced`, `unverified`, `not_demonstrated` | a gap |
| `unknown` / `partial_run` | a break survived, but a verifier could not run (no runner, or a journey the runner excludes), so it says nothing about that verifier |
| `unknown` | nothing to judge, for example an invariant verified by a structural check only |

Unknown is never a pass and never fake: with no per-test coverage and no break
run, a promise reads `demonstrated_only` with kind `no_tool`, not `fake`. Only
`break_tested` and `exercised` are drawn positive.

### Signals

Each row carries eight signals, each with an honesty label: `declared`,
`enforced`, `verified`, `demonstrated`, `exercised`, `live` (reach class of the
enforcers; an upper bound), `break_tested` and `confirmed`. `exercised` says the
code ran, not that the test checks it, so it is a proxy. `confirmed` is the
platform's record of who confirmed the BRD criteria that map to the invariant,
read from `lattice/.cache/confirm.json` (section 4l) and from nothing else:
`human` (a verified platform approval of this version), `pending` (prepared and
not merged or not verified, or made by a local session), `agent` (a council or
an agent approved it and no human decision confirmed it), `draft`, `drifted`,
`unknown` (an approval is claimed and no confirm data backs it) or `none`. A
BRD's `approved_by` is display only: a hand-written `approval:` block reads
`unknown`, never `human`. It is a fact about who confirmed which version, not
proof the intent is right.

### Break results

`breaks.json` (`lattice/.cache/results/`, written by `lattice break`) is read
as a file. A run for another commit, or of a schema the audit does not know, is
set aside and listed under `inputs.breaks.ignored`. When an invariant has
several enforcers, a survived break on any of them reads `passes_when_broken`.
A partial run (`unknown` with a `partial:` reason, or, in an older
`breaks.json`, `survived` with entries in `not_run_verifiers`) reads `unknown` /
`partial_run`, with a reason naming the verifiers that did not run. `break_tested` needs at least one enforcer killed and none survived
with all verifiers run. A row's evidence lists the outcome of each enforcer, with
its unrun verifiers and failed tests. A `breaks.json` from before these fields
reads as it always did.

Each row also carries `enforcers`: every enforcer of the promise as
`{symbol, file, line, outcome, reason}`, with `outcome` one of `killed`,
`survived` (its break left every verifier green), `unknown` (no runner, a red
baseline, or a partial run) or `not_run` (no break
run of it). It says *which* enforcer a class is about and never changes the
class: one survived enforcer still makes the promise `fake` /
`passes_when_broken`, even when the others were killed. When a promise has
several enforcers, the reason of a row decided by breaks counts them and names
the survivors, for example `1 of 3 enforcers survived: wallet.LimitPanel; 2
killed: wallet.Cap, wallet.Check`. The text and Markdown output and the
`/audit` page print one line per enforcer for fakes, partial runs, and rows
whose enforcers' breaks disagree.

### Acceptance cases

A long invariant pulls in many tests, and when one fails the audit names a paragraph,
not a claim. An invariant may list its claims as `cases`, one claim each:

```yaml
invariants:
  - id: INV-1
    statement: A fee never exceeds five percent of the amount.
    cases:
      - id: a
        kind: happy
        statement: A 100.00 withdrawal pays at most 5.00.
      - id: b
        kind: edge
        statement: A 0.01 withdrawal pays no fee.
      - id: c
        kind: error
        statement: A negative amount is rejected first.
        enforcer: wallet.fees.validateAmount
```

- **Ids** are lowercase letters (`a` … `z`, then `aa`, `ab`, …), and they are never reused:
  a case that no longer applies gets `retired: true` and keeps its id. A case is displayed
  as `1a` (case `a` of `INV-1`).
- **`kind`** is `happy`, `edge`, `error` or `security`.
- **`enforcer`** (optional) names the symbol that enforces this case when it is narrower
  than the invariant's enforcers. It must be a symbol tagged `@enforces` for the invariant.
- **A test verifies one case** with `@verifies wallet.fees:INV-1#a` (or `INV-1#a` inside
  the feature), in every language. It still counts as a verifier of `INV-1` for every
  other signal, so adding cases changes nothing that worked before.

The audit computes each case's coverage; it is never typed:

| Coverage | Means |
|---|---|
| `covered` | a test tagged with the case passed on the ingested results, and breaking the enforcer (the case's own, else the invariant's) made that test fail, by `failed_tests` in `breaks.json` |
| `partial` | a tagged test passed, but no break run of the enforcer exists, or the break left it green, or failed only other tests |
| `failing` | a tagged test's ingested result is red |
| `uncovered` | no test is tagged with the case, or no tagged test has a result |

Only `covered` is positive. The cases are on the audit row (`cases`, in
`lattice.audit/1`), under each invariant in `lattice feature show` and under each
criterion in `lattice rtm`. They never change the row's class.

`lattice validate` checks them: a bad or repeated id is `CASE_ID_FORMAT` or
`CASE_ID_DUPLICATE`, an unknown kind or an empty statement `CASE_SCHEMA`, a `@verifies` ref
to a case the invariant does not declare `ORPHAN_ANNOTATION_CASE` (all errors); a ref to a
retired case is `CASE_RETIRED_VERIFIED` (warning); a case `enforcer` that is not a symbol
enforcing the invariant is `CASE_ENFORCER_UNKNOWN` (error). `INVARIANT_BROAD` (info) names
an invariant with no cases whose statement makes several claims: 60 words or more, in two
sentences that each have a verb, or in three sentences or semicolon clauses that each have
one; or 70 words or more however it is punctuated, since one sentence that strings many
conditions together with commas is the hardest for a failing test to point into. A shorter
statement, and a rule with its exception (`X; Y`), stays quiet. Its next action is to add cases; split the
invariant only when its claims are unrelated.

Cases are part of what a human confirms. They are hashed into the review packet with the
promise, so a case added, edited or retired after confirmation makes the decision stale,
and the promise reads unconfirmed until it is confirmed again. A promise with no cases
hashes as it did before. An agent may draft cases (`lattice agent draft-proposal`); the
schema has no field that records a confirmation or a coverage, so nothing an agent writes
can mark a case confirmed or covered.

Each case also has its own confirmation state. A decision that `lattice prepare` writes
records the cases of each of its promises (`cases:` in the decision file: id, kind,
statement, enforcer, retired, and a hash of kind, statement and enforcer), and `lattice
confirm` carries them into `confirm.json`. The list is bound to what was confirmed: the
packet hash already covers every case, so while a decision's packet still matches,
`lattice confirm` requires the list to equal the cases now and records `cases_check:
verified` (or `mismatch`); an intact earlier `confirm.json` that verified the same list
carries the check once the packet moves on. The decision format and the packet hash are
unchanged, so no confirmed decision goes stale. The audit compares each case with the one the
confirming decision recorded (the current version's confirmed decision, else its stale one,
else the latest earlier version's):

| Confirmation | Code | Means |
|---|---|---|
| `confirmed` | `CASE_CONFIRMED` | the case is as a current, confirmed decision for the BRD's current version recorded it |
| `unconfirmed` | `CASE_DECISION_STALE` | the case is unchanged since the last confirmation, but that decision is stale (its packet changed) or for an earlier version: the promise needs confirming again |
| `unconfirmed` | `CASE_ADDED_AFTER_CONFIRMATION` | the decision did not hold the case |
| `unconfirmed` | `CASE_EDITED_AFTER_CONFIRMATION` | its kind, statement or enforcer changed, or it was restored; the reason names what, e.g. `kind edge->error` |
| `unconfirmed` | `CASE_RETIRED_AFTER_CONFIRMATION` | it was retired since |
| `unconfirmed` | `CASE_STAMP_MISMATCH` | the decision's `cases:` list does not match the cases its packet hashed (it was edited after the packet was pinned) |
| `unconfirmed` | `CASE_STAMP_UNVERIFIED` | the list was not checked against its packet; `lattice confirm` checks it |
| `unconfirmed` | `CASE_NOT_RECORDED` | the decision was prepared before decisions recorded their cases; prepare and confirm again |
| `unconfirmed` | `CASE_NEVER_CONFIRMED` | no human confirmation covers the promise |

The state is on each case of the audit row (`confirmation: {state, code, reason}`), under
each case in `lattice feature show`, and in `/api/v2` `case_rows` (`confirmation`: `pass`
only for a confirmed case, `drifted` for an edited or retired one or one under a stale
decision, `fail` for a stamp mismatch, `unknown` otherwise).
The `/audit` page of `lattice serve` lists each row's cases with their kind, coverage and
confirmation; only `covered` and `confirmed` are drawn in the positive colour. It
never changes the promise's `confirmed` signal: any case change still makes the decision
stale, as above. Only a current decision confirms a case; under a stale one, the cases
that changed name their change and the others read `CASE_DECISION_STALE`.

### Second readers

`lattice/audit-notes.yaml` lists `{promise, reader, verdict, note}` entries,
with `verdict` one of `confirmed_fake` or `not_fake`. The note is shown on the
row. It never changes the class. Nothing in the audit blocks a merge.

## 4k. The check: one verdict for a change

`lattice check --base <rev>` judges one change, from `merge-base(<rev>, HEAD)`
to the working tree, and prints one page. It finds the touched promises and
lines with `lattice impact`, reads their signals from `lattice audit` at head,
and compares them with the audit of the base. It adds no measurement of its own.

```sh
lattice check --base origin/main              # text
lattice check --base origin/main --json       # lattice.check/1
lattice check --base origin/main --markdown   # PR comment, starts with <!-- lattice-check -->
lattice check --base origin/main --ci         # count only CI-stamped evidence
lattice check --snapshot                      # save the audit of HEAD, for a base-branch job
```

### The verdict

| Verdict | Means | Merge |
|---|---|---|
| `BROKEN` | Something fell: a promise's class dropped to a known-bad class (`fake`, `unenforced`, `unverified`, `not_demonstrated`) against base; an enforcer or verifier was removed (an enforcer moved within the change reads `ENFORCER_MOVED` instead, see below); a break killed at base survives at head; the suite is red; or the statement, tier or mapping of a confirmed promise was edited without its re-confirmation (see below) | Never |
| `UNPROVEN` | Nothing fell, but a touched promise is unconfirmed, not exercised, not break-tested where its tier needs it, or unknown, or touched lines are untraced, or touched production lines have no promise guarding them (`UNGUARDED_LINES`). It is never drawn as a pass: no check mark, and the comment says so | Only if the merge rule holds |
| `ASSURED` | Every touched production line is guarded by a promise (or none is touched), every touched promise has the signals its tier needs, and nothing fell. `NOTHING_TOUCHED` means the change touches no promise and no production line at all | Yes |

**Editing a confirmed promise.** The edit voids the confirmation. To keep the change mergeable, put the re-confirmation in the same pull request: run `lattice prepare brd approve <id> --version <N>` (or `prepare promise confirm`) on the pull request's branch, then `git merge --ff-only` the decision branch it prints, so its decision file rides with the edit. That makes the promise's `confirmed` gap read *pending*. A pending confirmation does not block the merge, tier 3 included, because the merge needs the human approval that confirms it; `lattice confirm` records it once the pull request merges. Without the decision file the edit reads `BROKEN` (`CONFIRMATION_VOIDED`).

**An enforcer moved.** A refactor that moves enforcement, such as a helper moved
to a shared package or renamed, removes a base enforcer. That is not a fall when
the same change adds an enforcer of the same promise that did not enforce it at
base: the promise's row carries the note `ENFORCER_MOVED` ("fees.round no
longer enforces fees:INV-1; money.Round now does") instead of the fall
`ENFORCER_REMOVED`. A move must never weaken enforcement silently, since any
trivial function can carry an `@enforces` tag, so it opens a `break_tested` gap,
`MOVE_NOT_BREAK_TESTED`, at every tier. The gap is new whatever the base read and blocks the merge until the break
results counted at head show a killed break of each added enforcer; run `lattice
break` and re-run the check, no human step is needed. A kill of an enforcer the
change kept never counts, a waiver never removes the gap, and stale, non-CI or
missing break results leave it open. Several removed enforcers may move onto one
added enforcer. A removal with only kept enforcers left, and any removal on a
tier-3 promise, still reads `BROKEN`. A move edits no statement, tier or mapping,
so a confirmed promise stays confirmed. In `lattice.check/1` the row's `touched`
reads `enforcer_moved` and its `needs` include `break_tested`. `lattice impact --json` lists what the check reads as `added_enforcement:
[{symbol, file, invariant, change}]`: every enforcement pair the base did not hold,
new symbols in new files included.

**An enforcer changed and its statement did not.** When the change edits the
lines of an `@enforces` symbol and leaves the promise's statement and tier as they
were, the promise's row carries an info note, `STATEMENT_UNCHANGED`: "the enforcer
of fees:INV-1 changed and its statement did not: re-read the statement and confirm
it still holds", with a `next:`. The written promise may no longer describe the
code; a reviewer reads it again. The note is information only: it never changes the
verdict, a gap or `merge_allowed`. A promise the change adds, an edited statement or
tier (other rules read those), and a change to verifiers only get no note. Text and
Markdown show the notes in one list; `lattice.check/1` carries them per touched
promise as `notes: [{code, text, next}]` (`[]` when there are none), and `lattice
impact --json` lists them at top level as `notes: [{code, promise, statement,
enforcers, text, next}]`.

Tier defaults until the owner decides: tier 1 needs `exercised` or
`break_tested`; tier 2 and 3 need `break_tested`; every tier needs the promise
traced, confirmed and live. A missing signal is a gap, never a pass.

**The merge rule** (`merge_allowed`): an `UNPROVEN` change may merge when no
(promise, signal) pair has a gap at head that it did not have at base, the
count of new untraced lines did not rise, and no tier-3 promise is involved.
A promise the change adds is exempt for `confirmed` only: a new promise cannot
be confirmed before it merges. A waiver in `lattice/waivers.yaml` is shown on
the gap and never removes it, including one added in the same change.

### Traced, and the blind spot

A touched line counts as traced only when an enforcer span covers it (the
symbol carries `@enforces`), a checked reference names it (a direct-wire
criterion's `enforced_by`), or an entry point reaches it. A
file-level `@module-feature` tag alone never counts. The rest goes in the blind
spot, with the promises a human marked out of reach, and keeps the verdict at
`UNPROVEN`. Lines are counted by the touched symbol's extent, an upper bound.

A line reads "an entry point reaches it" only when the call graph reaches its
symbol from the `handler` of a declared entry point; a handler that names no
symbol of the graph reaches nothing. A line whose feature is on an entry
point's `flow`, and that some root reaches but no handler does, is still traced
and says so instead ("its feature is on an entry point's flow, but no entry
point's handler reaches it"). When the graph cannot decide (an ambiguous call,
a language with no reference data) the reason says it is unknown. Each line in
`blind_spot` carries `handler_reach` (`reached`, `unreached` or `unknown`).

Traced is not guarded. A line is **guarded** only when a promise covers it: an
enforcer span or a checked reference, the lines `lattice impact` attributes to a
promise. A touched line that an entry point reaches, but that no promise guards,
is traced and **unguarded**; so is a removed symbol that enforced nothing. Such a
change reads `UNPROVEN` (`UNGUARDED_LINES`, "touches N production lines that no
promise guards"), never `ASSURED`, because nothing checks those lines. Unguarded
lines are reported, not blocking: they never change `merge_allowed`, and the PR
comment says so, so an allowed `UNPROVEN` is not a refusal. To make such a
change read `ASSURED`, anchor a promise on the code (`@enforces`) and verify it.

`lattice.check/1` carries the counts in `blind_spot`: `unguarded_lines` (every
touched production line no promise guards, untraced ones included),
`guarded_lines`, and `unguarded`, the per-symbol list of unguarded lines that are
not already in `untraced` (reached only by an entry point, or removed).

`lattice.check/1` also carries `tests`: the verifier tests of the touched
promises grouped by `break.runners`, the listing of `lattice impact --tests`
(see [Run only the tests a change touches](#run-only-the-tests-a-change-touches)).
It is for a script and never changes the verdict. When `break.runners` is
declared and a runner may not run here, the report also carries a
[`preflight`](#preflight-refuse-in-seconds-before-a-long-run) note, which
never changes the verdict either.

### The base audit, and how CI gets it

The base's audit is `lattice/.cache/audit/<merge-base sha>.json`. It is derived
and never committed. `lattice check` (and `lattice check --snapshot`) writes the
audit of `HEAD` there when the tree is clean. In CI:

1. A job on the base branch runs the suite, `lattice results ingest`,
   `lattice break` and `lattice check --snapshot`, then saves
   `lattice/.cache/audit/` to a cache or artifact keyed by the commit sha.
2. The pull-request job restores the entry for the merge base (for GitHub
   Actions, `actions/cache` with `key: lattice-audit-${{ github.event.pull_request.base.sha }}`),
   runs the same evidence steps at head, then `lattice check --base
   origin/${{ github.base_ref }} --ci --markdown`, and posts the comment.

With no snapshot, only structural signals are compared with the base (removed
enforcement, and edits to confirmed promises). Evidence comparisons read
`unknown` ("no base audit"). The verdict is `UNPROVEN`, never `BROKEN`, because
of missing data, and the merge rule is not met.

### CI provenance

`lattice results ingest`, coverage ingest and `lattice break` stamp their files
with the head commit and a `ci_run` (from `GITHUB_RUN_ID`, `CI_PIPELINE_ID` and
similar, else `local`).

- In every run, local included, evidence whose commit is not `HEAD` (or that
  carries no commit) is set aside and reads `unknown` (`EVIDENCE_STALE`, "results
  are for <sha>, not HEAD <sha>: re-run the tests and ingest"). Old passes never
  count, so stale evidence can never read `ASSURED`; and an old red result is not
  a fall either: stale evidence is a gap (`UNPROVEN`), never `BROKEN`. After a
  failed `results ingest`, the previous file is still on disk; this rule is what
  keeps it out of the verdict.
- With `--ci`, evidence must also come from a CI run: a file whose `ci_run` is
  `local`, or whose commit is not `HEAD`, reads `unknown` (`EVIDENCE_NOT_CI`).
- The stamp is a commit, not a tree. Evidence ingested at `HEAD` still counts
  while the working tree has uncommitted changes, because the stamp cannot tell
  whether the run saw those edits. Re-run and ingest before you trust a local
  verdict on a dirty tree.
- The base audit snapshot (`lattice check --snapshot`, and the snapshot a check
  of a clean tree saves) keeps its own rule: only `--ci` sets evidence aside
  there.

Only CI's verdict counts; a local run is advice.

### Exit codes

`0` for `ASSURED` or an allowed `UNPROVEN`; `1` for `BROKEN` or a disallowed
`UNPROVEN`; `2` for a usage error (no `--base`, a base that is not a commit).
A [preflight](#preflight-refuse-in-seconds-before-a-long-run) problem of
`break.runners` is a note in the report and never changes the exit code.

## 4l. Confirmation: `lattice prepare`, `lattice confirm` and `lattice sign`

Only a human confirms, and something outside the repository vouches for who
that human is. A `--by <email>` value is a typed string, so it proves nothing.
The record is the platform's approving review on a merged pull request, or a
hardware-key signature over the decision (see "Confirm by signature" below).

### Prepare a decision

```sh
lattice prepare brd approve brd.refunds --version 3
lattice prepare brd return brd.refunds --reason "C-2 is not what we agreed" --row refunds:INV-2
lattice prepare brd ack brd.refunds                       # a council's approval
lattice prepare brd overturn brd.refunds --reason "..."
lattice prepare promise confirm refunds:INV-1 refunds:INV-3   # one BRD per batch
lattice prepare waiver add --code NO_BREAK_RUN --subject refunds:INV-1 \
    --kind no_tool --reason "no mutation tool for PL/pgSQL"
```

`prepare` checks the action against the kernel, then writes a branch
`decision/<id>-v<N>-<packet8>` holding one file,
`lattice/decisions/approvals/<id>-v<N>-<packet8>.yaml`, where `<packet8>` is the
first 8 hex digits of the decision's packet hash (`brd ack`, `return`, `overturn`
and `promise confirm` add a suffix before it, such as `.ack` or `.confirm-<refs>`,
so several decisions on one version never share a file; a waiver also writes the
protected `lattice/waivers.yaml`). The branch is built from HEAD with git plumbing,
so your checkout, index and working tree are never touched. It never pushes and
never opens a pull request. It prints `lattice.prepare/1`:

```json
{ "contract": "lattice.prepare/1", "commit": "cad25d1", "branch": "decision/brd.refunds-v3-9e1f04ab",
  "base": "main", "files": ["lattice/decisions/approvals/brd.refunds-v3-9e1f04ab.yaml"],
  "packet_hash": "sha256:9e1f04ab...", "command": "gh pr create --head decision/brd.refunds-v3-9e1f04ab --base main ...",
  "note": "Push the branch first ..." }
```

Because the name comes from the packet, a decision file is never overwritten:

- Preparing the same packet again reports the branch it already wrote and exits 0
  (the `note` starts with "Already prepared").
- After the BRD or a promise changes, the packet differs, so `prepare` writes a new
  branch and a new file. The earlier decision files stay in the tree as they were.
  This is how you re-confirm a promise after `CONFIRMATION_VOIDED`.
- A local branch of the same name that does not hold this decision is refused
  (`BRANCH_EXISTS`). A decision that is already on HEAD is refused
  (`DECISION_EXISTS`); `lattice confirm` reads its verdict once it merges.

Decision files written before v0.15.1 are named `<id>-v<N>[.<action>].yaml`.
`lattice confirm` and `lattice check` still read them: they go by the file's
content, not its name.

When the kernel says no, it prints `{refused, params, text, next}` and exits 1:

```json
{ "contract": "lattice.prepare/1", "refused": "REGULATED_MACHINE_WRITTEN",
  "params": {"constraint": "C-1", "kind": "legal", "author": "agent:impl-2"},
  "text": "constraint C-1 (legal) was written by agent:impl-2",
  "next": "rewrite it yourself, then re-open this review" }
```

Refusal codes: `BRD_NOT_FOUND`, `VERSION_REQUIRED`, `VERSION_MISMATCH`,
`BRD_SUPERSEDED`, `REGULATED_MACHINE_WRITTEN`, `AGENT_PREPARER`, `REASON_REQUIRED`,
`PROMISE_NOT_FOUND`, `PROMISE_NO_BRD`, `REFS_REQUIRED`, `BATCH_MULTI_BRD`,
`NO_ENFORCER`, `NO_COUNCIL_APPROVAL`, `VERSION_MOVED`, `VOTES_INCOMPLETE`,
`COUNCIL_TOUCHED`, `COUNCIL_HUMAN_ONLY`, `COUNCIL_OVERTURNED`, `COUNCIL_INVALID`,
`MANDATE_NOT_PINNED`, `UNKNOWN_WAIVER_KIND`, `ADR_REQUIRED`, `ADR_NOT_FOUND`,
`WAIVER_TIER3`, `WAIVER_DUPLICATE`, `SUBJECT_NOT_FOUND`, `DIRTY_LATTICE`,
`BRANCH_EXISTS`, `DECISION_EXISTS`, `BAD_ID`, `NOT_A_REPO`. Waiver kinds are closed: `no_tool`,
`generated_code`, `out_of_reach` and `accepted_risk` (the last two need an ADR). A
waiver is shown and never counted as proof.

The decision file records `{action, id, version, packet_hash, rows, cases, reason,
prepared_by, prepared_at}`. `prepared_by` is your git identity, for display only;
`cases` lists each row's acceptance cases (see "Acceptance cases").
The `packet_hash` covers the BRD version's content (not its status, its approval
block or a source's checked mark), each promise's structural signals (text, tier, enforcers, verifiers, cases) and
the structural diff since the last confirmed version. Run-dependent signals are
left out, so a better test run never voids a confirmation.

The review packet's structural diff (`/api/v2/brds/{id}`, `packet.diff.structural`) also
names each acceptance case changed since the last confirmation that recorded it:
`case 1b of wallet.fees:INV-1 edited: kind edge->error`, `case 1c of … added`, `… retired`,
`… removed`, or `cases of … not recorded at the last confirmation` (a list that does not
match its packet, or was never checked against it, is named too, so it never makes a
review light). Any edit to a promise,
criterion or case is structural, so `packet.diff.friction` reads `full` (type the version,
view the key sections); it reads `light` only when the diff is `none`. The case lines are
not hashed through the diff (the cases are hashed with their promises), so a decision
pinned before they existed still matches.

### Confirm

```sh
lattice confirm                 # in CI, on the default branch; needs GITHUB_TOKEN or GH_TOKEN
lattice confirm --offline       # read the cache only
lattice confirm --unsigned      # what a signature could re-confirm, with the commands
```

For each decision file on the default branch, `confirm` finds the commit that
added it, the pull request that merged it, and that request's reviews from the
GitHub REST API. A decision counts only if all hold:

- an approving review exists from a user other than the pull request's author;
- that user is not a bot (a `[bot]` login, or account type `Bot`) or an agent actor;
- the review is on the head that was merged (`pull.head.sha` equals the review's
  `commit_id`), and it is the reviewer's last verdict: a later change request or
  dismissal cancels it;
- the decision's packet hash still matches the BRD, its promises and the diff.

A decision whose hash no longer matches reads `stale`. A prepared decision that is
not merged reads `pending`. With no token, `unknown`. `--offline`, or a missing
cache, reads `unknown`, never confirmed. The result is
`lattice/.cache/confirm.json` (`lattice.confirm/1`: derived, cached by commit,
never committed), with `{id, version, action, state, via, approver_login, pr,
merged_sha, verified_at}` per decision, plus `pr_author` and `pr_author_bot`
(who opened the pull request) and any informational `notes`. `lattice audit` reads the `confirmed`
signal from that file only. Set branch protection to require a current review;
`confirm` does not rely on it, but it keeps the platform's own state in step.

The file is stamped with what produced it:

```json
"stamp": { "commit": "cad25d1...", "ci_run": "4812", "platform": "github",
           "repository": "acme/shop", "digest": "sha256:..." }
```

`ci_run` is the CI run id (`GITHUB_RUN_ID`, `CI_PIPELINE_ID`, ..., the same one
`results ingest` and `break` record) or `local`. `digest` covers the records and
the other stamp fields. The stamp is provenance, not a signature: there is no
secret, so it shows an edited or hand-written file for what it is rather than
proving who wrote it. Every reader (the audit's `confirmed` signal, `validate`,
`/api/v2`) takes a verdict only from a file whose stamp is intact:

| The file | Reads |
|---|---|
| has no stamp (hand-written, or written before v0.15.1) | `unknown`, `CONFIRM_UNSTAMPED` |
| has a digest that does not match its records, commit or `ci_run` | `unknown`, `CONFIRM_TAMPERED` |
| is tracked by git (someone committed it) | `unknown`, `CONFIRM_TRACKED` |
| is stamped by a local run | `human (local, not CI)` in a local audit or check |
| under `lattice check --ci`: is local, or of another commit than head | `unknown`, `EVIDENCE_NOT_CI` |

`confirm` reuses a previous file's verdicts only when its stamp is intact, and a
CI run never reuses a local file's verdicts. Keep `lattice/.cache/` in
`.gitignore`; `lattice init` adds it when it creates the `.gitignore`.

`lattice validate` reads the same file: a machine-written draft BRD (`llm_from_code`
or agent-authored) whose current version has a confirmed `approve` or `ack` there
no longer raises `BRD_UNAPPROVED_LLM`. A typed `approved_by`, a decision for another
version, a `pending` or `stale` verdict, or a confirmed `return` or `overturn` does
not satisfy it.

### Confirm by signature

A decision also reaches `human` through a second route: a detached SSH
signature over the decision, made with a hardware security key. Either route
suffices, unless `confirm.routes` (below) narrows them. The signature route
needs no second GitHub account, so it works on a project where one person owns
the repository and an agent opens the pull requests.

**Which routes count: `confirm.routes`.** By default both routes confirm. A
workspace can accept fewer in `lattice/config.yaml`:

```yaml
confirm:
  routes: [signature]           # review, signature, or both (the default)
```

With `routes: [signature]`, a decision confirmed only by an approving review
reads `pending`, `ROUTE_NOT_ACCEPTED`, never `human`. The record keeps the
approver and the pull request for display, and its text names the routes the
workspace accepts. `routes: [review]` does the same for a signature. The
setting only narrows: every reader (`audit`'s `confirmed` signal, `check`,
`validate`, `feature show`, `/api/v2`, `confirm --offline`) applies it to the
`confirm.json` it reads, including one written before the setting changed. An
entry that is not `review` or `signature` accepts nothing: `validate` reports it
as `VALIDATE_POLICY` (error) and `lattice confirm` refuses with `BAD_CONFIG`.
A `config.yaml` that cannot be read leaves the accepted routes unknown:
`lattice confirm` refuses with `BAD_CONFIG`, and every reader reads each
confirmed verdict as `unknown`, `CONFIRM_ROUTES_UNREADABLE`. Use it on a
repository with one maintainer, where an agent's token can approve pull
requests: there the review route proves only that another account approved.

**Enrol a key once.**

1. Create a security key (a FIDO2 token such as a YubiKey):

   ```sh
   ssh-keygen -t ed25519-sk -C "ana lattice decisions"   # or -t ecdsa-sk
   ```

2. Add the public key to your GitHub account as a **signing** key, not an
   authentication key: Settings → SSH and GPG keys → New SSH key → Key type
   *Signing Key*, or `gh ssh-key add ~/.ssh/id_ed25519_sk.pub --type signing`.
3. Add one line to `lattice/allowed_signers` (OpenSSH allowed_signers format,
   with your GitHub login as the principal) and merge it to the default branch
   before you sign a decision with the key:

   ```text
   ana namespaces="lattice-decision" sk-ssh-ed25519@openssh.com AAAAGnNrLXNzaC1lZDI1NTE5... ana
   ```

   Only plain lines count: exact principals and at most a `namespaces` option
   that includes `lattice-decision`. Lines with wildcard principals,
   `cert-authority`, `valid-after` or `valid-before` are skipped.

   The first `allowed_signers` is the root of trust: merge it yourself.
   Every later key must be endorsed by a key already trusted. The holder of
   that key runs, on the branch with the change:

   ```sh
   lattice sign --signers          # prints the signers added and removed, then signs
   git add lattice/allowed_signers lattice/allowed_signers.sig
   git commit -m "signers: enrol bo"   # the change and its endorsement in one commit
   ```

   `--signers` compares the working tree's `allowed_signers` with the default
   branch (`--branch` names it), writes `lattice/allowed_signers.sig`, and has
   the same refusals as `lattice sign`. Commit the `.sig` in the same commit as
   the change, or merge with a merge commit or a squash, so the change lands
   with its endorsement. Removing a key needs no endorsement.

**Sign a decision.**

```sh
lattice prepare brd approve brd.refunds --version 3
git switch decision/brd.refunds-v3-9e1f04ab
lattice sign brd.refunds-v3-9e1f04ab            # touch the key when it blinks
git add lattice/decisions/approvals/brd.refunds-v3-9e1f04ab.sig
git commit -m "decision: sign brd.refunds v3"
git push -u origin HEAD                         # open the PR and merge it
```

`lattice sign` prints the decision (action, BRD, version, packet hash,
promises, repository) and the exact message, then runs
`ssh-keygen -Y sign -n lattice-decision -f <key>` and writes `<stem>.sig` beside
the decision file. `--key` names the private key file (its `.pub` must sit beside
it); the default is the first `~/.ssh/id_ed25519_sk*` or `~/.ssh/id_ecdsa_sk*`. It
refuses a key that is not a security-key type (`SOFTWARE_KEY`), and refuses to
run without a terminal (`SIGN_NO_TTY`), under CI (`SIGN_CI`) or for an agent
actor (`SIGN_AGENT`). It is the only lattice command that runs an outside
program, it is not offered through MCP or the shipped skills, and it is a
separate command from `confirm` so that it is easy to deny to agents in their
permission settings.

The signed message is computed by the signer and the verifier, never stored:

```text
lattice-decision/v1
repository acme/shop
id brd.refunds
version 3
action brd.approve
packet sha256:9e1f04ab...
rows refunds:INV-1,refunds:INV-3
```

`rows` lists the promises the decision names (`none` when it names none):
a `promise confirm` packet hash does not say which promises the batch covers, so
the message does. The repository is `GITHUB_REPOSITORY`, or the origin remote's
`owner/name`, so sign in a clone whose `origin` is the repository itself, not a
fork. A message missing any field is neither signed nor accepted.

**What `lattice confirm` checks.** For a decision file on the default branch
with a `.sig` beside it, all of these must hold, natively in Go (no
`ssh-keygen` at verify time):

| Check | Otherwise |
|---|---|
| The file is an SSHSIG signature | `pending`, `SIGNATURE_MALFORMED` |
| The key is `sk-ssh-ed25519@openssh.com` or `sk-ecdsa-sha2-nistp256@openssh.com` | `pending`, `SOFTWARE_KEY` |
| The signature carries the user-presence flag (the key was touched) | `pending`, `SIGNATURE_NO_PRESENCE` |
| The namespace is `lattice-decision` | `pending`, `SIGNATURE_NAMESPACE` |
| It verifies over this decision's message: a signature copied to another decision, version, action, packet, promise list or repository fails | `pending`, `SIGNATURE_INVALID` |
| The key is listed for the login in `lattice/allowed_signers` at the base commit | `pending`, `SIGNER_NOT_ALLOWED` |
| ... and is trusted there: in the root `allowed_signers`, or added by an endorsed change (below) | `unknown`, `SIGNER_NOT_ENDORSED` |
| ... and still at the tip of the default branch | `pending`, `SIGNER_REVOKED` |
| The history of `allowed_signers` can be followed to its root (not a shallow clone) | `unknown`, `SIGNERS_NO_HISTORY` |
| The login is a human's, not a bot (`...[bot]`) or agent (`agent...`) login | `pending`, `SIGNER_NOT_HUMAN` |
| GitHub lists the key among the login's SSH signing keys (`GET /users/{login}/ssh_signing_keys`) | `pending`, `SIGNER_NOT_ON_GITHUB` |
| GitHub can be read (a token is set and the API answers) | `unknown`, `GITHUB_ANCHOR_UNKNOWN` |
| The base commit exists (not a root commit or a shallow clone's edge) | `unknown`, `SIGNATURE_NO_BASE` |

The record then reads `confirmed` with `via: signature`, the signer's login,
the key's `fingerprint`, the `decision_path` and the `signature_base`; a changed
packet reads `stale` as on the review route. Review-route records carry
`via: review`. If the signature fails and an approving review exists, the review
counts (unless `confirm.routes` leaves the review route out: then the
signature's failure is the verdict). Text output always names the method:
`human (signed by ana, SHA256:Xb3k9...)` or `human (approved by bo on PR #41)`.

**The sole-approver note.** When a bot or agent account opened the pull request
and the approving reviewer is the repository's only human with write access,
the review record carries an informational note, `SOLE_HUMAN_APPROVER`, in
`notes` and under the record in text:

```text
confirmed  brd.approve brd.refunds v3 human (approved by ana on PR #41)  [APPROVED]
    note: PR #41 was opened by release-agent[bot], a bot or agent account, and ana, who approved it, is the only human with write access: the review route here proves only that an account other than the author approved; to require a hardware-key signature, set confirm.routes: [signature] in lattice/config.yaml  [SOLE_HUMAN_APPROVER]
```

The note never changes the verdict. The collaborator list comes from
`GET /repos/{owner}/{repo}/collaborators`, which needs push access: when it
cannot be read, `confirm` says nothing rather than guess. The note is not made
under `routes: [signature]`, where the review does not confirm.

The signer is the login `allowed_signers` and GitHub's signing keys name, never
the pull request's author, so a decision an agent prepared and a human signed
reads `human` with the human's login. To show that a machine prepared it, a
signature record carries `pr_author` (who opened the pull request that added the
decision) and `pr_author_bot`, and the text adds a note under the record:

```text
confirmed  brd.approve brd.refunds v3 human (signed by ana, SHA256:Xb3k9...)  [SIGNED]
    note: the pull request was opened by release-agent[bot], a bot or agent account: it prepared the decision, and ana signed it
```

**The base commit.** `allowed_signers` is read with git plumbing at one commit:
the first parent of the commit on the default branch's first-parent history that
added (or last changed) the `.sig`. For a pull request merged with a merge commit
or squashed, that is the default branch just before the merge, so a key added to
`allowed_signers` in the same pull request does not count, and the pull request's
head is never read. With rebase merging every rebased commit lands on its own, so
the base is the commit just before the one that adds the `.sig`, and a key added
earlier in the same pull request is in it. That key still does not count unless
its addition was endorsed (below), so every merge style is safe.

**Endorsed changes to `allowed_signers`.** A key counts only when it is trusted,
and trust is followed along the default branch's first-parent history of
`lattice/allowed_signers`, from git objects, by `confirm` and every reader:

- The first version that lists a signer is the **root of trust**, accepted as
  is. `confirm` reports it as `ROOT_SIGNERS` with its commit
  (`signers  trust starts at 1a2b3c4d ...  [ROOT_SIGNERS]`), so you can see when
  trust started. Check that commit: whoever merged it chose the first keys.
- Every later commit that adds a signer (a new line, or a login or the
  `lattice-decision` namespace added to a line) must carry
  `lattice/allowed_signers.sig` in the same commit: an endorsement made with an
  `sk-` key, with user presence, by a key trusted *before* the change. Keys added
  without one do not count, from that commit on, and decisions they sign read
  `unknown`, `SIGNER_NOT_ENDORSED`. The report lists such a change as
  `SIGNER_NOT_ENDORSED`; endorsed ones read `SIGNERS_ENDORSED`.
- A change that only removes signers needs no endorsement (`SIGNERS_REMOVED`):
  revocation stays one commit.
- Endorsements chain: a key endorsed by ana can endorse the next key, and a key
  can endorse its own replacement while it is removed in the same change.

The endorsement signs this message, computed and never stored:

```text
lattice-signers/v1
repository acme/shop
after 5e6f7a8b...          # the last commit that changed allowed_signers on the default branch
signers sha256:...         # the new file, with CRLF read as LF
```

`after` ties it to the history, so an old endorsement cannot re-add a key that
was removed since. If anything else changes `allowed_signers` on the default
branch first, sign again. Enrol a second key early (a backup security key):
once every trusted key is removed, no change can be endorsed again, and the
signature route stays closed for that history. A shallow clone cannot show
where trust started, so `confirm` and the readers need the full history
(`fetch-depth: 0`).

**Re-confirm by signature.** `lattice confirm --unsigned` lists the decisions a
signature could confirm, and the exact commands for each: an approval or
acknowledgement typed into a BRD file (by `brd approve`, `brd ack`, or written
before Lattice marked such notes `local`), a council's approval that its
delegating human has not acknowledged yet (`council`: read the council's decision
first), and a confirmation by review. It reads only: it signs, prepares and
writes nothing (`lattice.confirm-unsigned/1`).

```text
local  brd.ack brd.refunds v3  (a local note typed as ana@example.com, not a confirmation; ...)
    lattice prepare brd ack brd.refunds
    git switch decision/brd.refunds-v3.ack-9e1f04ab
    lattice sign brd.refunds-v3.ack-9e1f04ab
review  promise.confirm brd.refunds v3  (approved by bo on PR #41; sign the decision file, ...)
    lattice sign lattice/decisions/approvals/brd.refunds-v3.confirm-1a2b3c4d-9e1f04ab.yaml
```

- `local`: the BRD's current version was approved or acknowledged by
  `lattice brd approve` or `brd ack` (a typed name), and no decision confirms
  it. The stem is the one `prepare` writes now; if the BRD or a promise changes
  first, `prepare` prints another.
- `review`: a decision confirmed by an approving review, on a current version,
  that no signature confirms. Under `confirm.routes: [signature]` it also lists
  a decision a review approved that therefore reads `pending`
  (`ROUTE_NOT_ACCEPTED`). It reads the cache `lattice confirm` wrote, so run
  that first.

**Revocation.** Remove the key from `allowed_signers` on the default branch, or
from your GitHub signing keys, and everything it signed reads un-confirmed: the
tip check fails at once for every reader, and GitHub is asked again on every
`confirm` run (nothing about the signing keys is cached across runs).

**Readers re-verify.** `lattice check`, `audit`, `validate`, `/api/v2` and
`confirm --offline` re-verify every signature verdict in `confirm.json` from
git objects on the branch `confirm` read: the signature, its message, key type
and flags, and `allowed_signers` at the base commit and the tip. The only fact
they take from the stamped `confirm.json` is GitHub's word that the key is the
login's signing key. A record claiming a signature verdict that does not
re-verify (written by hand, or the `.sig` is gone or changed) reads `unknown`,
`SIGNATURE_NOT_REVERIFIED`, never `human`.

**Threat model.** The route is built for a project where an agent works with the
human's own GitHub token.

- An agent with that token *can* open and merge pull requests, edit decision
  files, write `confirm.json` by hand, run `lattice confirm`, add a key to
  `allowed_signers` in a pull request, and copy a `.sig` from one decision to
  another. None of these produces `human`: a key enrolled in the same pull
  request is read before the change; a copied signature fails the message; a
  hand-written verdict is re-verified away; a key made in software is refused by
  type. A key added to `allowed_signers` counts only with an endorsement by a key
  already trusted, and an agent cannot make one: so adding a key in the same pull
  request, under any merge style, adds nothing.
- An agent *cannot* touch the hardware key, so it cannot make your key sign.
- The residual risk is the root of trust. The `sk-` key type is not attested:
  Lattice refuses key types that are certainly software, but it cannot prove a
  key with an `sk-` type lives in hardware; such a key can be generated in
  software. Before the first `allowed_signers` is merged, an agent whose token
  may manage SSH signing keys could add a key it generated to your GitHub
  account and merge it as the root. After the root, that no longer works: the
  key also needs an endorsement from a key already trusted. So create the root
  yourself, check the `ROOT_SIGNERS` commit `confirm` reports, withhold the SSH
  signing key permission from any token an agent holds (the classic
  `admin:ssh_signing_key` and `write:ssh_signing_key` scopes, or the
  fine-grained "SSH signing keys" account permission), and check the
  fingerprint every verdict shows against your own key
  (`ssh-keygen -lf ~/.ssh/id_ed25519_sk.pub`).
- Trust is read from the default branch's history, so protect that branch
  against force pushes: a rewritten history can start trust at a new root.
  The `ROOT_SIGNERS` commit `confirm` reports would then change, which is what
  to watch for.
- The terminal and CI refusals of `lattice sign` are friction, not a boundary:
  the boundary is the touch on the hardware key.

**The fallback.** A project with no hardware key keeps the review route, which
proves that an account other than the pull request's author approved the merged
head. Delegated confirmation (`docs/rnd/0010-assurance-decisions.md`, section D)
is the designed fallback for projects that have neither: its state is
`delegated`, never `human`.

### The local commands

`lattice brd approve`, `brd ack` and `brd overturn` still work for now. Each
prints a deprecation line and records `local: true`, and the audit shows
"prepared by a local session, not confirmed". `lattice confirm --unsigned` lists
the approvals and acks to re-confirm, with the commands. Councils (`brd vote`, `brd
decide`) stay as advice: a council's approval reads "reviewed by council, awaiting
you" until a human ack is confirmed.

## 4m. Running the check in CI

Lattice runs `lattice check` on itself in `.github/workflows/lattice-check.yml`,
report only. Copy it for your repository and swap the test command for yours.

```yaml
name: Lattice check
on:
  pull_request:
  push:
    branches: [main]
permissions:
  contents: read
jobs:
  check:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      actions: read            # find and download the base commit's audit
    env:
      GH_TOKEN: ${{ github.token }}
      GH_REPO: ${{ github.repository }}
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
          ref: ${{ github.event.pull_request.head.sha || github.sha }}
          persist-credentials: false
      # Install lattice (a release binary on PATH) and your toolchain here.
      - name: Test suite, as JUnit
        continue-on-error: true            # a red suite is evidence, not a stop
        run: ./run-your-tests --junit "$RUNNER_TEMP/unit.xml"
      - run: lattice results ingest "$RUNNER_TEMP/unit.xml"
      - run: lattice break --timeout 180s  # on the base branch, and on PRs that touch code
      # Base branch: save this commit's audit for later pull requests.
      - if: github.event_name == 'push'
        run: lattice check --snapshot
      - if: github.event_name == 'push'
        uses: actions/upload-artifact@v4
        with:
          name: lattice-audit-${{ github.sha }}
          path: lattice/.cache/audit/${{ github.sha }}.json
          if-no-files-found: error
          retention-days: 30
      # Pull request: restore the merge base's audit, then judge the change.
      - if: github.event_name == 'pull_request'
        env:
          BASE_REF: ${{ github.base_ref }}
        run: |
          mb="$(git merge-base "origin/$BASE_REF" HEAD)"
          run="$(gh run list --workflow lattice-check.yml --branch "$BASE_REF" --commit "$mb" \
            --status success --limit 1 --json databaseId --jq '.[0].databaseId // empty')"
          mkdir -p lattice/.cache/audit
          [ -z "$run" ] || gh run download "$run" --name "lattice-audit-$mb" --dir lattice/.cache/audit || true
      - if: github.event_name == 'pull_request'
        env:
          BASE_REF: ${{ github.base_ref }}
        run: |
          rc=0
          lattice check --base "origin/$BASE_REF" --ci --markdown >lattice-check.md || rc=$?
          cat lattice-check.md >>"$GITHUB_STEP_SUMMARY"
          [ "$rc" -le 1 ]     # 1 is a verdict (report only); 2 is a setup error and fails
```

Post the report as one sticky comment in a second job that has
`pull-requests: write` and nothing else beyond `contents: read`: upload
`lattice-check.md` from the job above, download it there, find the comment whose
body starts with `<!-- lattice-check -->` (`gh api repos/$GITHUB_REPOSITORY/issues/<n>/comments`),
and `PATCH` it, or `POST` one if none exists. Lattice's own
`scripts/ci-lattice-check.sh` does this (`touches-go`, `snapshot`, `fetch-base`,
`check`, `comment`) and `scripts/ci-lattice-check.test.sh` tests it with a fake
`gh`. Mark that job `continue-on-error`: a fork's token is read-only.

Notes:

- **Report only.** The `check` step tolerates the verdict's exit code (1) and
  nothing else. Making it blocking is a choice for the owner (which tier blocks,
  and from when): drop the tolerance and require the job in branch protection.
- **Evidence is CI's.** `--ci` reads evidence only when it is stamped with the head
  commit and a CI run id, so the ingest and `break` steps must run in the same job
  as `check` and on the commit it judges (hence checking out the pull request's
  head, not the merge commit).
- **No base audit** (first run, an expired artifact, a base that predates the
  workflow) reads `UNPROVEN` with "no base audit", never `BROKEN`.
- **Cost.** `lattice break` re-runs each promise's verifiers once per enforcer. On
  Lattice (187 promises) it took about four minutes on top of the 35-second test
  run. Run it on the base branch and on pull requests that touch code only.
- **Called workflows.** A job in a workflow called with `uses:` cannot ask for more
  permissions than its caller grants. If your release workflow calls your CI
  workflow with `contents: write` only, keep this in a workflow of its own.
## 4n. The /api/v2 contracts

`lattice serve` also serves the JSON the coming React UI and agents bind to, under `/api/v2`.
`/api/v1` is unchanged. Every payload carries `contract` and `commit` (and `ci_run` and
`evidence_tied` for the results it stands on), and every assurance value has one shape:
`{state, honesty, code, params, text}`.

| Endpoint | Contract | Returns |
|---|---|---|
| `GET /api/v2/meta` | `lattice.meta/1` | kernel version and commit, dirty flag, the provenance of the results, coverage, breaks, audit snapshot and `confirm.json` (commit, `ci_run`, tied to head or not), and the CSRF token for `prepare` |
| `GET /api/v2/queue` | `lattice.queue/1` | what needs a human, ranked by tier, then blast radius (entry points, then promises), then age; each with a `reason` and a `link` |
| `GET /api/v2/brds`, `GET /api/v2/brds/{id}` | `lattice.brd/1` | the BRD page; its state (DRAFT, PROPOSED, CONFIRMED, DRIFTED, SUPERSEDED) with sub-chips such as "reviewed by council" |
| `GET /api/v2/brds/{id}?version=N` | `lattice.brd/1` | the review packet: the structural diff since the last confirmed version, the `packet_hash` exactly as `lattice prepare` pins it, the promise counts, the out-of-reach promises |
| `GET /api/v2/features/{id}` | `lattice.brd/1` | the feature drawer: promises with signals, gaps, enforcers and verifiers, entry points, related features |
| `GET /api/v2/check?base=<rev>[&ci=1]` | `lattice.check/1` | exactly `lattice check --json` |
| `GET /api/v2/health` | `lattice.health/1` | the evidence matrix per feature: results, per-test coverage, breaks, confirm data, each with its commit and whether CI stamped it |
| `POST /api/v2/prepare` | `lattice.prepare/1` | what `lattice prepare` prints: `{branch, files, packet_hash, command}`, or its refusal `{refused, params, text, next}` |

The API computes nothing that counts. Every value comes from the audit, the check, `confirm`
and `prepare`, and a field the kernel has no source for reads
`{"state": "unknown", "code": "NOT_COMPUTED"}`, never a guess (the story, a feature's sources, the
cases of an invariant that declares none, the text-only diff, the blind spot outside a
change, where a feature runs, open proposals). A promise whose invariant has cases carries
them in `case_rows`, each with the audit's `coverage`, and sums them up in `cases`: `pass`
(`CASES_COVERED`) only when every case that is not retired is covered, `fail`
(`CASES_FAILING`) when one is failing, `unknown` (`CASES_NOT_COVERED`) otherwise. A view
of one promise, feature or BRD is never `ASSURED` or `BROKEN`: those need a base, so ask
`/api/v2/check`. Unknown, `UNPROVEN` and partial are never a `pass`; every payload is checked
against that rule before it is sent, and a payload that breaks it is a 500, not a response.

`POST /api/v2/prepare` is the one write, and it writes one local decision branch. It is
refused unless the connection comes from this machine (not through a proxy), carries the
token `GET /api/v2/meta` issued to it in the `X-Lattice-CSRF` header, and comes from the
same origin. It never pushes, never opens a pull request and never returns anything that
confirms: a decision counts only when `lattice confirm` reads the platform's approval of the
merged pull request. Send `packet_hash` from the review packet to refuse the decision if the
packet moved after you read it (`PACKET_STALE`). Prepare is not an MCP tool; agents use
`propose`.

## 4o. Layers: what kind of runtime is this code?

A **layer** says what kind of runtime a piece of code lives in. The vocabulary
is closed:

| Layer | Meaning |
|---|---|
| `ui` | runs in a browser or an app |
| `api` | request handlers: routes and RPC endpoints |
| `service` | application logic and command-line programs |
| `data` | database functions, policies and triggers |
| `async` | scheduled jobs, queue workers, event and webhook consumers |
| `edge` | edge or serverless functions |
| `external` | third-party integrations |
| `unknown` | nothing says |

Three words are easy to mix up. `layer` is the runtime kind of code, computed as
below. `tier` on a block is its existing frontend/backend grouping (a source's
`tier:`), and `tier` on a BRD criterion is how critical it is. They are separate
and unchanged.

Layers are computed, never guessed. A symbol takes the first of these that
applies:

1. **An adapter fact.** A symbol of the Postgres adapter (a function, policy or
   trigger) is `data`. The handler of a declared entry point takes the layer of
   its kind: `http_route` and `grpc` are `api`; `cron`, `queue`,
   `event_consumer` and `webhook` are `async`; `cli` is `service`. This holds for
   the handler symbol only, not for what it calls. When entry points of
   different kinds share one handler, the entry point with the lowest id
   decides.
2. **A declared path rule.** `layers:` in `lattice/workspace.yaml` lists
   `{glob, layer}` entries, matched against the file's path as the graph names it
   (relative to its code root). The first match wins.
3. **Otherwise `unknown`.**

An adapter fact beats a rule. Each symbol records which one decided it as
`layer_source`: `adapter`, `rule`, or empty for `unknown`.

```yaml
# lattice/workspace.yaml
layers:
  - glob: "apps/web/**"
    layer: ui
  - glob: "services/**"
    layer: service
  - glob: "functions/**"
    layer: edge
```

A layer outside the vocabulary or a glob that is not a valid pattern is a
`VALIDATE_POLICY` error, and the rule matches nothing.

A feature's layers are the sorted set of the layers of its implementation
symbols, and an invariant's those of its enforcers. They are computed and cannot
be declared.

- `lattice audit --json` rows carry `layers` (the promise's enforcers' layers),
  and each enforcer a `layer`.
- `lattice feature show <id>` lists the feature's layers, and its `--json` output
  gains a `layers` field.
- `/api/v2` promise objects carry `layers`, their enforcers `layer`, and a feature
  page `layers`.
- The block map's **Group by > Layer** lens groups blocks by layer, colours each
  group, gives blocks that span several layers a neutral colour and `unknown` its
  own, and shows a legend with a count per layer.

## 5. Day-to-day commands

```sh
lattice extract                       # refresh the knowledge graph
lattice validate                      # check everything
lattice fmt [--check]                  # canonical field order for features, BRDs, entry points
lattice feature list                  # what features exist
lattice feature show checkout.refund   # one feature, hydrated
lattice symbol <fqn>                   # Lattice context of a code symbol
lattice search "refund" [--semantic]   # find features/capabilities/invariants
lattice view developer|product|business|c4   # c4: Context/Container/Component
lattice view c4 --format structurizr          # C4 as a Structurizr DSL workspace
lattice view entry-points                     # every trigger (HTTP/CLI/cron/queue) grouped by kind
lattice view flows [<ep-id>]                  # Mermaid: trigger -> handler -> features
lattice serve [--port 7070] [--token X]       # v0.4.0 web UI: tracking + visualising + editing
lattice analyze proposal <file>        # conflict + impact analysis
lattice impact --since origin/main     # read-only change-impact report (PR comment: --format markdown)
lattice patch --from-file p.json --preview|--apply
lattice initiative show <id>           # initiative + tasks
lattice task pick-next                 # next actionable task
lattice agent context --task <id>      # full bundle for one task
lattice results ingest <reports>...    # test results + coverage of one run (section 4f)
lattice results tests --verifiers      # verifiers with each runner's filter, for per-test coverage
lattice mutation run                   # mutation-test invariant code
lattice structural-checks run          # run custom structural checks
lattice skills list                    # shipped agent skills
```

Add `--json` to any command for machine-readable output.

### 5a. After a merge: `lattice backprop`

`lattice backprop --since <ref>` diffs the files changed since `<ref>`
against the graph and lists the grounded (approved-BRD) criteria whose
enforcers moved. With `--flag` it raises a meaning flag on each one until a
human clears it.

Like `validate`, every run records its outcome ("no doc impact", or the
affected criteria) as the latest `backprop` run in the untracked
`lattice/.cache/last-runs.json` (`lattice ledger runs` shows it). The tracked
ledger changes only with `--flag`: each flag raised is a state change and is
ledgered as a `flag` event.

For a report that writes nothing, such as a pull-request comment, use
`lattice impact` (below).

### 5b. Change impact on a pull request: `lattice impact`

```sh
lattice impact --since origin/main                    # terminal
lattice impact --since origin/main --format markdown  # PR comment
lattice impact --since origin/main --json             # machine-readable
```

`lattice impact` compares `merge-base(<ref>, HEAD)` with the working tree
(HEAD plus uncommitted and untracked files, as extraction reads them) and
says which base commit it used. It is **read-only**: it writes no ledger
entry, raises no flag and changes no tracked file. Git runs on a private
copy of the index, so even `.git/index` is left as it was. The head graph is
built the way `validate` builds it, minus the LLM labeling of entry points
(which saves files). Nothing is checked out for the base side: each changed
file is read with `git cat-file` and parsed with the same adapters.

The report has five sections:

| Section | What it lists |
|---|---|
| Changed code | Symbols whose own lines (declaration to last line) a `git diff -M --unified=0` hunk touches, on the base or the working-tree side. A symbol that only shifted in its file, or moved with its text unchanged, is not listed. |
| Affected criteria | Each BRD criterion whose `maps_to_invariant` (one ref or a list) has a changed enforcer, with the criterion, its invariants and the enforcers that changed. A direct-wired criterion counts through its `enforced_by` symbols and `@enforces <brd>:<criterion>` tags. Approved BRDs come first. Draft and proposed BRDs are in a separate section, shown by default; `--include-drafts=false` hides it. Superseded BRDs are left out. |
| Removed enforcement | A base-side enforcer that no longer enforces its invariant: the symbol was deleted, its `@enforces` tag was dropped (the tag sits above the symbol, outside its lines, so this compares each file's (symbol, invariant) pairs on both sides), or its file was deleted or moved out of the scanned tree. A file moved elsewhere in the scanned tree is not a removal. |
| Manifest changes | Features, BRDs and entry points added, removed, renamed and changed, with every changed field named (`invariants[INV-1].verifiable_by`, `user_scenarios[US-1].then`, `success_criteria[SC-2].maps_to_invariant`). The walk is generic over the schema, so a new field is compared without a code change. A manifest moved out of `lattice/features/` is removed; one moved within it is renamed, compared by id. |
| Code roll-up | With a SCIP code graph (`lattice extract --with-code-graph`), the blast radius of each changed or removed enforcer, rolled up to referencing modules, test files and entry points (through each entry point's handler and `flow`). `--max-symbols N` (default 50) caps how many are expanded, enforcers of approved criteria first, and the report says when it truncated. Without a code graph the section says "code graph unavailable" and the command still succeeds. |

When the change touches an acceptance case, the report adds a **Touched cases** section
(`touched_cases` in `--json`, always present): each case whose own `enforcer` changed
(`why: enforcer_changed`) and each case a changed test is tagged with (`@verifies
<promise>#<case>`, `why: verifier_changed`), with the symbol and its change. A change to
an enforcer of the invariant that the case does not name touches the promise, not the
case. `lattice break --promise <promise>#<case>` re-breaks one of them.

When an enforcer's own lines change and its invariant's statement and tier do
not, the report adds a **Notes** section with a `STATEMENT_UNCHANGED` note for that
invariant: re-read the statement and confirm it still describes the code. Notes are
informational and appear only when there is one; `--json` always has `notes`.

The markdown form is meant to be posted as a sticky PR comment. It starts
with `<!-- lattice-impact -->`, so a CI job can find and replace its own
comment. A sample:

```markdown
<!-- lattice-impact -->
## Lattice impact

Compared with base `1a2b3c4d5e6f` (merge base of `main` and HEAD `7a8b9c0d1e2f`), including uncommitted changes.

11 files changed · 5 symbols changed · 4 approved criteria affected · 4 enforcements removed

### Changed code

| Change | Symbol | File | Lines | Enforces |
|---|---|---|---|---|
| removed | `refund.Gone` | `refund/gone.go` | 7-9 (base) | `refund:INV-4` |
| added | `refund.NewHelper` | `refund/guard.go` | 6-8 |  |
| modified | `refund.Guard` | `refund/guard.go` | 12-17 (was 7-9) | `refund:INV-1` |
| removed | `refund.Limit` | `refund/guard.go` | 18-20 (base) | `refund:INV-2` |
| modified | `refund.Audit` | `refund/guard.go` | 25-27 (was 23-25) |  |

### Affected criteria: approved BRDs

- **`brd.refunds` / SC-1** (tier 3): A refund never exceeds what was captured.
  - invariants: `refund:INV-1`
  - `refund:INV-1` changed enforcers: `refund.Guard` (modified, `refund/guard.go`)
- **`brd.refunds` / SC-2** (tier 1): A refund is tagged and reversible.
  - invariants: `refund:INV-3`, `refund:INV-4`, `refund:INV-6`
  - `refund:INV-3` changed enforcers: `refund.Tagged` (enforcement removed, `refund/tagged.go`)
  - `refund:INV-4` changed enforcers: `refund.Gone` (enforcement removed, `refund/gone.go`)
- **`brd.refunds` / SC-3** (tier 1, direct-wired): Every refund is audited.
  - invariants: `brd.refunds:SC-3`
  - `brd.refunds:SC-3` changed enforcers: `refund.Audit` (modified, `refund/guard.go`)
- **`brd.refunds` / SC-4** (tier 1): A refund is reported.
  - invariants: `refund:INV-5`
  - `refund:INV-5` changed enforcers: `refund.Moved` (enforcement removed, `refund/moved.go`)

### Affected criteria: draft and proposed BRDs (not yet approved)

_These BRDs are not the source of truth until a human approves them._

- **`brd.payouts` / SC-1** (tier 1): Daily refund volume is capped.
  - invariants: `refund:INV-2`
  - `refund:INV-2` changed enforcers: `refund.Limit` (enforcement removed, `refund/guard.go`)

### Removed enforcement

| Symbol | Invariant | File | Why |
|---|---|---|---|
| `refund.Gone` | `refund:INV-4` | `refund/gone.go:7` | file deleted |
| `refund.Limit` | `refund:INV-2` | `refund/guard.go:18` | symbol deleted |
| `refund.Tagged` | `refund:INV-3` | `refund/tagged.go:7` | @enforces tag dropped |
| `refund.Moved` | `refund:INV-5` | `refund/moved.go:7` | file moved out of the scanned tree (to vendor/moved.go) |

### Manifest changes

**Features**

- added `added`: `lattice/features/added.yaml`
- removed `legacy`: `lattice/features/legacy.yaml`
- renamed `notes`: `lattice/features/notes/old.yaml` → `lattice/features/notes.yaml`
- changed `refund`: `lattice/features/refund.yaml`
  - `capabilities[issue].rules`: `[A refund is issued against one capture.]` → `[A refund is issued against one capture., A refund names the capture it reverses.]`
  - `invariants[INV-1].verifiable_by`: added: `[test, mutation]`

**BRDs**

- changed `brd.refunds`: `lattice/brds/brd.refunds.yaml`
  - `user_scenarios[US-1].actor`: `customer` → `support agent`
  - `user_scenarios[US-1].verified_by`: added: `[refund.TestGuard]`
  - `user_scenarios[US-1].given`: added: `[a captured payment]`
  - `user_scenarios[US-1].when`: added: `[the customer asks for a refund]`
  - `user_scenarios[US-1].then`: added: `[the refund is issued]`
  - `user_scenarios[US-1].blocks`: added: `[blk.refund]`
  - `success_criteria[SC-1].tier`: `2` → `3`
  - `success_criteria[SC-2].maps_to_invariant`: `[refund:INV-3, refund:INV-4]` → `[refund:INV-3, refund:INV-4, refund:INV-6]`

### Code roll-up

Expanded 6 of 6 enforcer symbols.

| Enforcer | References | Modules | Test files | Entry points |
|---|---|---|---|---|
| `refund.Audit` | 1 | 1 | 0 | `ep.cli.refund` |
| `refund.Gone` | not in index | 0 | 0 | `ep.cli.refund` |
| `refund.Guard` | 2 | 1 | 1 | `ep.cli.refund` |
| `refund.Moved` | not in index | 0 | 0 | `ep.cli.refund` |
| `refund.Tagged` | not in index | 0 | 0 | `ep.cli.refund` |
| `refund.Limit` | not in index | 0 | 0 | `ep.cli.refund` |

**Entry points reached**

- `ep.cli.refund` (cli refund issue) via flow: refund, handler file

**Test files referencing them**: `refund/guard_test.go`
```

A GitHub Actions step that keeps one comment up to date (the checkout needs
the base branch's history, e.g. `fetch-depth: 0`):

```yaml
- run: lattice impact --since origin/${{ github.base_ref }} --format markdown > impact.md
- run: gh pr comment ${{ github.event.pull_request.number }} --edit-last --body-file impact.md ||
       gh pr comment ${{ github.event.pull_request.number }} --body-file impact.md
  env:
    GH_TOKEN: ${{ github.token }}
```

**Monorepos without a root `tsconfig.json`.** The SCIP indexers run from the
code root, so a repository whose packages each carry their own
`tsconfig.json` gets no TypeScript index. Point them at the packages instead:

```sh
lattice extract --with-code-graph --index-root apps/web --index-root apps/api
```

Each `--index-root` is relative to the code root (or absolute inside it).
The runs are merged into one index per language, with paths relative to the
code root, so `blast-radius` and the impact roll-up read it as if it had
been indexed from the root.

---

### Run only the tests a change touches

`lattice impact --since <rev> --tests` lists the verifier tests of the promises
a change touches, grouped by the `break.runners` of `lattice/config.yaml` (see
[Runners](#4i-break-testing-does-the-verifier-notice)). A promise is touched when
the change edits, removes or adds one of its enforcers, or edits one of its
verifiers, any change to a file that holds a verifier included. Every verifier of
a touched promise is listed, not only the changed ones. The command only lists:
it never runs a test.

A change can also touch production lines no promise guards: no `@enforces` span
or checked reference covers them (`lattice check` reports them as untraced or
unguarded). No verifier names those lines, so the listing also selects the tests
that exercise them, on the strongest evidence it has for each symbol:

| Evidence | Label | What it means |
| --- | --- | --- |
| Per-test coverage (`lattice results ingest --per-test`) | `coverage` | the test's own run executed the symbol: a fact |
| The static call graph `lattice extract` builds | `call_graph` | the test's calls reach the symbol: an upper bound, the call may not run |
| A test file beside the symbol's file with the same base name (`fees.test.ts`, `fees.spec.tsx`, `fees_test.go`, `test_fees.py`, or one in `__tests__/`) | `heuristic` | a guess |
| A test file that changed in the same commits as the symbol's file: in the change itself, or in two or more commits of the `break.co_change` window that score 0.25 or more, each commit weighing 1/(the files it changes) | `co_change` | a hint: the why names the shared commits |
| A test file whose text names the symbol as a whole word (case-sensitive; a compound name such as `TotalBanner` or `total_fee` of four characters or more, not a common one like `onClick`), or a literal route an entry point declares for its file | `mentions` | a hint: the why names the line |

Only tests are selected: a test function, a `test()` or `it()` case, a pgTAP
assertion, or a symbol tagged `@verifies`. The other symbols of a test file (a
fixture builder, a fake class, a date helper) are never listed as tests, and a
support file that holds only helpers is never hinted. The call graph walks
through a helper to the tests that call it.

A weaker source is used only when the stronger ones find no test. The two hints
catch an end-to-end spec that drives the built app and imports nothing from the
source tree. A symbol whose tests only a hint found is listed under `hinted`, so
the weak link stays visible. A symbol none of them finds a test for is listed
under `uncovered` ("no test is known to exercise this; run the full suite"): it
is never dropped silently.

The co-change history is one `git log --name-only` over a bounded window, and
each test file is read at most once per run. The window is set in
`lattice/config.yaml`:

```yaml
break:
  co_change:
    commits: 200    # the most commits read, from HEAD back (default 200)
    days: 365       # how far back (default 365)
    max_files: 50   # skip a commit that changes more files (default 50); a change
                    # within it counts as one unit, a larger one commit by commit
    min_commits: 2  # commits a test file must share (default 2); a change
                    # within max_files that edits it is enough
    min_score: 0.25 # each shared commit adds 1/(the files it changes); a test
                    # file needs this score (default 0.25), so one that only
                    # changed with the file in large commits is not hinted
```

```sh
lattice impact --since origin/main --tests          # a shell script, one line per runner
lattice impact --since origin/main --tests --json   # {runners, unmatched, hinted, uncovered}
```

The text form is one line per runner, with its ready-to-run command (in a
subshell that enters the runner's `dir`) and, in a trailing comment, the
promises and the unguarded symbols its tests exercise; then one comment line per
test file no runner matches; then a `hinted` block for the symbols whose tests
only a hint found; then, when some touched line has no known test, an
`uncovered` block:

```sh
(cd apps/web && pnpm vitest run src/checkout.test.ts src/fees.test.ts -t '(caps the fee|rejects a negative amount|shows the total)$')  # vitest: fees:INV-1, unguarded apps.web.src.total.total via call_graph
# unmatched e2e/fees.spec.ts (fees:INV-1): no break.runners entry matches
# unmatched e2e/checkout.spec.ts (unguarded apps.web.src.summary.OrderSummary via co_change (hint: e2e/checkout.spec.ts changed with apps/web/src/summary.ts in this change and 2 commits (1a2b3c4, 5d6e7f8))): no break.runners entry matches
# hinted: touched lines no promise guards whose tests only a hint (co_change, mentions) found
#   apps/web/src/summary.ts:4-20  apps.web.src.summary.OrderSummary via co_change
# uncovered: touched lines no promise guards and no known test exercises
#   apps/web/src/banner.ts:3-9  apps.web.src.banner.render
# run the full suite before merge
```

The verifiers are grouped exactly as `lattice break` groups them: by the first
runner whose `match` glob covers the file, with `{files}` (relative to `dir`)
and `{filter}` filled the same way, and `{junit}` filled with
`junit-<runner>.xml` in the runner's `dir`. `--json` gives each runner's `name`,
`dir`, `command` (as configured), `run` and `argv` (filled), `files`, `filter`,
`junit`, `tests`, `promises` and `why`, each `unmatched` file with its `tests`,
`promises`, `reason` and `why`, each `hinted` symbol with its `symbol`, `file`,
`lines`, `via`, `files` (its tests' files) and `tests`, and each `uncovered` symbol with its `symbol`,
`file`, `lines` and `reason`. `why` maps each test to why it is in the set:
`promise <id>` for a verifier of a touched promise, `unguarded <symbol> via
coverage|call_graph|heuristic` for a test of an unguarded line, and `unguarded
<symbol> via co_change|mentions (hint: …)` when only a hint links them. A test no
`break.runners` entry matches, Go
verifiers and `break.commands` templates included, is listed under `unmatched`
with the reason, never dropped; with no runner configured every verifier is
there ("no break.runners entry matches (none is configured)"). `lattice check
--json` carries the same listing as `tests`.

In CI, run the listed commands on the pull request for fast feedback, and keep
the full suite on the merge commit, since an unmatched test is not run, and a
`call_graph`, `heuristic`, `co_change` or `mentions` selection can miss a test
that only reaches the line at run time; when the listing has an `uncovered` block, run the full suite
before merge:

```yaml
jobs:
  touched-tests:
    if: github.event_name == 'pull_request'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - name: Run only the tests this change touches
        run: |
          lattice impact --since origin/${{ github.base_ref }} --tests > touched-tests.sh
          cat touched-tests.sh
          sh -e touched-tests.sh
  full-suite:
    if: github.event_name == 'push' && github.ref == 'refs/heads/main'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: pnpm test   # the whole suite, on the merge commit
```

## 6. Using Lattice with an AI service

There are three integration surfaces — use whichever your assistant supports:

| Surface | What it is | Best for |
|---|---|---|
| **MCP server** | 21 tools over the Model Context Protocol | Claude Code, Claude Desktop, Codex, Gemini, Cursor |
| **Agent skills** | Markdown how-to packs in `lattice/skills/` | Any agent that can read files |
| **CLI + `--json`** | Shell out to `lattice … --json` | Any agent that can run a command |

The MCP server simply wraps the CLI, so all three give the same capabilities.

### 6a. Claude Code

Claude Code reads an `.mcp.json` at the project root. Add Lattice:

```sh
cd /path/to/your/repo
claude mcp add lattice -- npx -y @salahmyn/mcp-server
```

or write `.mcp.json` directly:

```json
{
  "mcpServers": {
    "lattice": {
      "command": "npx",
      "args": ["-y", "@salahmyn/mcp-server"],
      "env": { "LATTICE_REPO": "." }
    }
  }
}
```

Then the shipped skills are already on disk at `lattice/skills/lattice/`.
Point Claude Code at them in `CLAUDE.md`:

```markdown
## Lattice
This repo uses Lattice. Before changing code, read the relevant skill in
`lattice/skills/lattice/` and call `lattice_get_agent_context` for the task.
Validate with `lattice_validate` before finishing.
```

### 6b. Claude Desktop

Add to the Claude Desktop MCP config (see [mcp-setup.md](mcp-setup.md)):

```json
{
  "mcpServers": {
    "lattice": {
      "command": "npx",
      "args": ["-y", "@salahmyn/mcp-server"],
      "env": { "LATTICE_REPO": "/abs/path/to/repo" }
    }
  }
}
```

### 6c. OpenAI Codex (Codex CLI)

Codex CLI supports MCP servers in `~/.codex/config.toml`:

```toml
[mcp_servers.lattice]
command = "npx"
args = ["-y", "@salahmyn/mcp-server"]
env = { LATTICE_REPO = "/abs/path/to/repo" }
```

If your Codex setup has no MCP support, it can call the CLI directly — see 6e.

### 6d. Gemini CLI

Gemini CLI reads `mcpServers` from `.gemini/settings.json`:

```json
{
  "mcpServers": {
    "lattice": {
      "command": "npx",
      "args": ["-y", "@salahmyn/mcp-server"],
      "env": { "LATTICE_REPO": "." }
    }
  }
}
```

### 6e. Any other agent (no MCP)

Any agent that can run a shell command can use Lattice directly — the MCP
server is only a convenience wrapper. Give the agent this instruction:

```
This repository uses Lattice. The `lattice` CLI is the interface.
- Run `lattice <command> --json` for machine-readable output.
- Read `lattice/skills/lattice/` for how-to guidance.
- Before editing: `lattice agent context --task <id> --json`.
- Edit manifests only via `lattice patch --from-file <f> --preview` then `--apply`.
- Before finishing: `lattice validate --json` must report `"ok": true`.
Every error includes a `next_action` object — act on its `kind` field.
```

---

## 7. The agent loop

Whatever the surface, an AI assistant should follow the same loop. MCP tool
names are shown; the CLI equivalent is in parentheses.

1. **Orient** — list skills and load the relevant ones.
   `lattice skills list` → read `lattice/skills/lattice/working-tasks/SKILL.md`.

2. **Pick work** — `lattice_pick_next_task` (`lattice task pick-next --json`).
   Returns the next task whose dependencies are satisfied.

3. **Get context** — `lattice_get_agent_context`
   (`lattice agent context --task <id> --json`). One call returns the task, the
   manifests it touches, the invariants to preserve, the current code, the
   verifying tests, related decisions, and which skills to load. **No further
   exploration is needed to start.**

4. **Implement** — write the code. Keep the invariants in
   `invariants_to_preserve` true.

5. **Annotate** — for any new symbol, call `lattice_suggest_annotation`
   (`lattice agent suggest-annotation <file> <line> --json`) and apply the
   suggested `@feature` / `@enforces` / `@verifies` annotations.

6. **Edit manifests safely** — if the manifest must change, build a patch and
   call `lattice_preview_patch`. Only `lattice_apply_patch` if the preview's
   `introduced_violations` contains no errors.

7. **Validate** — `lattice_validate` (`lattice validate --json`). If
   `ok` is false, branch on each violation's `next_action.kind`
   (`add_annotation`, `add_invariant`, `strengthen_tests`, …) and fix, then
   re-validate.

8. **For a proposed change** — before promoting a proposal, run
   `lattice_analyze_proposal` and resolve every item under
   `resolutions_required`.

The contract that makes this reliable: every tool returns structured JSON, and
every error carries a `next_action` — the agent branches on fields, never on
prose.

### 7b. Council approval of BRDs

By default a human approves every BRD (`lattice brd approve <id> --by <email>`).
A workspace can hand that step to a **council** of agents. The council reviews
each BRD version, approves it or sends it back, and tells the human it acts for
what it decided and why. The human reads the decision afterwards and can
overturn it.

Only a human turns this on, by pinning a mandate in `lattice/config.yaml`.
Agents never create, widen or renew a mandate.

```yaml
autonomy:
  default_mode: council            # no brd_approval human gate
  mandates:
    - id: m-brd-council
      grants: brd_approve
      delegated_by: owner@example.com
      tier_max: 2                   # highest criterion tier the council may approve
      expires: "2027-04-30"
      council:
        seats: [intent, evidence, risk]
        quorum: unanimous
```

The flow for one BRD version:

1. Each seat reviews the BRD with its own agent identity and votes:
   `lattice brd vote <id> --seat intent --verdict approve --reason "..." --actor agent-intent`.
   - `intent` checks that the BRD says what the features do.
   - `evidence` checks that `lattice rtm` backs it.
   - `risk` checks constraints, tiers and scope.
2. `lattice brd decide <id> --summary "..."` counts the votes.
   - If every seat approves, the BRD is `approved` at once, and the
     `approval` block records the mandate, the human, the votes, the
     summary and the engine's evidence (criteria and scenarios demonstrated).
   - One `changes` vote sends the BRD back.
   - Either way the decision is written to
     `lattice/decisions/brd-council/<id>-v<N>.md` and to the ledger.
3. The human reads the summary, then runs `lattice brd ack <id> --by <email>`.
   Or `lattice brd overturn <id> --by <email> --reason "..."` sets the BRD
   back to `proposed`. The council cannot decide that version again until
   the BRD is revised.

What a council can never do, whatever the mandate says:

- **Approve money or regulation.** A BRD with a tier-3 criterion or a
  regulatory, legal or financial constraint stays with a human.
- **Review its own work.** No seat may be held by the agent that wrote the
  version (`provenance.authored_by`, set from `--actor` by `brd new` and
  `brd from-code`), and no agent may hold two seats.
- **Outlive the version.** A new version voids the approval (`BRD_DRIFT`),
  as for a human approval.

`validate` reports `BRD_COUNCIL_UNREVIEWED` (info) for a decision the human
has not acknowledged yet. It reports `BRD_COUNCIL_INVALID` (error) for a
council approval that no pinned mandate, floor check or unanimous vote
supports, such as a hand-written `approval` block. Switching council mode
off later does not void the approvals it already gave.

### 7c. BRD sources

A BRD can name the documents its intent was first written in: tickets,
roadmap items, help pages, API docs and ADRs. `sources` is optional and comes
after the other BRD fields.

```yaml
sources:
  - kind: ticket               # ticket | roadmap | help_page | api_doc | adr
    ref: WAL-12
    title: Withdrawal fees     # optional
    url: https://tracker.example/WAL-12   # optional
    checked_by: ana@example.com           # optional; set by a human only
    checked_at: "2026-11-10"              # required with checked_by
```

`checked_by` and `checked_at` say that a person compared the BRD with the
source. Only a person sets them. `lattice brd from-code` may draft sources from
the evidence, but it drops any checked mark the model writes. Agents follow the
same rule.

`lattice validate` checks the shape only. It never opens a URL and never sets
or infers a checked mark.

- `BRD_SOURCE_INVALID` (error): the kind is not in the list, the `ref` is
  empty, or the `url` is not an absolute `http(s)` URL.
- `BRD_SOURCE_CHECK_INVALID` (error): `checked_by` and `checked_at` are not
  set together, or `checked_at` is not a `YYYY-MM-DD` date or is in the future.

`GET /api/v2/brds/{id}` lists the sources under `sources.entries`. A checked
source reads `state: checked` with `honesty: claimed`, plus who checked it and
when: it is a person's claim, not verified. Any other source reads
`state: unchecked`.

Sources are part of the review packet. Adding or editing a source changes the
`packet_hash`, so a confirmed decision reads stale and needs a new review. The
change is text-only: it is not in the structural diff. A checked mark is left
out of the hash, so marking a source checked after approval does not void the
approval.

The BRD's `story` field stays `NOT_COMPUTED` (`params.reason: HELD_BY_DECISION`).
The name is reserved for the planned story layer and is not settled yet.

---

## 8. Adopting an existing codebase

Sections 4–7 assume a greenfield project. To adopt Lattice into a codebase
that already exists, use `lattice import` — it discovers the features latent
in the code, drafts their manifests, and attaches the code to them, producing
a graph indistinguishable from a hand-authored one.

The pipeline is five stages, each re-runnable and `--json`-capable:

```sh
lattice import scan     [--scope <dir>]   # discover feature candidates (static, no LLM)
lattice import draft                      # draft a manifest per candidate (LLM, deterministic fallback)
lattice import review   [<candidate-id>]  # review one candidate; --accept / --reject
lattice import verify                     # validate the generated manifests
lattice import inscribe [--inline]        # attach code to the accepted features
lattice import status                     # session progress and coverage
```

1. **Scan** clusters the code into feature candidates by static analysis
   alone and writes `lattice/import/candidates.json`. It is deterministic and
   needs no LLM — useful on its own as a structural map of an unfamiliar repo.
2. **Draft** turns each candidate into a draft manifest. With an LLM
   configured it names and describes the feature from the candidate's
   evidence; with `--no-llm` (or no LLM) it falls back to mechanical names and
   `TODO` prose. Brownfield import works fully air-gapped.
3. **Review** works one candidate at a time. `--accept` writes the draft into
   `lattice/features/` as a real `proposal` manifest; `--reject` records the
   decision. Re-running `scan` reconciles rather than discarding decisions.
4. **Verify** runs the full validation engine over the generated manifests —
   the mechanical fact-check that the import is correct.
5. **Inscribe** attaches code to the accepted features, two ways:
   - the default **sidecar** mode writes `annotation-map.yaml`, which the
     graph builder merges on every `extract` — the full graph with no source
     change, so adoption needs no code-mod PR;
   - `--inline` inserts real annotations into source (preview, then `--apply`
     to write; compile-checked). `lattice import uninscribe` reverses it.

`lattice coverage` reports adoption as three ratios: how much code is
clustered into candidates (discovery), attached to an accepted feature
(documentation), and carries fact-checked invariants (verification).
