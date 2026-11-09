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

Check optional tooling (SCIP indexers, mutation runners, LLM keys):

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

### Step 6 — Commit

The whole `lattice/` directory (manifests, `lattice.json`, config, skills) is
committed to git; `lattice/.cache/` is gitignored. Wire `lattice validate`
into CI with the templates in `ci-templates/`.

> **Parallel branches.** `lattice.json` is rebuilt by `lattice extract` from
> the source and manifests on every run, so teams (or agent fleets) working on
> many branches at once can gitignore it and treat it as build output. The
> ledger (`lattice/.ledger/ledger.jsonl`) is append-only evidence: `init` adds
> `merge=union` for it to `.gitattributes`, so a merge keeps both sides'
> lines, and a check run that repeats the previous outcome is not appended
> again. Existing workspaces can add the same line to `.gitattributes`.

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
names a non-existent feature), and `HANDLER_MISSING` (handler FQN not
in the IR).

---

## 4c. The web UI (v0.4.0)

`lattice serve` boots a local HTTP server that consumes the same
KnowledgeGraph the CLI produces. Single binary, no Node, no build step.

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

Findings. Each one is off until the workspace has a `coverage:` block (or
sets its severity), fires only when its data was ingested, and states the
evidence basis it rests on:

| Code | With `coverage:` | Fires when |
|---|---|---|
| `VERIFIER_MISSES_ENFORCER` | warning | every verifier of an invariant has evidence (its own run, its file's run, or a suite that never ran the enforcer) and none executed an enforcer |
| `VERIFIER_UNPROVEN` | info | per-test coverage was ingested, and a verifier has none; the finding names the tests |
| `COVERAGE_STALE` | warning | the coverage was ingested at another commit than the graph's, or from a dirty tree |
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
| Go | imports; calls, composite literals, types, function values; a method's receiver; a local's declared type (`func f(kg schema.KnowledgeGraph)`, `var x T`, `x := &T{}`) | an import path is local when it is `<module>/<dir>`; the module path is inferred from the imports, not read from `go.mod` |
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
judgments. `lattice serve` shows it at `/map`, and `lattice export map
out.html` writes the same page as one file that works offline. The page
lays out with ELK from a pinned, SRI-checked CDN copy; without the network
it falls back to a simple grid.

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
into), `init` (the proposal), `flow`, `lanes` and `hide` (hidden kinds).
In `lattice serve`, the pages of a feature, a BRD and an entry point link
to `/map#tab=focus&id=<block>` when the graph has a block for them:
`feat:<id>`, `ep:<id>`, and for a BRD its features' blocks and the blocks
it names.

Keys: `/` search, `Esc` close, `f` fit; in Focus also `[` `]` back and
forward, `1` `2` `3` hops and `c` system context.

### Checking it in a browser

`scripts/blockmap-smoke.js` drives an exported map in Chromium with
Playwright and fails on any console error. `go test ./internal/cli -run
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
lattice break --feature cart --json
```

For each enforcer it makes a temporary detached `git worktree` at HEAD,
runs the verifiers unbroken (the **baseline**), applies the break, runs
them again and removes the worktree. Your working tree is never edited. A
tree with uncommitted changes is refused; `--allow-dirty` copies the changes
into the worktree and records `dirty: true`.

| Outcome | Meaning |
|---|---|
| `killed` | The verifiers passed before the break and failed after it. |
| `survived` | They passed both times: the test cannot tell the code is broken. |
| `unknown` | Anything else. It is never a pass and never a fail. |

`unknown` covers: no generated break for the language ("add a script"), a
break that does not compile, a baseline that fails or executes no test, a
timeout (`--timeout`, default 120s, per test run), no runner configured, and
SQL without a script.

Only an explicit build marker reads as "does not compile": Go's `[build
failed]` or `[setup failed]`, an esbuild or transform `file:line:col: ERROR:`
line, Deno's `error: TS...`, `error: Module not found` or `Check file`, and
pytest's collection and import errors. A runtime failure with a stack trace is
a failing test, so it reads `killed` when the baseline passed. The reason
names the failing tests, else the first error line.

**The generated break** is an early return at the top of the enforcer's
body, with the rest left in place so imports and variables stay used. Go
returns zero values (`if true { return }` for named or no results);
TypeScript returns `undefined as any` (plain `undefined` in JavaScript),
for function declarations, methods, and functions or arrows with a block
body; Python gets `return None`. An arrow with an expression body,
`(a) => expr`, has its body wrapped as `(true ? (undefined as any) : (expr))`
(no `as any` in JavaScript), so `expr` stays referenced. Constructors, setters,
classes and consts that are not functions ("not a function") read unknown.

**A scripted break** is a patch at `lattice/breaks/<feature>/<INV>.patch`
(made with `git diff`), applied with `git apply` in the worktree. It
replaces the generated break for that promise, and is the way to break
SQL or anything else with no generator.

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
  where they appear. `dir` (optional) is the working directory, and file paths
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
`-v` pass lines), `not_run_verifiers` (no runner, or the filter or the runner's
config left them out) and `failed_tests` (the tests that failed after the break).

**Cache.** An earlier killed or survived outcome is reused when the
enforcer's source, the verifiers' source, the method, the script and the
runner configuration (the resolved runner commands, the setup commands and the
generator version) all match, so a config change runs again; `unknown` is never
reused, so a timeout is always tried again.

`--promise` and `--feature` print only the runs they selected; `breaks.json`
still keeps the rest of the commit's runs.

The result is `lattice/.cache/results/breaks.json` (`lattice.breaks/1`,
derived and gitignored). A promise with several enforcers has one run each.
Nothing blocks on it yet: it is evidence for `lattice audit`.
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
```

The same rows are on the `/audit` page of `lattice serve`, filterable by class
and feature; each fake opens to its verifiers, enforcers, the evidence behind
the class and any reader's note. `GET /api/v1/audit` returns the JSON.

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
approval record behind the BRD criteria that map to the invariant:
`human`, `agent` (an agent actor, or a council not yet acknowledged), `draft`,
`drifted`, or `none`. It is a fact about who signed which version, not proof
the intent is right.

### Break results

`breaks.json` (`lattice/.cache/results/`, written by `lattice break`) is read
as a file. A run for another commit, or of a schema the audit does not know, is
set aside and listed under `inputs.breaks.ignored`. When an invariant has
several enforcers, a survived break on any of them reads `passes_when_broken`,
but only when no verifier of the promise was left unrun: a run with entries in
`not_run_verifiers` reads `unknown` / `partial_run`, with a reason naming those
verifiers. `break_tested` needs at least one enforcer killed and none survived
with all verifiers run. A row's evidence lists the outcome of each enforcer, with
its unrun verifiers and failed tests. A `breaks.json` from before these fields
reads as it always did.

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
| `BROKEN` | Something fell: a promise's class dropped to a known-bad class (`fake`, `unenforced`, `unverified`, `not_demonstrated`) against base; an enforcer or verifier was removed; a break killed at base survives at head; the suite is red; or the statement, tier or mapping of a confirmed promise was edited (the edit voids the confirmation, and the row says to re-confirm it) | Never |
| `UNPROVEN` | Nothing fell, but a touched promise is unconfirmed, not exercised, not break-tested where its tier needs it, or unknown, or touched lines are untraced. It is never drawn as a pass: no check mark, and the comment says so | Only if the merge rule holds |
| `ASSURED` | Every touched line is traced, every touched promise has the signals its tier needs, and nothing fell | Yes |

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
criterion's `enforced_by`), or an entry point of its feature reaches it. A
file-level `@module-feature` tag alone never counts. The rest goes in the blind
spot, with the promises a human marked out of reach, and keeps the verdict at
`UNPROVEN`. Lines are counted by the touched symbol's extent, an upper bound.

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
similar, else `local`). With `--ci`, evidence whose commit is not `HEAD` or whose
`ci_run` is `local` is set aside and reads `unknown` (`EVIDENCE_NOT_CI`). Only
CI's verdict counts; a local run is advice.

### Exit codes

`0` for `ASSURED` or an allowed `UNPROVEN`; `1` for `BROKEN` or a disallowed
`UNPROVEN`; `2` for a usage error (no `--base`, a base that is not a commit).

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

`backprop` is a gate, so **every run records a check run in the ledger**
(`lattice/.ledger/ledger.jsonl`), as `validate` does: one entry for "no doc
impact", or one per affected criterion. A run whose actor and outcome repeat
the latest entry for the same unit adds nothing. This is intended, and it
means running `backprop` in CI on every pull request changes a tracked file.

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
