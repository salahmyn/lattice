# 0009 — Coverage and reachability

- **Status:** accepted. Phase 1 (coverage ingest, `exercised`, the coverage
  findings and the per-tier level) is implemented; see the user guide,
  section 4f. Phase 2 (call edges from the adapters, roots, reach classes,
  the reachability findings and a static suite-overlap check) is
  implemented; see the user guide, section 4g. See
  [Decisions](#decisions) for both.
  The prototype described below lives on branch
  `rnd/coverage-and-reachability`; its sample workspace moved to
  `examples/coverage-prototype/`, and its package was replaced by
  `pkg/lattice/coverage`.
- **Question:** can Lattice check that a verifier test really exercises
  what it claims to verify? Can it find code that nothing reaches? It
  would read test coverage, and a call graph of every symbol. What would
  that cost?

## Why

A real adopter reached 35/35 criteria demonstrated and 61/61 invariants
enforced and verified. Lattice still could not answer three questions:

1. **Does the verifier run the enforcer?** A `@verifies X:INV-N` test can
   pass without the `@enforces X:INV-N` guard running: the guard is
   mocked, the code returns early, or the test takes another branch. It
   still counts as demonstrated.
2. **How much of the enforcer runs?** A large guard counts as verified
   even when half its branches never run.
3. **What is dead?** Two SQL functions had no caller anywhere (app code,
   edge functions, cron, other SQL) and no test; newer RPCs had replaced
   them. About 24 SQL symbols belonged to no feature. Two e2e suites
   probably overlapped.

Today a test result says only that the test passed. A coverage report
says which code the test ran. Joining the two is the missing step.

## Headline recommendations

1. **Ingest coverage per run, beside results.** A coverage report and a
   JUnit report from the same run share a commit stamp. Read LCOV, istanbul
   JSON, cobertura/clover and Go profiles. Map them onto symbols by line
   extent: every adapter already sets `ir.Symbol.EndLine`. Mapping is
   cheap: 1,787 symbols in about 1 ms (measured).
2. **Add `VERIFIER_MISSES_ENFORCER`, backed by per-test coverage.** It
   fires when no verifier of an invariant runs any of its enforcers. Add a
   top rung, `exercised`, above `demonstrated`, and let
   `validate.require_level` ask for it. Per-test data is cheap in Python
   (one run with dynamic contexts) and Go (about 5 ms per test from a
   prebuilt binary). In JS/TS it costs one runner process per test, so
   collect it for verifier tests only.
3. **Static reachability needs typed edges and real roots.** Do not ship
   name matching. On this repository a name scan gave 14,991 edges for
   1,794 Go symbols, because common method names collide. On the
   adopter's graph, which declares no entry points, it called 119 of 331
   runtime symbols unreached even from tests. Adapters should emit raw
   references (imports, calls, `.rpc('x')`, `.from('t')`). The core
   resolves them, and SCIP refines them when an index exists. Module
   top-level code and framework conventions must count as roots.
4. **SQL dead code is static first.** The adopter's dead SQL functions are
   a static finding: no `.rpc`, no trigger, no caller in SQL, no cron job.
   The postgres adapter already knows the effective definition of every
   function, so the edges are regex-cheap. SQL runtime coverage needs
   `plpgsql_check` (statement and branch) or `track_functions` (call
   counts only). Neither could be run here.
5. **Every new code is opt-in**, as `UNATTRIBUTED_MODULE` is. Each one is
   `off` in `defaultSeverity`, and fires only when its data was ingested.
   A finding says what evidence it rests on (per-test, per-file, suite,
   static), so a weak basis is never mistaken for a strong one.

## The prototype

`pkg/lattice/coverage` (prototype, test-only use) and
`examples/coverage-prototype/` (a sample workspace).

**Why a Go test over a sample workspace.** The test runs the real
pipeline: `workspace.Open`, `extract.Extract` with the configured adapters
(TypeScript and postgres), `graph.Build` and the entry-point loader. So
the symbols, extents, `@enforces` and `@verifies` edges are what `lattice
extract` produces, not fixtures. It runs in `go test ./...`, so it cannot
rot, and it adds nothing to the binary.

**The coverage files are real.** Node 22's built-in test runner wrote
them through `examples/coverage-prototype/regen.sh`, with V8 coverage and
the lcov and junit reporters. Nothing is installed and nothing was written
by hand. Per-test coverage comes from one run per test with
`--test-name-pattern`. The only edit is a `sed` that makes the `SF:` paths
relative. The SQL part has no coverage data; see the postgres section.

The sample is a wallet with a CLI entry point, `src.cli.main`, and two
features:

- `wallet:INV-1` is enforced by `assertSufficientFunds`. Its verifier
  passes `withdraw` a stub guard, so the enforcer never runs.
- `wallet:INV-2` is verified by a test that does run its enforcer.
- `fees:INV-1` is enforced by `computeFee`, which has five paths. Its
  verifier takes one of them.
- `sql.public.debit_v1` was replaced by `sql.public.debit`. App code calls
  `debit` through `client.rpc("debit")`, and nothing calls `debit_v1`.

```sh
go test ./pkg/lattice/coverage -run TestPrototype -v
```

```
warning  COVERAGE_BELOW_FLOOR      fees:INV-1: its verifiers run 53.3% of its enforcer lines (8/15), below the invariant floor of 80%
warning  COVERAGE_BELOW_FLOOR      wallet:INV-1: its verifiers run 0.0% of its enforcer lines (0/4), below the invariant floor of 80%
info     SYMBOL_TEST_ONLY          src.statement.formatStatement is called only from tests: no entry point reaches it
warning  SYMBOL_UNREACHED          sql.public.debit_v1 is reached from no entry point, no other symbol and no test (no coverage data for its file)
info     SYMBOL_UNTESTED           src.wallet.assertSufficientFunds is reachable from an entry point, but no test executed it
info     SYMBOL_UNTESTED           src.wallet.debitInDatabase is reachable from an entry point, but no test executed it
warning  VERIFIER_MISSES_ENFORCER  none of the 1 verifier(s) of wallet:INV-1 executes its enforcer src.wallet.assertSufficientFunds: the test can pass without the guard running
wallet:INV-1  demonstrated verifiers passed, but no verifier's coverage executes an enforcer
wallet:INV-2  exercised    tests.wallet.test.test.rejects-a-non-positive-deposit passed and executed an enforcer
fees:INV-1    exercised    tests.fees.test.test.charges-two-percent-in-usd passed and executed an enforcer
```

The output shows the three asked-for cases:
- **(a)** `VERIFIER_MISSES_ENFORCER wallet:INV-1`. The invariant is
  demonstrated today, and the prototype keeps it at demonstrated.
- **(b)** `SYMBOL_UNREACHED sql.public.debit_v1`. The live `debit` is not
  flagged, because the `.rpc("debit")` edge reaches it.
- **(c)** `COVERAGE_BELOW_FLOOR fees:INV-1`. It is `exercised` and still
  below its floor, because the two checks are separate.

To regenerate the coverage files (Node >= 22, nothing installed):

```sh
examples/coverage-prototype/regen.sh
```

A rerun writes byte-identical LCOV files. Only the test durations in
`junit.xml` change.

Two opt-in tests measure cost. They are skipped unless their environment
is set:

```sh
go test -coverprofile=cover.out ./...
LATTICE_COVERAGE_PROFILE=$PWD/cover.out go test ./pkg/lattice/coverage -run TestMeasureThisRepo -v
LATTICE_COVERAGE_GRAPH=<lattice.json> LATTICE_COVERAGE_ROOT=<code root> \
  go test ./pkg/lattice/coverage -run TestMeasureGraph -v
```

What the prototype does **not** do:
- It has no config parsing, no CLI and no cache file.
- It reads no SQL coverage.
- It does no typed edge resolution. `StaticEdges` is a name scan
  standing in for the proposed adapter references.
- It reads no istanbul, cobertura or coverage.py JSON.

### Lessons from the prototype data

- **A declaration line is not evidence.** V8 marks a function's
  declaration line as run when its module loads. In
  `rejects-an-overdraft.lcov`, `assertSufficientFunds` has line 8 hit and
  `FNDA:0`. A symbol's own coverage must start after its declaration
  line, and a function record (`FN`/`FNDA`) decides "executed" when the
  report has one.
- **A file missing from the report is not zero coverage.** Node's runner
  lists only the files that were loaded, so `src/cli.js` is absent.
  vitest with `coverage.include` lists unloaded files with zero counts
  (verified). So "absent means zero" holds only for files inside the
  report's declared include set. Otherwise the symbol has no data, and
  no finding is raised.
- **Static analysis cannot catch the mock.** In the static graph, the
  stubbed test still reaches the enforcer: `withdraw` names
  `assertSufficientFunds` as its default guard. Only runtime evidence
  shows that the guard did not run.
- **Suite-level coverage catches part of it.** Here no test runs the
  guard at all, so even the union of all runs shows `FNDA:0`
  (`SYMBOL_UNTESTED`). Per-test data is needed only when some other test
  runs the guard but the verifier does not. That case is common in real
  suites, and it is the one that matters.

## Coverage ingestion, per language

The core model is a `Report`: file → line → hits, plus function records
and branch counts where the format has them. A symbol's coverage is the
instrumented lines in `(Line, EndLine]`. Roll-ups:

- **Feature:** the body lines of the feature's runtime symbols.
- **Invariant:** the body lines of its enforcers. They are measured over
  what its verifiers ran when every verifier has per-test data, and over
  the whole suite otherwise.
- **Criterion:** the worst of its invariants, the same rule the RTM
  already uses.

| Adapter | Report | Per-test | Verified here | Notes |
|---|---|---|---|---|
| TS/JS, vitest/Jest | LCOV, istanbul `coverage-final.json` | Not native. One process per test (`-t`), or per file as an approximation | vitest 3.2.4 + coverage-v8: lcov and istanbul JSON, `include` reports unloaded files, a `-t` run takes about 1.2 s | istanbul has `fnMap`/`branchMap`, so branch counts are usable (5 branches on `computeFee`, 1 taken) |
| TS/JS, Node `node --test` | LCOV (`--test-reporter=lcov`) | `--test-name-pattern`, one process per test | Yes: the prototype's data; about 0.15 s per run | Branch records are noise (`BRDA:1,0,0,1` per file); use lines and functions only |
| TS/JS, Playwright | V8 via `page.coverage` (Chromium only), then v8-to-istanbul with source maps; or istanbul-instrumented builds reading `window.__coverage__` | Native per test: each test owns its page | No (browsers not installed) | Highest cost: source maps through the dev server or bundler. Gives e2e reach, which is what overlap and dead-code checks need |
| Deno | Raw V8 JSON per isolate, `deno coverage --lcov` | `--filter <name>`, one process per test | Yes, Deno 2.5.4 from npm: the stubbed test shows `FNDA:0` on the guard | `--junit-path` gives results from the same run |
| Python | coverage.py JSON, LCOV, cobertura | **Native:** `dynamic_context = test_function`, then `coverage json --show-contexts` gives the tests behind each line, in one run | Yes, coverage.py 7.10.6 + pytest 8.4.2 on a toy: the stubbed test is absent from the guard's lines | The cheapest per-test source. Context ids are `module.test_name`, which joins the Python adapter's FQN by suffix, as `results.Lookup` already does |
| PHP | clover, cobertura, PHPUnit XML (`--coverage-xml`) | **Native:** php-code-coverage records the tests behind each line; the PHPUnit XML format lists them per line | No: neither pcov nor Xdebug is installed here | Needs a coverage driver in CI. One run gives per-test data |
| Go | `-coverprofile` (block ranges and counts) | `-run '^TestX$'` per test against a binary built once with `go test -c -cover` | Yes, on this repository (numbers below) | No branch data. Blocks map cleanly onto lines. Avoid `-coverpkg=./...` without merging: it repeats every block once per test binary |
| Postgres | `plpgsql_check` profiler: `plpgsql_coverage_statements/branches()`, `plpgsql_profiler_function_tb()`. `track_functions = all` → `pg_stat_user_functions` call counts | Reset between pgTAP files or tests (`plpgsql_profiler_reset_all()`, `pg_stat_reset()`) and snapshot after each | **No.** No server headers to build `plpgsql_check`, and the sandbox refuses to run a native cluster as the postgres user. PGlite 0.3.7 (Postgres 17.5 in WASM) lists functions in `pg_stat_user_functions`, but `calls` stays 0 | Profiler line numbers are relative to the function body. Add the body's start line in the **effective** definition (the postgres adapter's `Resolve` knows it) to land on the migration's lines. SQL functions that Postgres inlines are never counted by `track_functions`. piggly is unmaintained, so not practical |

Parsers needed, in order of use: LCOV (done in the prototype), Go profile
(done), istanbul JSON, coverage.py JSON with contexts, cobertura/clover,
and the plpgsql_check function tables. LCOV alone covers Node, Deno,
vitest, Jest and coverage.py `lcov`.

## Per-test coverage: proving the link

For each `@verifies` test, Lattice needs the set of symbols that test
executed. It does not need full line data. Store
`per_test: {test FQN: [executed symbol FQNs]}`, reduced at ingest time.
A raw per-test report on a large app is megabytes, because every report
repeats every file.

`VERIFIER_MISSES_ENFORCER` fires on an invariant with enforcers and
verifiers when:
- every verifier has per-test evidence;
- and none executed any enforcer.

A verifier with no evidence makes the check unknown, never a pass. In
that case `VERIFIER_UNPROVEN` (info) names the tests that lack data.

When a runner cannot give per-test data, use the cheapest approximation
and label the finding with it:

| Basis | How | Proves | Cannot prove |
|---|---|---|---|
| `per_test` | one run per test, or native contexts | this verifier ran this enforcer | that it ran the right branch |
| `per_file` | one run per test file | some test in the verifier's file ran the enforcer | which test |
| `suite` | one ordinary coverage run | some test ran the enforcer | that the verifier did; the mock case passes |
| `static` | the call graph reaches the enforcer from the verifier | the verifier *can* reach the enforcer | anything about runtime; the mock case passes, as the prototype shows |

Only `per_test` may lift a row to `exercised`. The other bases can raise
findings: `suite` catches "no test runs this enforcer at all". They can
never promote a row.

Collecting per-test data only for verifier tests is the practical
default. In the adopter checkout, 154 of 405 tests are verifiers, so
this cuts JS per-test cost by about 60%.

## Call graph and reachability

**What exists.** `scip.CallGraph` reconstructs edges with a
"most recent definition before the reference" rule. It ignores `EndLine`,
so a reference in top-level code after a function is credited to that
function. Lattice's own IR carries no references at all: no imports, no
calls. The only flow tracer without SCIP is module proximity.

**Proposal:**

1. Adapters emit raw references:
   - `ir.Module.Imports []Import{Name, From, Line}`;
   - `ir.Symbol.Refs []Ref{Name, Qualifier, Line, Kind}`, where Kind is
     call, new, rpc, table or jsx;
   - top-level code as a pseudo-symbol `<module>.<top>`, because the
     adopter's unreached `.tsx` components are reached from top-level
     router or render code.

   Tree-sitter already parses every one of these files.
2. The core resolves each ref to an FQN: first through the module's
   imports, then the same module, then a unique global name. Anything else
   stays unresolved, and is never guessed. When a SCIP index exists, it
   replaces the core's resolution for that language. Use `EndLine` to bound
   the enclosing definition, which also fixes the existing tracer.
3. Roots:
   - declared and detected entry points;
   - module top-level code of files the workspace marks as roots
     (`coverage.roots`: main files, route tables, exported library API);
   - `cron.schedule` bodies;
   - Deno `serve` handlers.

   A graph with no entry points, like the adopter's, cannot produce
   `SYMBOL_UNREACHED` until roots are declared. Validate should say that
   once, not once per symbol.
4. **SQL edges**, all from text the postgres adapter already scans:
   - `.rpc('name')` in app and edge-function code → `sql.<schema>.name`
     (done in the prototype);
   - `.from('table')` → a table node, which reaches the policies and
     triggers on that table;
   - `create trigger ... execute function f()` → trigger → function;
   - a function body naming `f(` / `perform f(` / `select f(` → function
     → function (done in the prototype);
   - policy `using (...)` / `with check (...)` → the functions it calls;
   - `cron.schedule('job', '<cron>', $$ ... $$)` → a `cron` entry point
     whose handler is the function it calls.

   A dynamic `.rpc(name)` with no literal is reported unresolved (info),
   so it cannot hide a live function.
5. Classification per runtime symbol:
   - `entry`: reachable from a root;
   - `test_only`: reachable only from tests;
   - `unreached`.

   An exported symbol that nothing imports is `UNUSED_EXPORT`.
   Runtime coverage overrides the static answer only upward: a symbol that
   ran is never reported unreached, but a symbol that did not run is still
   reachable.
6. **Suite overlap.** With per-test symbol sets, two suites (or two
   tests) whose sets have a Jaccard index ≥ `coverage.overlap` are
   `SUITE_OVERLAP` (info). That is the adopter's "two e2e suites probably
   overlap". It needs e2e coverage (Playwright above).

## Findings and policy

| Code | Default | Fires when | Needs |
|---|---|---|---|
| `VERIFIER_MISSES_ENFORCER` | off → warning | no verifier of an invariant executed any enforcer | per-test |
| `VERIFIER_UNPROVEN` | off → info | a verifier has no per-test evidence | per-test ingest |
| `COVERAGE_BELOW_FLOOR` | off → warning | a feature, invariant or criterion tier is under its floor | suite (per-test for invariants when present) |
| `SYMBOL_UNREACHED` | off → warning | no root, symbol or test reaches it, and it did not run | static graph (+ coverage) |
| `SYMBOL_TEST_ONLY` | off → info | only tests reach it | static graph |
| `SYMBOL_UNTESTED` | off → info | reachable, but no test executed it | static + suite |
| `UNUSED_EXPORT` | off → info | exported, imported by nothing | static graph |
| `SUITE_OVERLAP` | off → info | two suites' per-test symbol sets overlap above the threshold | per-test |
| `COVERAGE_STALE` | off → warning | the coverage set's commit is not the graph's, or the tree was dirty (0008 F10) | ingest |

"off → X" means the code is `off` in `defaultSeverity` and becomes X when
the workspace enables it. Unattributed SQL symbols are already a
symbol-level attribution question. Under this proposal they would surface
as `SYMBOL_UNREACHED` or `SYMBOL_TEST_ONLY` together with an empty
`feature`, rather than as a new code.

**The `exercised` level.** A criterion is exercised when it is
demonstrated, and for every invariant behind it, at least one verifier's
per-test coverage executed at least one enforcer. A direct-wired
criterion uses its `enforced_by` and `verified_by` lists. A user scenario
is exercised when it is demonstrated and its verifier's coverage executed
the handler of the entry point it names. That turns today's declared
`TouchesEntryPoint` into a checked fact.

- `exercised` sits above `demonstrated` (Severity 8). It does not change
  what demonstrated means: 35/35 demonstrated stays 35/35, and "exercised
  N/35" is a new number beside it.
- `ComputeCoverage` counts it as covered.
- `validate.require_level: exercised` raises `BRD_BELOW_LEVEL` for a
  demonstrated row that is not exercised. When no per-test coverage was
  ingested, the reason says so. Floors are separate findings: a row can be
  exercised and below its floor, as `fees:INV-1` is in the prototype
  (open question 2).

**Allowlist kinds** that fit these codes:
- `generated`: a generated client or types, for `SYMBOL_UNREACHED` and
  `SYMBOL_UNTESTED`;
- `framework`: code reached by reflection, DI or routing conventions the
  graph cannot see;
- `debt`, with `require_adr`: known dead code awaiting removal, or a
  verifier that knowingly stubs its enforcer.

Subjects are FQNs and `feature:INV-N`, so the existing glob matching and
`ALLOWLIST_STALE` work unchanged: an entry for dead code that has been
deleted goes stale.

### Config (proposed, not read by Lattice yet)

```yaml
coverage:
  reports:                          # read by the ingest command when no paths are given
    - path: apps/web/coverage/lcov.info
      format: lcov                  # lcov | istanbul | cobertura | clover | go | coveragepy | plpgsql_check
      root: apps/web                # prefix the report's paths are relative to
      include: ["src/**"]           # files the run covered: absent = zero hits inside this set only
  per_test:
    - dir: apps/web/coverage/per-test   # one report per test, named by its slug
    - contexts: coverage.json           # coverage.py json --show-contexts
    only_verifiers: true                # collect for @verifies tests only
  roots:
    - symbol: "src.main.<top>"
    - file: "src/routes/**"
      reason: route table, reached by the router
  floors:
    feature: 60
    invariant: 80
    tier: {2: 90, 3: 100}           # per BRD criterion tier
  overlap: 0.8
validate:
  require_level: exercised
  severity:
    VERIFIER_MISSES_ENFORCER: error
    COVERAGE_BELOW_FLOOR: warning
    SYMBOL_UNREACHED: warning
  allowlist_kinds:
    generated: {}
    framework: {}
    debt: {require_adr: true}
```

## Granularity

- **Symbol** (recommended base): every adapter has extents, the join is
  exact, and the result is small: one record per symbol.
- **Branch:** only from istanbul, coverage.py arcs, cobertura and
  `plpgsql_check`. Use it as an optional floor metric where present. Never
  require it: Go profiles have none, and Node's LCOV branch records are
  unreliable (measured).
- **Block** (Lattice's catalogue logic paths, rnd/0003): blocks do not
  carry code extents today. Only route blocks have a `wired_by
  file:line`. Block coverage can come later, through the symbols a block
  is anchored to, once blocks have anchors.
- **Leaf** (closures, arrows inside a symbol): they roll up into the
  enclosing symbol, which is what the adapters already record. Splitting
  them out is not worth the noise.

## Cost

M = measured in this session. E = estimated, with the basis given.

| What | Value | |
|---|---|---|
| This repo: Go source files, symbols | 221 files, 1,794 symbols (1,787 runtime) | M |
| `go test ./...`, no coverage / `-coverprofile` / `-coverpkg=./...` | 4.2 s / 10.7 s / 12.3 s (rebuild included) | M |
| Profile size, `-coverprofile` / `-coverpkg=./...` | 0.86 MB, 11,414 blocks / 31 MB, 411,605 lines (36 copies) | M |
| Statement coverage of this repo | 42.5% of 16,707 statements; 37.8% of symbols executed | M |
| Parse a profile / map it onto 1,787 symbols | 8–11 ms / 0.7–1.1 ms | M |
| Parse the Go sources with the adapter (extract cost) | 0.39–0.47 s | M |
| Go per-test, from a binary built with `-c -cover` | 53 tests in 0.25 s (about 5 ms each), 60 KB raw profile per test | M |
| Node runner per-test run (toy) | about 0.15 s per process | M |
| vitest `-t` per-test run (toy) | about 1.2 s per process | M |
| Name-scan edges: this repo / adopter graph | 14,991 edges in 60 ms / 1,523 edges in 40 ms; walk < 1 ms | M |
| Adopter checkout (read-only) | 178 modules, 14.5k lines, 415 symbols, 405 tests (154 verifiers), 72 enforcers (median 7 lines, max 84), no entry points declared | M |
| Adopter SQL (text counts) | 32 `create function` statements (with redefinitions), 9 triggers, 8 policies, 2 `cron.schedule`, 8 distinct `.rpc` names, 5 distinct `.from` tables | M |
| Ingest and map on 1,500 files / 4,000 symbols | < 0.5 s | E, linear from the Go numbers |
| Per-test data kept after reduction | about 1,000 tests × ~100 executed symbols ≈ 100k ids, 2–4 MB JSON. Verifiers only: under 1 MB | E |
| JS per-test runs, 1,000 tests at about 1.2 s | about 20 min serial. Verifiers only (~40%) on 8 workers: about 1 min | E |
| `lattice.json` growth | a `reach` field per symbol: about 80 KB at 4,000 symbols. Edges would be about 1.4 MB, so keep them in the cache | E |

**What changes on disk:**
- `lattice.json`: `GraphSymbol.Reach` (static, deterministic, so it can be
  committed).
- `lattice/.cache/results/coverage.json` (derived, like `results.json`):
  - `commit` and `dirty`;
  - the source reports with their hashes;
  - per symbol: lines, hit and executed;
  - `per_test`: test FQN → executed symbol FQNs.
- `lattice/.cache/callgraph.json`: the resolved edges.
- RTM:
  - `Row.Exercised`, `Row.EnforcerCoverage` and `Row.Evidence`
    (per_test, per_file, suite or none);
  - `StatusExercised`;
  - `BRDSummary.Exercised`;
  - in the text view, a demonstrated row that is not exercised shows
    why.

**The ingest command.** `lattice coverage` already exists (graph
coverage). The least surprising place is
`lattice results ingest junit.xml lcov.info --per-test coverage/per-test`:
one run, one commit stamp, one ledger event. This is open question 1.

## Open questions for the owner

1. Should coverage come in through `lattice results ingest` (results and
   coverage are one run), or through a new command? `lattice coverage` is
   taken.
2. Does `exercised` mean only "a verifier ran an enforcer", or should it
   also require the invariant floor? The prototype keeps them separate.
3. Should Lattice orchestrate per-test runs, as it does mutation runs
   (`adapters.yaml` commands per language)? Or should it only ingest what
   CI produces?
4. Should the per-test default be verifier tests only, or every test?
   SUITE_OVERLAP needs every test.
5. Should the new codes stay `off` until each is enabled, like
   `UNATTRIBUTED_MODULE`? Or should they become warnings as soon as a
   `coverage:` block exists?
6. Static edges: build reference extraction into every adapter
   (tree-sitter, no installs, approximate)? Or rely on SCIP indexers
   (precise, but an install per language, and Go has none wired; 0008 F2)?
   The proposal does both, with the adapters first.
7. SQL runtime coverage: is `plpgsql_check` acceptable as a required
   extension where SQL invariants are tier 2 or 3? Or should
   `track_functions` call counts be enough, and line coverage stay
   optional?
8. What are the roots in a project with no declared entry points? Main
   files, route tables, the exported API? Must the workspace list them
   before `SYMBOL_UNREACHED` runs?
9. Is e2e (Playwright) coverage worth its source-map setup? It is the
   only source of e2e reach and suite overlap.
10. Should a tier-3 criterion need both `exercised` and a mutation score?
    Coverage shows the guard ran. Mutation shows the test notices when the
    guard is wrong.
11. Should the existing SCIP tracer's enclosing-definition rule be bounded
    by `EndLine` now, as a separate fix PR?

## Decisions

The owner answered the questions as follows. Questions 6, 8 and 9 (static
edges, roots, e2e coverage) were answered for phase 2.

1. **Ingest through `lattice results ingest`.** Coverage reports go in
   beside test reports and are told apart by content, as JUnit and TAP
   are: LCOV, Go profiles, istanbul JSON, coverage.py JSON (with
   `--show-contexts` per-test contexts), cobertura and clover XML.
   `--per-test <dir>` reads one report per test, named by its slug. One
   run is one commit stamp and one ledger event. The derived data goes in
   `lattice/.cache/results/coverage.json`. The existing `lattice coverage`
   command keeps its meaning.
2. **`exercised` is separate from floors.** A criterion is exercised when
   it is demonstrated and, for every invariant behind it, a verifier's
   per-test coverage executed an enforcer. Only per-test evidence
   promotes; per-file, suite and static evidence can raise findings but
   never promote. A row can be exercised and below its floor.
3. **Lattice ingests; it does not run tests.** `lattice results tests
   --verifiers` prints each verifier with its runner's filter (Go `-run`,
   Node `--test-name-pattern`, vitest and Jest `-t`, pytest node ids and
   `-k`, Deno `--filter`) and report slug, so CI can run them in parallel.
4. **Per-test scope is verifier tests by default**
   (`coverage.per_test.only_verifiers: true`), with an opt-in for every
   test. A source that holds every test from one run anyway (coverage.py
   contexts) is kept whole.
5. **The new codes are off until enabled.** They are `off` in
   `defaultSeverity`. A `coverage:` block in `config.yaml` switches on
   `VERIFIER_MISSES_ENFORCER` (warning), `COVERAGE_STALE` (warning) and
   `VERIFIER_UNPROVEN` (info); each declared floor switches on
   `COVERAGE_BELOW_FLOOR` (warning). Explicit `validate.severity` entries
   win. A finding states its evidence basis and fires only on ingested data.
6. **Call edges: the adapters first, cautious resolution, SCIP when
   present.** The TS/JS, Go, Python and Postgres adapters emit raw
   references from their tree-sitter (or SQL) parse: `ir.Module.Imports`
   (name, from, member, re-export, line), `ir.Symbol.Refs` (name,
   qualifier, line, kind: call, new, rpc, table, jsx, and ref for a value
   named without a call), and top-level code as a `<module>.<top>` node.
   The core (`pkg/lattice/reach`) resolves each reference through the
   module's imports, then its own scope, then a member name unique in the
   language. Anything else stays unresolved, never guessed, and is counted
   (ambiguous, missing, dynamic). When a SCIP index exists for a language
   it replaces the core's resolution for that language, each reference
   credited to its innermost definition bounded by `enclosing_range`. The
   resolved edges go to `lattice/.cache/callgraph.json`. SQL edges came
   first: `.rpc('fn')`, `.from('table')` to the table's policies and
   triggers, trigger to function, function-body calls, policy expressions,
   and `cron.schedule` bodies as cron roots; a computed `.rpc(x)` is
   `RPC_UNRESOLVED` (info). PHP is not done: its symbols are `unknown`.
7. **Postgres runtime data has no hard requirement.** Function call counts
   from `pg_stat_user_functions` (`track_functions = 'pl'` or `'all'`) are
   the baseline, mapped to `sql.<schema>.<fn>` symbols as per-test or
   per-file "executed" evidence. `plpgsql_check` profiler data is optional
   and not read in phase 1. SQL functions that Postgres inlines are never
   counted, so such enforcers cannot reach `exercised`.
8. **Roots: a small automatic set plus declared ones, nothing guessed in
   between.** Automatic roots are the handlers of declared and detected
   entry points; entry files named by build configuration (`package.json`
   `main`, `module`, `exports` and `bin`, and an `index.html`'s `<script
   type="module" src>`); Deno `serve(...)` handlers; `cron.schedule`
   bodies; SQL triggers and policies; Go's `func main` in package main.
   Declared roots are `coverage.roots: [{symbol: <fqn or glob>, reason},
   {file: <glob>, reason}]`. With no roots at all, one
   `REACHABILITY_NO_ROOTS` (info) replaces every per-symbol finding. Each
   runtime symbol's class (`entry`, `test_only`, `unreached`, `unknown`)
   is `GraphSymbol.Reach` in `lattice.json`. Runtime coverage only moves a
   symbol up. `SYMBOL_UNREACHED` (warning), `SYMBOL_TEST_ONLY`,
   `SYMBOL_UNTESTED` and `UNUSED_EXPORT` (info) are off until each is
   enabled in `validate.severity`; a `coverage:` block does not turn them
   on. With roots, `FEATURE_UNREACHED` counts a feature as reached when a
   root reaches any of its symbols.
9. **End-to-end (Playwright) coverage is deferred.** The cheap static check
   ships instead: `SUITE_OVERLAP` (info, off until enabled) compares each
   pair of suites' `@verifies` targets and the entry points in
   `verified_by` of the scenarios their tests verify, and fires at a
   Jaccard index at or above `coverage.overlap` (default 0.8). Suites come
   from `coverage.suites: {name: [globs]}`, or default to the top-level
   test directories. Per-test e2e coverage would make it precise.
10. **Tier 3 needs both.** `validate.require_level` takes a per-tier form
    (`{default: demonstrated, tier: {3: exercised}}`), and the tier-2+
    `MUTATION_REQUIRED_TIER` gate stays as it is, so a tier-3 criterion
    needs `exercised` (when configured) and mutation evidence. The
    allowlist kinds `generated`, `framework` and `debt` (with
    `require_adr`) are configuration the workspace writes, not built in.
11. **Yes, as a separate fix.** Bounding the SCIP tracer's
    enclosing-definition rule by the definition's extent landed on master
    on its own (#42, "fix(scip): bound a reference's enclosing definition
    by its extent"). Phase 1 does not touch the tracer, so the "What
    exists" paragraph under *Call graph and reachability* describes the
    tracer before that fix.

### Where phase 1 differs from the proposal above

- **Config shape.** `per_test` is a mapping, so `only_verifiers` and the
  report directories sit side by side:
  `per_test: {only_verifiers: true, dirs: [{dir: …, basis: per_test|per_file}]}`.
  A coverage.py report with contexts goes under `reports:`, since it is
  one file that holds the suite and every test. `roots` and `overlap`
  are phase 2 and not read.
- **The dirty flag** ignores untracked files (the coverage reports
  themselves are usually untracked) and Lattice's own runtime state
  (`lattice/.ledger/`, `lattice/.cache/`). It looks only at the directory
  holding `lattice/`.
- **Per-file evidence** comes from `--per-file <dir>` (or a `dirs` entry
  with `basis: per_file`): one report per test file, named by the file's
  slug. It can raise `VERIFIER_MISSES_ENFORCER` but never promotes.
- **Scenarios** follow the proposal: a user scenario is exercised when it is
  demonstrated and a verifier's per-test run executed the handler of an
  entry point it names. A scenario has no tier, so it uses the default
  level.
- **The Postgres export** is a JSON or CSV file the user's CI writes (shape
  in the user guide). Its per-test and per-file runs are kept whole, like
  coverage.py contexts. No Postgres server was available, so the format is
  tested with hand-written fixtures only.

### Where phase 2 differs from the proposal above

- **Another reference kind, `ref`.** A function named without being called
  (a callback, a default argument `guard = assertSufficientFunds`, a Go
  method value `p.handleTop`) is a reference. Without it, the prototype's
  `assertSufficientFunds` and most of Lattice's own Go parser read as
  unreached.
- **Imports carry what they bind.** `ir.Import` also has `member` (the
  export imported), `export` (a re-export's name) and `module` (the
  workspace namespace the adapter resolved `from` to, once it has seen
  every file). Each adapter resolves its own specifiers in its `Resolve`
  hook: TS paths and `@/` aliases, Go import paths against an inferred
  module path, Python relative and `src/`-layout imports. The core only
  follows bindings and re-exports.
- **"A globally unique name" applies to members.** A bare name in TS, Go
  or Python is lexically scoped: it is an import or a name of its own
  module, never a unique name elsewhere. The unique-name step resolves a
  method called on a value of unknown type, within one language, and a
  production caller never resolves to a test's member. A value whose type
  the Go adapter read (a parameter, `var x T`, `x := &T{}`) resolves by
  that type instead, and the result of an outside call
  (`time.Now().Add(d)`) never falls back to a workspace method.
- **Ambiguous calls and Rapid Type Analysis.** An ambiguous reference
  (typically an interface call: `ad.Parse(...)` with five adapters that
  have `Parse`) stays unresolved in the call graph and counted. For the
  classes only, it reaches the candidates whose type reached code builds
  or names, as Go's own `deadcode` tool does (RTA). A candidate whose type
  nothing reached is `unknown`, never `unreached`: no dead-code finding
  rests on a guess. Without RTA, 352 of this repository's 2,173 runtime
  symbols were `unknown` (90 interface methods and the 262 symbols only
  they call, most of the adapters' parsers); with it, 3 are.
  `FEATURE_UNREACHED` counts only `entry` symbols as reached.
- **Conventions as edges.** Go methods the standard library calls through
  an interface (`String`, `Error`, `MarshalJSON`, `UnmarshalYAML`,
  `Len/Less/Swap`, ...) are reached with their type; a Python class reaches
  its dunder methods; `new C()` reaches C's constructor. Python's `if
  __name__ == "__main__":` block is a root, like Go's `func main`.
- **`FEATURE_UNREACHED` with roots runs without entry points.** A project
  whose only roots are a `func main` or SQL triggers now gets the check.
- **Default suites** are the top-level directories named `tests`, `test`,
  `e2e`, `__tests__`, `spec`, `specs`, `cypress` or `playwright`; tests
  elsewhere belong to none. That is clean for the usual e2e layouts and
  says nothing about colocated unit tests, which `coverage.suites` can
  group.

### Phase 2 measured on this repository

Lattice's own workspace: Go and the TypeScript MCP server, 360 modules,
2,173 runtime symbols. Roots (49): the 46 CLI entry points, `func main` and
its package, and `lattice-mcp`'s `package.json` `bin` (mapped from
`dist/index.js` to `src/index.ts`). No `coverage.roots` declared.

| What | Value | |
|---|---|---|
| `lattice extract`, before / after (median of 9 runs, same tree) | 376 ms / 507 ms | M |
| Where the added time goes | the adapters' reference walk (tree-sitter calls); building, rooting and classifying the graph take about 30 ms | M |
| References read | 28,764 (Go 28,641, TypeScript 123) | M |
| Resolved | 11,949, into 9,630 distinct edges (the name scan gave 14,991) | M |
| Unresolved | 569, all ambiguous; 0 missing, 0 dynamic | M |
| External (standard library, dependencies, locals) | 16,246 | M |
| Resolved by: own scope / import / declared type / receiver / unique member / structure / adapter target | 4,877 / 2,971 / 470 / 410 / 317 / 557 / 18 (edges) | M |
| Classes, as committed | entry 2,129, test_only 10, unreached 31, unknown 3 | M |
| Classes without Rapid Type Analysis | entry 1,785, test_only 5, unreached 31, unknown 352 | M |
| Classes with `coverage.roots: [{file: pkg/lattice/lattice.go}]` | entry 2,143, test_only 10, unreached 20, unknown 0 | M |
| `lattice.json` | 1,439,833 → 1,492,994 bytes (+53 KB, +3.7%: one `reach` per runtime symbol) | M |
| `lattice/.cache/callgraph.json` | 1.76 MB (not committed) | M |

The unreached list, checked by hand:

- **The Go library API** (`pkg/lattice/lattice.go`: `Open` and the
  `Lattice` methods, 11 symbols): public API that nothing in the workspace
  calls. Not dead: declare it in `coverage.roots`, which leaves 20.
- **Really dead, 20 symbols**: each is named only by its own declaration
  anywhere in the repository. `agentic.Capabilities.WithProvider`,
  `analyze.Analyzer.WithEmbedder`, `analyze.ImpactReport.HasConflicts`,
  `brd.ByStatus`, `config.Autonomy.MayAdvanceMeaning`,
  `entrypoints.LoadStatus`, `entrypoints.ResetForTest`,
  `mutation.ParseStrykerJSON`, `mutation.AggregatePerInvariant`,
  `mutation.FileScore` and its `Score`, `mutation.strykerReport`,
  `schema.InvariantRefs.Has`, `ui.Server.configRelPath`,
  `validate.HasErrors`, and `workspace.Workspace.AdaptersPath`,
  `EmbeddingsDir`, `MCPPath`, `SchemasDir`, `SkillsDir`. Some are exported
  and may be meant as API; none is called.
- **Test-only, 10**: each adapter's `CanParse` (the registry dispatches by
  extension, so only tests call it), `brd.LoadVotes`, `graph.Load`,
  `ledger.Entry.From`, `validate.applySeverity` and `scip.NewCorpus`.

Resolution gaps found and fixed on the way: method values and calls on a
typed local (`p.walkList(root, p.handleTop)`: the whole Go parser read as
unreached), calls on the result of an outside call
(`time.Now().Add(d)` wired to a workspace `Add`), a library file's
exported methods as roots, and interface dispatch (RTA above). What
remains: a Go local's type is read from its declaration only, never from
a function's result type, so a method on such a value resolves by unique
name or stays ambiguous.
