# Changelog

All notable changes to Lattice are documented in this file.

## [0.23.0] — 2026-11-21

Four checks for promises that pass every gate and still do not hold. One finds an enforcer that a
client can call directly. One finds a second writer of a promise's columns. One shows, line by line,
what each promise's own verifiers run. One flags a promise whose breaks were killed only by tests
that never entered through a way in. Every new finding is info and always on, so a `fail_on: warning`
gate stays green after upgrading. Anything Lattice cannot resolve reads as unknown, never as safe.

### Added

- **Who can call a SQL enforcer** (user guide 4s).
  - **What the adapter reads.** The postgres adapter now reads `GRANT`/`REVOKE` on functions
    (including `ALL … IN SCHEMA`), `ALTER DEFAULT PRIVILEGES`, `SECURITY DEFINER`, and role
    membership (`GRANT role TO role`, `CREATE ROLE … IN ROLE | ROLE | USER | ADMIN`, `[NO]INHERIT`).
    It folds them in migration order, and each one cites the statement that decided it.
  - **Config.** Declare roles in `workspace.yaml`:
    - `sql.api_roles`: the roles a client reaches the database as;
    - `sql.default_execute`: the roles the platform grants `EXECUTE` on new functions. Unset means
      unknown.
  - **`ENFORCER_EXPOSED`** (info): an API role can execute an enforcer, and no verified `security`
    case naming it exists.
  - **`EXPOSURE_UNKNOWN`** (info): a role's state cannot be told from the migrations, or no API
    roles are declared.
- **Who else writes the columns a promise is about** (4t).
  - **What the adapter records.** The writes of each SQL symbol: `INSERT`, including `ON CONFLICT DO
    UPDATE`; `UPDATE`; `DELETE`; `MERGE`; and trigger `NEW.col` assignments. It also records table
    grants and row-level security.
  - **Config.** `sql.default_write` holds the platform's default table privileges. With RLS on, a
    role writes only through a permissive policy that reaches it.
  - **`UNGUARDED_WRITER`** (info): a writer of the columns an invariant's enforcers write that is
    not an enforcer of it and not under an enforcing trigger or policy.
  - **`WRITERS_UNKNOWN`** (info): dynamic SQL, a view, a foreign-key action or an unresolved role
    leaves a table's writers incomplete.
  - **`lattice writers <table>[.<column>]`** lists each writer and what guards it. `--json` gives
    `lattice.writers/1`.
- **Lines: what is each block for, and do its verifiers reach it?** (4u)
  - **`lattice break --lines`** records which lines each promise's own verifiers run, by turning
    coverage on for the baseline runs.
    - Runner commands take `{coverage}`. Go's built-in command gets `-coverprofile`.
    - Data is kept per tree in `verifier-lines.json`.
  - **`lattice audit --lines`** gives each block an intent: `enforces`, `serves`, `shared` or
    `none`. It gives each instrumented line a level: `checked`, `run`, `run_shared`, `other`,
    `not_run` or `unknown`. Verified lines are always printed beside the unknown count.
    - **Early-return kills.** A killed early return checks a block as a whole, never a line.
    - **SQL bodies** read unknown at line level.
  - **`coverage.floors.verified_lines`** opts in to `VERIFIED_LINES_BELOW_FLOOR`.
- **Entry-point proof** (4v).
  - **What is checked.** For each break-tested promise, the audit asks whether a verifier that
    killed a break ran an entry point's handler that reaches the enforcer.
  - **What counts as proof.** Per-test coverage of the handler itself, or line data from an unshared
    group whose tests all killed the break. A static path alone is never proof.
  - **`BREAK_NO_ENTRYPOINT`** (info): every killer's complete call graph reaches no way in.
  - **Audit output.** `lattice audit` prints an `entry-point proof:` line and a note on each row.
    `lattice.audit/1` gains `rows[].entrypoint` and `summary.entrypoint`.

### Behaviour changes to plan for

- **New info findings on workspaces with SQL enforcers.** Expect `EXPOSURE_UNKNOWN` until
  `sql.api_roles` is declared, and `WRITERS_UNKNOWN` until `sql.default_write` is declared. They are
  info and change no gate. Raise any of them in `validate.severity` to gate on it.
- **`lattice validate` computes the audit** when `breaks.json` is present.
- **Plain `lattice break` is unchanged:** commands, cache keys and cached outcomes are the same as
  in 0.22.3.

### Fixed

- **The graph determinism test no longer depends on the clock.** It built twice with the wall-clock
  generation stamp, so two builds a second apart differed in `generated_at` alone.

## [0.22.3] — 2026-11-21

A cached break outcome could be handed to the wrong enforcer.

### Fixed

- **Two enforcers with the same source never share a cached outcome.** The break cache key held
  the enforcer's source hash, but not which enforcer the run was for.
  - **What went wrong.** One promise kept by the same code in two modules, such as a client copy and
    a shared copy, has two enforcers with equal keys. A second run on the same tree could then give
    one copy the other's cached outcome: a kill whose reason quoted a test that cannot import it.
  - **The fix.** The key now names the enforcer.
  - **Cached outcomes.** `GeneratorVersion` is 6, so outcomes cached under such a collision are
    dropped once.

## [0.22.2] — 2026-11-21

Two ways a result could read as a pass that was not earned are fixed. A type error that a runner
printed in colour read as a verifier catching the break. A member call matched by name joined code
the caller never loads.

### Fixed

- **A coloured type error reads "does not compile", not `killed`.**
  - **What went wrong.** Deno colours its output even into a pipe, so a type-check error such as
    `TS18046 [ERROR]` reached the classifier wrapped in escape codes. The build marker did not match,
    and the run read as a failing test.
  - **Now.** Terminal escape sequences are stripped before build markers are matched. Runner
    processes are started with `NO_COLOR=1` and `FORCE_COLOR=0` unless the environment already sets
    them.
  - **Cached outcomes.** `GeneratorVersion` is now 5, so cached outcomes are recomputed once.
- **A member call resolves only into code loaded with the caller.**
  - **What went wrong.** A call such as `client.messages.create()` on an SDK object resolved by name
    to the only `create` in the language. That could be a method in another edge function the caller
    never imports, which made an enforcer look reached when it was not.
  - **The rule.** For TypeScript, JavaScript and Python, a candidate must be in a module that some
    module transitively imports together with the caller's.
  - **What still resolves.** Dependency injection keeps its edge. A test's imports join modules only
    for test callers.
  - **No candidate left.** The call is external.

### Behaviour changes to plan for

- **Reach can drop on TypeScript and Python code** where it was granted only by a by-name guess. Code
  that no entry module imports alongside its caller loses those edges. This shows up as more
  `unreached` enforcers, which were not reached before either.
- **Break runs recompute their cached outcomes** once after upgrading.

## [0.22.1] — 2026-11-21

`lattice break --edges` now breaks a client's call to an edge function. A cron job that reads its
target URL from Supabase Vault joins the function it calls. A break whose verifiers include a SQL
swap runner still runs its other tests.

### Fixed

- **An edge function and the declared route that covers it are one http end.**
  - **What went wrong.** Both matched a `functions.invoke` call, so the reference read as ambiguous,
    and `lattice break --edges` skipped every client-to-function hop.
  - **What still reads ambiguous.** Different handlers on one path.
- **A runner group that needs the SQL swap no longer blocks the others.**
  - **Non-SQL and edge breaks.** Only the group whose command uses `{patched_sql}` or
    `{original_sql}` is left out, both from the run and from its baseline. Its verifiers read not
    run, and the other groups run.
  - **The outcome.** Killed when a group that ran fails. Otherwise `unknown` with a `partial:`
    reason, never survived.
- **A cron job that runs a function posting to an edge function** now reaches that function's
  handler. A declared cron entry point on its schedule covers it.

### Added

- **`http.secrets` in `lattice/workspace.yaml`** maps a Vault secret name to a route path.
  - **What joins.** A SQL function that reads a URL whole from Vault and posts to it through
    `net.http_*` joins that route. The URL may be the plain variable, or the variable with a literal
    suffix (`v || '/suffix'`). The read can be written as
    `select decrypted_secret into v from vault.decrypted_secrets where name = '<lit>'`, or with
    `:=`.
  - **What stays `dynamic`.** An undeclared secret, and `HTTP_CALL_DYNAMIC` names the secret to
    declare.
- **An object swap for a named CHECK inside `CREATE TABLE`.**
  - **Patched.** `ALTER TABLE … DROP CONSTRAINT IF EXISTS c, ADD CONSTRAINT c CHECK (true OR (…)) NOT
    VALID`.
  - **Restored.** The original expression, then `VALIDATE CONSTRAINT c`.
  - **When it is not offered.** Any later statement that touches the constraint makes the swap
    unavailable.

## [0.22.0] — 2026-11-21

Lattice now follows a promise through queues and events. It can break the hops between layers as
well as the code at their ends, and it treats named CHECK constraints as SQL enforcers. Break runs
that use the SQL object swap heal a database a failed restore left wrong.

### Added

- **Channels for async hops.** Queues, `NOTIFY` channels and events are channel nodes,
  `chan.<kind>.<name>`. A publisher reaches the channel, and the channel reaches every consumer that
  names it with a literal.
  - **Out ends.** SQL `pg_notify`, `NOTIFY` and `pgmq.send`. TypeScript `rpc('pgmq_send')`, and
    BullMQ queues bound in the same module.
  - **In ends.** `queue` and `event_consumer` entry points, `pgmq.read` and `pop`, and BullMQ
    workers.
  - **Declared wrappers.** List your own queue functions under `channels:` in
    `lattice/workspace.yaml`.
  - **Async reach is reported apart.** It shows as `async_hops` and never changes a symbol's `reach`
    or the audit's live signal.
  - **`lattice slice`.** Paths can cross channels, as `publish` and `consume` hops.
  - **Findings (info).** `CHANNEL_NO_CONSUMER`, `CHANNEL_NO_PUBLISHER` and `CHANNEL_DYNAMIC`.
  - User guide section 4r.
- **Edge breaks: `lattice break --edges`** (or `break.edges: true`).
  - **What it breaks.** Each resolved hop on a promise's slice paths is broken at its out end by
    inserting `-lattice-break` into the literal: an `http` path, a `functions.invoke` name, an `rpc`
    or `table` name, or a publisher's channel. Then the promise's verifiers run.
  - **What it never breaks.** An unknown hop, a hop with no recorded literal, or a computed key is
    skipped as `unknown` with a reason. Nothing is guessed.
  - **Results.** A survived edge break carries `EDGE_BREAK_SURVIVED` (info). `lattice slice` marks a
    hop `break_tested` (`kind*`) only when its edge break was killed.
  - **Off by default.**
- **Named CHECK constraints are SQL enforcers.** `CONSTRAINT n CHECK (…)` in `CREATE TABLE` and
  `ALTER TABLE … ADD CONSTRAINT n CHECK (…)` are `sql.check.<schema>.<table>.<name>`, tagged by the
  comment above their line.
  - **The break.** `CHECK (true OR (expr))`, on the effective statement.
  - **The swap.** `DROP CONSTRAINT IF EXISTS` plus the `ADD CONSTRAINT`. A CHECK inside
    `CREATE TABLE` has no swap.
- **`net.http_post` and declared origins.** `net.http_post`, `net.http_get` and `net.http_request`
  in SQL are http out ends.
  - **New origin forms.** `http.origins` accepts `env:NAME` (`import.meta.env.NAME`,
    `process.env.NAME`, `Deno.env.get('NAME')`) and `setting:name` (`current_setting('name')`).
  - **When it joins.** A URL that is exactly one declared origin plus a literal path joins routes
    and edge functions. Anything else stays `dynamic`.
- **`break.restore`.** Commands run after every run that used `{patched_sql}` or `{original_sql}`.
  If one fails, `break.setup` runs again at once.

### Fixed

- **An awaited call with type arguments is no longer dropped.** `await invoke<T>('x')` (and
  likewise `fetch`, `axios`, `.rpc`, `.from` and channel calls) now joins its route or object.
- **Runtime-root proposals no longer duplicate declared entry points.**
  - **Serve roots.** A declared `http_route` or `webhook` covers a serve root by its
    `/functions/v1/<name>` path, or by the handler the serve call builds.
  - **Cron roots.** A declared `cron` covers a cron root when it has the same schedule and its
    handler the job reaches, including through one `http` hop.
- **A "did not apply" reason ends at a literal `\n`** from a CLI's JSON output.
- **A failed SQL swap restore no longer poisons later runs.**
  - **The heal.** When a baseline fails and a swap has run since the last setup, `break.setup` runs
    again and that baseline is retried once. A second failure reads as before.
  - **The outcome stands.** A run whose verifiers already ran keeps its outcome.
  - **Summary.** It shows `setup: 1 + N after restore`. The JSON summary gains `setup_runs`.

### Behaviour changes to plan for

- **New info findings.** On a codebase with queues or notify channels, `CHANNEL_*` info findings can
  appear.
- **Longer runs with `--edges`.** Edge breaks add one run per hop, up to 20 per promise.

## [0.21.0] — 2026-11-21

Two `lattice break` results that could read as a pass without one are fixed: a cached outcome
reused on another tree, and a SQL policy broken where a later migration overwrote the break. A
client's HTTP call now joins the route that serves it, and a slice shows which hops a test really
ran.

### Fixed

- **A cached break outcome is reused only on the tree it was measured on.**
  - **What went wrong.** A commit that changed a file the runner reads, such as a SQL migration
    outside the enforcer, used to print `killed (cached)` even though every baseline then failed.
  - **Default.** An outcome is now reused only on the same tree.
  - **Opt-in across commits.** A runner can declare `break.runners[].inputs` (globs of the files it
    reads) to reuse across commits that leave those files unchanged.
  - **Failed baselines.** A cached outcome is never reused over a baseline that failed at this tree.
  - **Contract.** `breaks.json` gains an optional `inputs_hash`.
- **A SQL break edits the statement that sets the object's effective definition.**
  - **What went wrong.** A policy changed by a later `ALTER POLICY … USING (…)` was broken at its
    `CREATE`. A full reset then overwrote the break, which read a false `survived`.
  - **Now.** The postgres adapter records `ALTER POLICY/FUNCTION/TRIGGER` (and renames) on the
    effective symbol. The break goes on the last statement that sets `USING` or `WITH CHECK`, in
    whichever migration it is.
  - **The object swap.** For an ALTER target, `{patched_sql}` and `{original_sql}` are that ALTER
    alone. When a later statement touches the object, the run reads `unknown` with a reason.
  - **Generator version.** It is 4, so cached SQL outcomes are recomputed once.
- **Runtime-root proposals no longer duplicate declared entry points.**
  - **Serve handlers.** A serve handler is covered by an `http_route` or `webhook` entry point whose
    handler is in its module or file.
  - **Cron jobs.** A cron job is covered by a `cron` entry point with the same job name, or with the
    same schedule and a handler the job's body calls.
  - **Edge function path.** A serve proposal for `supabase/functions/<name>/index.ts` carries
    `trigger.path: /functions/v1/<name>`.
- **A proposal never raises a warning.** `UNCLASSIFIED_ENTRY_POINT`, `DUPLICATE_TRIGGER` and
  `HANDLER_MISSING` report a `status: proposal` entry point as info, so proposals cannot turn a
  `fail_on: warning` gate red.
- **"Did not apply (runner exit 125)" quotes from the `ERROR:` token**, so a JSON log line reads as
  its message.
- **A held break lock prints `BREAK_LOCKED:`** in text mode.

### Added

- **HTTP edges.** A literal `fetch`, `axios` or `supabase.functions.invoke` call is a reference of
  kind `http`, keyed by method and normalised path.
  - **When it joins.** The reach graph joins it to a detected route, a declared `http_route` entry
    point or an edge function only when both ends were extracted and the keys are equal. `:id`,
    `{id}` and `[id]` match each other.
  - **When it does not.** Several matches are ambiguous. A computed URL never makes an edge. An
    absolute URL joins only when its origin is listed in `http.origins` in `lattice/workspace.yaml`.
  - **Findings.** `validate` adds `HTTP_CALL_UNSERVED` and `HTTP_CALL_DYNAMIC` (info).
  - **Edge truth.** `BlockEdge` gains `truth`.
- **Exercised hops in `lattice slice`.** A hop is exercised only when one verifier's own per-test
  run executed symbols on both sides of it, marked `=kind=>`. A browser test with a faked backend no
  longer shows the API or the database as run.
  - **Layer spans.** Each verifier is listed with the layers its run covered, or `unknown` without
    per-test coverage.
  - **Contract.** `lattice.slice/1` gains `exercised`, `exercised_by`, `exercised_hops` and
    `verifiers`.
- **`layer` on audit enforcer outcomes.** `lattice audit --json` rows carry `layer` on their
  `enforcers[]` entries too.

### Behaviour changes to plan for

- **Break runs are slower at first.** Cached break outcomes from earlier versions are not reused,
  and outcomes are reused only on the same tree unless runners declare `inputs`. The first
  `lattice break` after upgrading re-runs everything, and so does the first run on each new commit.
- **Fewer SQL survivals on altered policies.** Breaks of policies with a later `ALTER POLICY` change
  outcome: survivals there were false.

## [0.20.0] — 2026-11-21

Lattice now says what kind of runtime each piece of code lives in. Code the runtime invokes becomes
a proposed entry point, and each promise shows which ways in reach it, through which layers.
Database break runs are faster and cannot overlap.

### Added

- **Layers.** Every symbol, block, feature and invariant carries a computed `layer`: `ui`, `api`,
  `service`, `data`, `async`, `edge`, `external` or `unknown`.
  - **How a layer is decided.** An adapter fact comes first: Postgres objects are `data`, and an
    entry point's handler takes the layer of its kind. Otherwise a `layers:` path rule in
    `lattice/workspace.yaml` decides. Otherwise the layer is `unknown`. Nothing is guessed.
  - **Where layers show.** In `audit --json`, `feature show` and `/api/v2`, and in a **Group by >
    Layer** lens on the block map.
  - **Unchanged.** `tier` keeps its meaning. User guide section 4o.
- **Runtime roots become proposed entry points.** A `serve` handler, a `cron.schedule` job or a
  detected route that no entry point covers is proposed (`status: proposal`, `detected_by`).
  - **Flows.** A proposal's flow is the set of features its handler reaches through the call graph
    (`basis: reach`). Steps found by module proximity are marked `basis: proximity`.
  - **Nothing is written** until you run `ep accept` or `ep reject`.
  - **No silent roots.** A root that cannot be proposed raises `RUNTIME_ROOT_UNPROPOSED` (info): a
    route whose handler is not in the graph, or a cron job with a computed name or schedule. User
    guide section 4b.
- **`lattice slice <invariant-id> [--json]`.** For one promise, this lists one shortest path per
  root and enforcer.
  - **Each hop** carries its edge kind, the layer at both ends, and whether it is `resolved` or
    `unknown`.
  - **Each path** lists the layers it is carried by, e.g. `ui → data`.
  - **The reading** is `complete`, `unknown_from_hop`, `unreached`, `no_enforcer` or `unknown`. A
    slice with an unknown hop is never complete.
  - **Formats.** The JSON contract is `lattice.slice/1`, also served at `GET
    /api/v2/promises/{id}/slice`. The `lattice serve` audit page shows a Slice section per promise.
    User guide section 4p.
- **SQL breaks can apply just the broken object.** A runner can name `{patched_sql}` and
  `{original_sql}`: files holding only the broken object's statement, and the same statement
  unbroken. Policies and triggers come with their `DROP … IF EXISTS`.
  - **Why.** The runner no longer has to reset the database for every break.
  - **The default** is still the full reset.
  - **When the swap cannot apply.** A command that names these placeholders for a break that has no
    such statement reads `unknown`.

### Fixed

- **One `lattice break` run per repository at a time.** A second run fails at once with
  `BREAK_LOCKED`, naming the holder's pid and start time.
  - **The lock** is `lattice/.cache/results/break.lock`. It is removed on exit, SIGINT and SIGTERM.
  - **Stale locks.** On Linux and macOS, a lock whose process is gone is reclaimed. On Windows it
    stays held until removed.
- **"Did not apply" reasons are readable.** A runner exit 125 quotes the first `ERROR:` line (else
  the last line), cut at 120 characters, instead of a CLI's whole JSON error.
- **Entry-point files are written in canonical YAML**, so an accepted entry point passes `lattice
  fmt --check`.

### Behaviour changes to plan for

- **New info findings.** On a codebase with `serve` handlers, `cron.schedule` jobs or detected
  routes, `extract` now lists proposed entry points, and `RUNTIME_ROOT_UNPROPOSED` info findings can
  appear (inline route handlers, computed cron names).
- **Rule changes.** `DUPLICATE_TRIGGER` now includes a cron job's name, and tells apart entry points
  that have no trigger by their handler, so it reports fewer false duplicates.
- **`ep list` and `ep show`** build the graph read-only to include derived proposals, so they are
  slower on large repositories.

## [0.19.2] — 2026-11-21

Two `lattice break` outcomes read truthfully on real database tests.

### Fixed

- **A TAP "Bad plan" is a failing test, not a build failure.** When a break makes a pgTAP script
  stop partway, pg_prove prints `Parse errors: Bad plan`; a case-insensitive marker meant for
  PHP read that as "break does not compile" (`unknown`). The PHP marker now matches PHP's own
  wording only (`PHP Parse error`, `Parse error: syntax error`), so such runs read `killed` when
  the baseline passed.
- **A run some verifiers never saw reads `unknown`, not `survived`.** `survived` now requires every
  verifier of the run to have run, at baseline and after the break. A run whose verifiers that ran
  stayed green while others did not run (no runner, no test selected, absent from JUnit, or not
  run after the break) reads `unknown` with a `partial:` reason, so `--fail-on survived` no longer
  fails on a survival nobody tested. A group that fails after the break still reads `killed`.

### Behaviour changes to plan for

- **Cached break outcomes are re-checked once.** The generator version is bumped, so the first
  `lattice break` after upgrading re-runs each break and baseline once.
- **Fewer `survived`, more `unknown` (partial) and `killed`.** On a real project this moved 72
  runs from `survived` to `unknown` and 37 from `unknown` to `killed`.

## [0.19.1] — 2026-11-21

Faster, more truthful `lattice break` for database tests, results that stay current across
bookkeeping commits, and a tidier block map.

### Fixed

- **A runner exit of 125 reads `unknown`, never `killed`.** A break runner whose setup fails (for
  example, a database reset when a generated SQL break did not apply) exited non-zero and read
  as a verifier catching the break. As in `git bisect`, 125 now means "this code could not be
  set up": the run reads `unknown` ("did not apply (runner exit 125)"). The user guide's pgTAP
  runner ends its reset with `|| exit 125`.
- **Results of a commit with head's tree stay current.** `validate` and `audit` now treat test
  results and coverage ingested at an earlier commit as current when git shows the commits in
  between differ only in Lattice's own `.ledger/` and `.cache/` (an empty commit, or the
  ledger-only commit at the end of a pull request): no `RESULTS_STALE`, `VERIFIER_FAILING` reads
  them, and `matches_head` is true. If git cannot compare the commits, they still differ.
- **`lattice break` names each missing runner once.** Runs that lack a runner count under one
  reason, `no runner configured`; `--json` adds `missing_runners` (per language, the runs that
  need it) and the "no break ran" line lists them.
- **Block map edge labels no longer pile up where lines converge.** Each label is placed along its
  own line where it overlaps no other label and no box.

### Changed

- **Each break baseline runs once per tree.** Promises whose verifiers render the same runner
  invocation (a whole-file `pg_prove` over two tests of one file) share one baseline, and each
  still checks its own verifiers ran. Passing baselines are kept per HEAD tree in
  `lattice/.cache/results/break-baselines.json` and reused by the next run at that tree (never on
  a dirty tree, never a failing, timed-out or errored one). The summary shows
  `baselines: N run, M reused`; `--json` adds `summary.baselines`.

## [0.19.0] — 2026-11-21

Break-testing for PostgreSQL, a workspace switch that requires hardware-key signatures, and a
validator that says when results are of another commit.

### Added

- **Generated breaks for PostgreSQL enforcers.** `lattice break` now breaks SQL guards instead
  of reading them `unknown`: a policy's `USING` and `WITH CHECK` expressions become
  `(true OR (expr))`; a trigger gets ` WHEN (false)` (or `WHEN (false AND (cond))`); a PL/pgSQL
  function or procedure returns early after its top-level `BEGIN`, with a return that fits its
  result (`RETURN COALESCE(NEW, OLD)` for a trigger function, `RETURN true` for a boolean guard,
  `RETURN;` for void, set-returning or OUT-parameter functions, `RETURN NULL` otherwise). Other
  function languages, event triggers and `INSTEAD OF` triggers stay `unknown` with a reason. The
  verifiers run under a Postgres runner in `break.runners`; the user guide has a pgTAP example.
- **`confirm.routes`** in `lattice/config.yaml` (`review`, `signature`; default both) narrows
  which routes make a decision read `human`. Under `routes: [signature]` a confirmation by review
  alone reads `pending` (`ROUTE_NOT_ACCEPTED`) in `confirm` and in every reader (the audit,
  `check`, `validate`, `feature show`, `/api/v2`), and `confirm --unsigned` lists it. An unknown
  route is a `VALIDATE_POLICY` error.
- **`SOLE_HUMAN_APPROVER` note.** `lattice confirm` notes, without changing the verdict, when a
  bot or agent opened the decision PR and the approver is the repository's only human with write
  access, so the review route proves only that another account approved. Review records carry
  `pr_author` and `pr_author_bot`.
- **`RESULTS_STALE` (info).** `lattice validate` names ingested test results of another commit
  than the graph's: criteria and scenarios still read DEMONSTRATED from them, while the
  per-invariant `VERIFIER_FAILING` and the audit ignore them. It changes no level; raise it with
  `validate.severity: {RESULTS_STALE: warning}`.

### Fixed

- Counts of 100 or more print in full: the "no break ran" line read `651 of q1 runs`, and search
  ids for a BRD's 100th goal and later were garbled.

### Behaviour changes to plan for

- **A `config.yaml` that exists but cannot be parsed makes confirmed decisions read `unknown`**
  (`CONFIRM_ROUTES_UNREADABLE`) and `lattice confirm` refuse with `BAD_CONFIG`, since the accepted
  routes cannot be known. A missing `config.yaml` still means the default.
- **SQL enforcers that read `unknown` ("no generated break for postgres") now get a break.** With
  no Postgres runner configured they stay `unknown`, now with "no runner configured".

## [0.18.1] — 2026-11-21

A CI-ready `lattice break`, and a block map that loads visibly and uses the screen.

### Added

- **`lattice break --fail-on survived,unknown`** exits 1 (`BREAK_OUTCOME`, with the counts) when
  a run the invocation shows (the `--promise` / `--feature` selection) has a listed outcome. The
  default exit code is unchanged: 0 once the report is written. With `--fails-on-base` only
  `--fail-on unknown` applies. When no shown break ran, the output ends with a
  `no break ran: …` line naming the top reasons, and `--json` adds a `summary` (counts by
  outcome, `ran`).

### Fixed

- **The block map shows a live status while `lattice serve` builds the graph.** The first
  `/api/graph` request builds the knowledge graph and audit and can take seconds on a large
  workspace; the page shows the elapsed time and, after about 3 s, that later loads are cached.
  A failed load names the HTTP status and the server's error, with a Retry button. An exported
  map, whose graph is inline, never shows it.
- **The block map's first view follows the pane's shape.** A wide, shallow graph (many blocks,
  few links) is no longer drawn as one tall column: its connected groups are packed to the pane's
  aspect ratio, so Fit draws it much larger (14% → 40% on a 276-block graph at 1600×1000). Graphs
  that already fit are unchanged; one large connected group improves less.

## [0.18.0] — 2026-11-21

Lattice now works offline and fails fast. The block map and `lattice serve` load nothing from
the network, the map covers every feature and promise, commands that run tests check their
setup first, and a red verifier no longer hides behind a clean `validate`.

### Added

- **A fail-fast preflight** for `lattice break`, `runs-clean`, and `demonstrate` when it runs
  tests. In seconds and without running a test, each command checks what its config declares:
  - executables on `PATH` and runner directories;
  - the Node minimum from `package.json` `engines.node`;
  - the Docker daemon, for a command that runs docker;
  - TCP reachability of `runtime.services` and `break.services`;
  - a clean tree, for `break`.

  Every problem is listed in one refusal that exits 2 (`PREFLIGHT_FAILED` in `--json`) before
  any step runs. `--no-preflight` skips the check, and `lattice doctor` lists the same checks.
  `lattice check` runs no test, so it only adds a `preflight` note, which never changes the
  verdict, `merge_allowed` or the exit code.
- **`lattice results ingest` lists the failed tests** after the totals, up to ten, with their
  `@verifies` refs. `--json` adds `failed`.

### Fixed

- **`VERIFIER_FAILING` for every invariant with a red verifier.** It used to be raised only per
  BRD criterion and per scenario, so a red test of an invariant that no criterion maps left
  `validate` clean. It is now raised per invariant from the ingested results of the graph's
  commit, naming up to three failing tests; case verifiers count for their invariant. A
  criterion that already reports the invariant names the same tests instead, and results
  stamped with another commit raise nothing.
- **The block map shows every feature and invariant.** `refresh` loads the manifests `extract`
  loads, nested directories included, so workspaces with nested features no longer get blocks
  for only a handful of them.
  - A dotted feature's block sits inside the block of its id's prefix, or inside a feature-group
    block when no feature declares the prefix. Block ids are unchanged.
  - `export map` and `serve` rebuild the block graph as `refresh` would, and say so when they
    cannot. `export map --no-refresh` draws the stored graph as it is.
- **The block map works offline.** elkjs 0.9.3 is vendored and embedded in the binary.
  `export map` inlines it, and `serve` serves it with an SRI hash, so a map opened from
  `file://` is laid out.
- **`lattice serve` pages load nothing from the network.** The styles are a committed Tailwind
  stylesheet. The Geist, JetBrains Mono and IBM Plex Serif fonts and Mermaid 10.9.8 are
  embedded and served from `/vendor/`. `THIRD_PARTY_NOTICES.md` lists every vendored file and
  ships in release archives.
- The page footer shows the running version; it said 0.5.0 on every page.

### Behaviour changes to plan for

- **`break`, `runs-clean` and `demonstrate` refuse with exit 2** when their declared setup is
  missing or unreachable. Pass `--no-preflight` to keep the old behaviour.
- **`VERIFIER_FAILING` can appear on invariants that were silent before.** It is a warning, so
  it fails a gate only with `fail_on: warning`.

## [0.17.0] — 2026-11-21

Confirmation by signature that holds whatever the merge style, per-case confirmation for
acceptance cases, an assurance lens on the block map, and a fast `lattice serve`.

### Added

- **Endorsed changes to `lattice/allowed_signers`.** A commit that adds a signer counts only
  with `lattice/allowed_signers.sig`, signed by an sk- key trusted before the change. The first
  file is the root of trust (`ROOT_SIGNERS`, with its commit); removals need nothing; a key added
  otherwise reads `SIGNER_NOT_ENDORSED`, and a shallow clone reads `SIGNERS_NO_HISTORY`. This
  closes the rebase-merge gap of 0.16.0, whatever the merge style.
- **`lattice sign --signers`** endorses the working tree's change to `allowed_signers`, with the
  same refusals as `lattice sign`.
- **`lattice confirm --unsigned`** lists the decisions a signature could confirm, with the exact
  commands: typed approvals and acknowledgements (with or without the `local` mark), a council's
  approval its delegating human has not acknowledged yet (`council`), and confirmations by review.
  It writes nothing.
- **A signer must be a human login** (`SIGNER_NOT_HUMAN`); signature records name the PR author
  and whether a bot or agent opened it.
- **Per-case confirmation.** Decisions record their promises' acceptance cases, and
  `lattice confirm` binds that list to the packet it pinned (`cases_check`). A case reads
  confirmed only under a current, confirmed decision with a verified list; otherwise unconfirmed
  as added, edited or retired after confirmation, `CASE_DECISION_STALE`, `CASE_STAMP_MISMATCH`,
  unverified, not recorded, or never confirmed. Shown in the audit, `feature show`, `/api/v2`
  `case_rows` and the `/audit` page. The promise's confirmed signal and packet hashes are
  unchanged.
- **The review diff names case edits** (`case 1b of f:INV-1 edited: kind edge->error`); packets
  gain `diff.friction` (`full` for any structural change, `light` only for text-only drift).
- **`lattice impact`** gains `touched_cases`; **`lattice break --promise f:INV-n#a`** breaks one
  case against its tagged tests.
- **An assurance lens on the block map** (`a`, `as=1`, in `lattice serve` and
  `lattice export map`). Each block is coloured by the weakest audit class of its promises, in
  the kernel's order; only break-tested and exercised are green, and unknown is hatched grey.
  `/api/graph` and the export add an `assurance` object.

### Fixed

- **`INVARIANT_BROAD`** names very long statements however they are punctuated (70 words or
  more), and needs 60 words for the sentence and claim rule; it now names the genuinely omnibus
  invariants on a real project (every statement over 400 characters) and stays quiet on short
  two-sentence rules.
- The missing-base-audit note says how CI keeps the base snapshot, and that a local one takes a
  full suite run.

### Changed

- **`lattice serve` reuses the knowledge graph and audit** behind `/api/graph` and `/api/v2`
  while nothing they read has changed (git HEAD, every changed or untracked file's identity,
  everything under `lattice/` including `.cache/`, the files detectors read). Any change
  recomputes; inputs it cannot identify bypass the cache. `X-Lattice-Cache: hit|miss|bypass`
  shows which. A repeated map load drops from about 0.7 s to 0.05 s on Lattice's own repository.

### Behaviour changes to plan for

- **A signer added to `allowed_signers` after the first version needs an endorsement** by an
  already-trusted key (`lattice sign --signers`); until then its signatures read unknown.
- **Decisions prepared now carry a `cases:` list.** Older decisions read their cases as
  not recorded until re-confirmed.

## [0.16.0] — 2026-11-20

Assurance done right on real projects: a human confirmation that works on a single-maintainer
repository, acceptance cases under invariants, a test plan for the lines no promise guards, and
fewer false readings from `lattice check`.

### Added

- **Confirm a decision by hardware-key signature.** `lattice sign <decision>` signs a decision
  with an SSH security key (`ed25519-sk` / `ecdsa-sk`). It refuses without a terminal, under CI,
  for an agent, and for software keys (`SOFTWARE_KEY`). `lattice confirm` counts the signature as
  `human` only when the key is listed for the login in `lattice/allowed_signers`, both at the base
  commit and at the tip, and is one of the login's GitHub SSH signing keys. Every reader
  re-verifies a signature verdict from git. Records gain `via: review|signature`; text output
  names the method (`human (signed by ana, SHA256:…)`). Merge decision PRs with a merge commit or
  a squash, not a rebase.
- **Acceptance cases under an invariant.** An invariant may list `cases` (`a`, `b`, …; `happy`,
  `edge`, `error` or `security`). `@verifies f:INV-1#a` verifies one in every language, and still
  counts for `INV-1`. The audit computes each case's coverage (`covered`, `partial`, `failing`,
  `uncovered`; only `covered` is positive) and shows it in `lattice audit`, `feature show`, `rtm`
  and the `/api/v2` promise. Cases are part of what a human confirms: editing one makes the
  decision stale. `validate` adds the `CASE_*` codes, `ORPHAN_ANNOTATION_CASE` and
  `INVARIANT_BROAD` (info) for a statement that makes several claims and has no cases.
- **`lattice impact --tests`** lists the tests that matter for a change, by runner. For touched
  lines no promise guards, it looks for tests in per-test coverage, then the call graph, then a
  neighbour heuristic, then two labelled hints: `co_change` (a test file that changed in the same
  commits, each commit weighted by its size; `break.co_change`) and `mentions` (a test that names
  the symbol or its route). Each test has a `why`; anything it cannot place is `uncovered`.
- **`lattice break --fails-on-base <rev>`** runs a change's new or edited tests on the old code
  and records which of them fail there (`evidence.fails_on_base`, `PASSES_ON_BASE` note).
- **`ENFORCER_MOVED`.** When a change removes an enforcer of a tier 1 or 2 promise and adds
  another enforcer of it, the removal no longer reads BROKEN. It opens a `break_tested` gap
  (`MOVE_NOT_BREAK_TESTED`) that blocks the merge until a break of each added enforcer is killed
  at head. A removal with only kept enforcers left, and any removal on a tier-3 promise, stays
  `ENFORCER_REMOVED`.
- **`brd link --reconcile`** completes every one-sided link whose other half is declared, and
  keeps the BRD's version and approval. `brd link` appends ids and never re-sorts
  `implements_via`.
- **BRD `sources`** (`ticket`, `roadmap`, `help_page`, `api_doc`, `adr`). Only a person sets
  `checked_by` / `checked_at`; `brd from-code` drops them; `validate` checks the shape and never
  fetches a URL.
- **`STATEMENT_UNCHANGED` note:** an enforcer changed under a statement that did not.
- **`lattice ledger runs`** shows the latest check runs from the untracked cache.
- **TypeScript parameterized tests.** A test with a template-literal title, or one declared in a
  loop or a `forEach` callback, is one test per call site, named by its title template. Results
  join it by their expanded names (any failure fails it; an ambiguous name reads unknown), and
  runner filters match every expansion.

### Fixed

- **"An entry point reaches it" is decided per symbol**, from declared entry-point handlers in
  the call graph, not from the feature's flow. Untraced lines on this repository fell by about
  a fifth, and web API handlers and contract types now read as reached.
- **One rule for a dirty tree.** `check`, `impact` and `break` agree: a modified, deleted or
  untracked file that git does not ignore counts; Lattice's own `.ledger/` and `.cache/` never
  do, whatever the workspace is called. CI's check comment no longer reads `+uncommitted` on
  every pull request.
- **Go call graph:** a function declared again in a build-constrained file is reached with the
  first declaration; an unexported method name only matches the caller's own package; test
  support packages (`*test` importing `testing`) are test code.
- **`impact --tests` lists only tests**, never a test file's helpers.
- `lattice check` no longer counts an untouched promise's break-based gap as new when no break
  ran at head.
- The ledger records decisions only; check runs go to `lattice/.cache/last-runs.json`.
- `lattice init` gitignores the derived graph and the cache; `validate` warns when either is
  tracked (`GRAPH_TRACKED`, `CACHE_TRACKED`).
- A source build reports its commit and time from the Go build info.
- The missing base audit names how to create the snapshot.

### Changed

- **The official tree-sitter Go bindings** (`github.com/tree-sitter/go-tree-sitter`) replace the
  community fork. Extraction is identical on every tested workspace.
- **Go 1.26** is required to build from source.

### Behaviour changes to plan for

- **Untracked source files make the tree dirty** for `audit`, `prepare`, `confirm` and
  `check --snapshot`, as they already did for `break`. Write reports outside the checkout or
  gitignore them.
- **`lattice.json` and `lattice/.cache/` are no longer tracked** in a new workspace; untrack them
  in existing ones (`GRAPH_TRACKED`, `CACHE_TRACKED`).
- **`INVARIANT_BROAD` is info only** and never changes `fail_on`.
- **Existing `brd ack` records stay `local`.** To reach `human` by signature, re-confirm each
  decision with `lattice sign` once a key is enrolled.

## [0.15.1] — 2026-11-10

Hardening from running the v0.15.0 assurance features on a real downstream project: evidence
that could be stale, forged or misread now reads `unknown` or `UNPROVEN`, never a pass.

### Fixed

- **`lattice check` sets aside evidence for another commit in local runs too.** Results,
  coverage and breaks stamped with a commit other than HEAD (or with none) read `unknown`
  (`EVIDENCE_STALE`: "results are for <sha>, not HEAD <sha>: re-run the tests and ingest"),
  so an earlier ingest can no longer produce `ASSURED` or `BROKEN`. `--ci` still requires a
  CI run (`EVIDENCE_NOT_CI`); base audit snapshots keep their own rule. Evidence ingested at
  HEAD with uncommitted changes still counts: the stamp is a commit, not a tree.
- **Touched lines no promise guards keep `lattice check` at `UNPROVEN`.** A change to code an
  entry point reaches but no enforcer span or checked reference covers read `ASSURED`
  `NOTHING_TOUCHED` ("touches no promise and no production line"). It now reads `UNPROVEN`
  `UNGUARDED_LINES` ("touches N production lines that no promise guards"). `merge_allowed` is
  unchanged: these lines are reported, not blocking. `lattice.check/1` adds
  `blind_spot.unguarded_lines`, `guarded_lines` and a per-symbol `unguarded` list. A removed
  symbol that enforced nothing counts as unguarded, so a pure dead-code removal reads
  `UNPROVEN` (still mergeable).
- **`lattice results ingest` accepts an empty JUnit report** (`<testsuites/>`,
  `<testsuites tests="0"></testsuites>`, with or without a declaration or byte-order mark) as
  zero results, instead of aborting the ingest and losing the other reports. A malformed report
  shows its real XML error, still prefixed with its file.
- **`lattice break` runs right after `lattice results ingest`.** The workspace's own `.ledger/`
  and `.cache/` are left out of the dirty check wherever the workspace sits and whether or not
  the cache is gitignored. `breaks.json` `dirty` now matches the tree actually tested,
  including untracked files when `status.showUntrackedFiles` hides them, and the refusal names
  the dirty files.
- **Re-confirming a promise never overwrites an earlier decision.** Preparing again after an
  edit hit `BRANCH_EXISTS`, and once the old branch was deleted the new decision overwrote the
  first one's file.

### Added

- **`lattice audit` names the enforcer a row is about.** Each `lattice.audit/1` row has
  `enforcers: [{symbol, file, line, outcome, reason}]` (`killed | survived | unknown |
  not_run`), and the reason of a multi-enforcer row ends with a tally ("1 of 3 enforcers
  survived: X; 2 killed: …"). The class is unchanged: one survivor with every verifier run is
  still `fake` / `passes_when_broken`. Text, Markdown and `/audit` print one line per enforcer
  for fake, partial and mixed rows.
- **`lattice audit --promise <f:INV-n>`** (repeatable or a comma list), and `?promise=` on
  `/audit` and `/api/v1/audit`. It narrows output only; an unknown id exits 2
  (`AUDIT_UNKNOWN_PROMISE`) or answers 400.

### Behaviour changes to plan for

- **`confirm.json` carries a stamp, and only an intact one confirms.** `lattice confirm` stamps
  `lattice/.cache/confirm.json` with the commit, the CI run (or `local`), the platform and a
  digest of its verdicts. The audit's `confirmed` signal, `validate` and `/api/v2` accept
  `human` only from an intact stamp; unstamped, edited or git-tracked files read `unknown`
  (`CONFIRM_UNSTAMPED`, `CONFIRM_TAMPERED`, `CONFIRM_TRACKED`). A local file shows
  `human (local, not CI)`, and under `lattice check --ci` a local or other-commit file reads
  `unknown` (`EVIDENCE_NOT_CI`). The stamp is a checksum, not a signature: the protection is
  `--ci` plus refusing a committed copy. **Files written by v0.15.0 have no stamp and now read
  unknown: re-run `lattice confirm`.**
- **Decision branches and files are named by their packet**: `<id>-v<N>[.<action>]-<packet8>`.
  Preparing the same packet again reports the existing branch and exits 0. A same-named branch
  holding other content is refused (`BRANCH_EXISTS`), and a decision already on HEAD is refused
  (`DECISION_EXISTS`). `lattice confirm` and `lattice check` still read files with the old names.
- **`lattice check` reads `UNPROVEN` more often** (stale evidence, unguarded lines). Neither
  changes `merge_allowed`.

## [0.15.0] — 2026-11-10

### Fixed

- **`lattice impact` keeps the index's time on its private copy.** Git content-checks an
  entry no older than the index and trusts stat data otherwise; the copy was stamped with the
  current time, so a file rewritten at the same size in the same second as the last `git add`
  could read unchanged. The likely cause of an intermittent `TestMovedSymbolIsNotChanged`
  failure in CI.
- **Missing results are not a fall in `lattice check`.** A promise that reads
  `not_demonstrated` only because no result is ingested at head (`no_results`) was a
  `LEVEL_DROPPED` weakening, so a commit without a test run read `BROKEN`. It is now a gap
  (`UNPROVEN`); a red result still falls. Lattice's own `lattice-check` workflow now runs the
  test suite on every pull request, and only the break run is skipped when no Go changed.

### Added

- **Lattice runs `check` on itself in CI, report only** (`.github/workflows/lattice-check.yml`,
  `scripts/ci-lattice-check.sh`). On a push to master: the suite as JUnit, `results ingest`,
  `break`, `check --snapshot`, and the artifact `lattice-audit-<sha>`. On a pull request:
  the merge base's artifact (if any), `check --base --ci --markdown`, the job summary and one
  sticky comment (`<!-- lattice-check -->`). The verdict never fails the job; a setup error
  does. Break runs on master and on pull requests that touch Go. It is a workflow of its own
  because `release.yml` grants the called `ci.yml` only `contents: write`. User guide section 4m
  has the snippet to copy.
- **`/api/v2` contracts for the React UI** (in `lattice serve`; `/api/v1` is unchanged).
  `GET /api/v2/meta` (`lattice.meta/1`), `/queue` (`lattice.queue/1`), `/brds`,
  `/brds/{id}[?version=N]` and `/features/{id}` (`lattice.brd/1`: the BRD page, the review
  packet with its structural diff and `packet_hash`, and the feature drawer), `/check?base=`
  (exactly `lattice check --json`) and `/health` (`lattice.health/1`), and
  `POST /api/v2/prepare`. The shapes live in `pkg/lattice/contract`. The API computes
  nothing that counts: a field with no kernel source reads unknown `NOT_COMPUTED`, a scope
  that is not a change never reads `ASSURED` or `BROKEN`, and every payload passes a lint
  (unknown, `UNPROVEN` and partial are never a `pass`) before it is sent.
  `prepare` is refused unless the request comes from loopback with the CSRF token
  `/meta` issued, writes only a local branch, and confirms nothing. `lattice check`'s body is
  now `check.Run`, shared by the CLI and the API; `prepare.Request` takes an optional
  `packet_hash` (`PACKET_STALE`).
- **Re-confirm in the same pull request.** An edit to a confirmed promise that carries a
  `lattice prepare` decision for its new version reads its `confirmed` gap as pending, not
  `BROKEN`, and does not block the merge (tier 3 included): the merge's human approval is the
  confirmation. Without the decision the edit still reads `CONFIRMATION_VOIDED`.
- **`lattice check --base <rev> [--json | --markdown] [--ci]`** gives one
  verdict for a change, from `merge-base(<rev>, HEAD)` to the working tree:
  `BROKEN` (something fell: a level dropped, an enforcer or verifier removed, a
  break killed at base survives at head, the suite is red, or a confirmed
  promise's statement, tier or mapping was edited), `UNPROVEN` (a touched
  promise has a gap, or touched lines are untraced; never drawn as a pass) or
  `ASSURED`. It reuses `impact` for the touched promises and lines and
  `audit` for the signals, and prints `merge_allowed`: an `UNPROVEN` change may
  merge only when no (promise, signal) pair gained a gap against base, the new
  untraced lines did not rise, and no tier-3 promise is involved. A waiver never
  removes a gap. A file-level module tag alone does not make a line traced.
  - `--json` is `lattice.check/1` (frozen by a golden test); `--markdown` starts
    with `<!-- lattice-check -->`. Exit codes: 0 for `ASSURED` or an allowed
    `UNPROVEN`, 1 for `BROKEN` or a disallowed `UNPROVEN`, 2 for a usage error.
  - The base audit is read from `lattice/.cache/audit/<sha>.json`, written by
    `lattice check` or `lattice check --snapshot` and restored by CI from a
    cache. With none, evidence comparisons read `unknown` and the verdict is
    `UNPROVEN`, never `BROKEN`, from missing data.
  - `results ingest`, coverage ingest and `lattice break` stamp their files with
    a `ci_run` (`GITHUB_RUN_ID`, `CI_PIPELINE_ID` and similar, else `local`).
    With `--ci`, evidence that is not for `HEAD` or is from a `local` run is set
    aside and reads `unknown` (`EVIDENCE_NOT_CI`).
- **`lattice break [--promise f:INV-n] [--feature f] [--timeout 120s]
  [--allow-dirty]`** breaks each promise's enforcer on a temporary git
  worktree and runs only that promise's verifiers. A verifier that passes
  before and after the break reads `survived`; one that fails after reads
  `killed`; anything else (no generator, a break that does not compile, a
  failing baseline, a timeout, no runner) reads `unknown`, never a pass.
  - Generated breaks insert an early return for Go, TypeScript/JavaScript
    and Python; `lattice/breaks/<feature>/<INV>.patch` scripts a break
    (SQL, anything else).
  - Go runs with `go test -run`; other languages use `break.commands.<lang>`
    in `config.yaml`.
  - Writes `lattice/.cache/results/breaks.json` (`lattice.breaks/1`),
    cached by enforcer, verifier, method and script hashes.
- **`lattice audit`** and the `/audit` page of `lattice serve`: one row per
  invariant, classified `fake` (`never_runs_enforcer` from per-test coverage,
  `passes_when_broken` from a break run), `break_tested`, `exercised`,
  `demonstrated_only`, a gap, or `unknown`. Each row carries eight signals with
  fact, upper_bound or proxy labels. `--json` is `lattice.audit/1`, `--markdown`
  suits a PR comment, and `lattice/audit-notes.yaml` adds second-reader notes
  that never change a class. It reads `breaks.json` as a file and blocks nothing.
- **`lattice prepare` and `lattice confirm`** make confirmation a human act that
  the platform vouches for. `prepare brd approve|return|ack|overturn`, `prepare
  promise confirm` and `prepare waiver add` validate the action against the
  kernel and write a decision branch `decision/<id>-v<N>` holding one decision
  file, built from HEAD with git plumbing: the checkout, index and working tree
  are never touched, and nothing is pushed or opened. It prints
  `lattice.prepare/1` with the `gh pr create` command, or a refusal with a code
  (`REGULATED_MACHINE_WRITTEN`, `VOTES_INCOMPLETE`, `COUNCIL_TOUCHED`,
  `WAIVER_TIER3`, and more).
  - `lattice confirm [--platform github] [--offline]` finds each decision file on
    the default branch, the pull request that merged it, and that request's
    reviews (GitHub REST via `GITHUB_TOKEN` or `GH_TOKEN`). A decision counts only
    with an approving review from a non-author, non-bot, non-agent user on the
    merged head, and while its packet hash still matches. It writes
    `lattice/.cache/confirm.json` (`lattice.confirm/1`, derived); `--offline` reads
    the cache only and unknown is never confirmed.
  - **`lattice audit`'s `confirmed` signal now reads `confirm.json` only**: `human`,
    `pending`, `agent`, `draft`, `drifted`, `none` or `unknown`. A BRD's
    `approved_by` and a hand-written `approval:` block are display only.
  - `brd approve`, `brd ack` and `brd overturn` keep working, print a deprecation
    line and record `local: true`; the audit reads that as "prepared by a local
    session, not confirmed".

### Changed

- **A platform confirmation approves a machine-written BRD.** `BRD_UNAPPROVED_LLM` no longer
  fires for an `llm_from_code` or agent-drafted BRD whose current version has a confirmed
  `approve` or `ack` in `lattice/.cache/confirm.json`. A typed `approved_by`, another version,
  a `pending` or `stale` verdict, or a confirmed `return` or `overturn` never satisfies it.
  `lattice validate` and the library read the file with `confirm.LoadCache`.
- **`lattice break` runs per framework, not per language.** `break.runners`
  lists `{name, match, command, dir}` entries (`{files}`, `{filter}`, `{junit}`);
  a promise's verifiers are grouped by the first runner whose glob matches and
  every group runs, so vitest and Deno verifiers can share a promise. A
  `break.commands.<lang>` template stays as the fallback, and Go stays built in.
  A verifier no runner matches is recorded as not run.
  - Each run in `breaks.json` adds `ran_verifiers`, `not_run_verifiers` and
    `failed_tests`. A baseline that executes no test reads `unknown` ("the filter
    matched no tests"), never `survived`. With `{junit}` the executed tests are
    counted; without it the reason says the count could not be checked.
  - `break.setup` runs commands in the temporary worktree before the baseline
    (`{repo}` is the main checkout), and `node_modules` directories are linked
    in by default (`break.link_node_modules: false` opts out).
  - The cache key includes the resolved runner commands, the setup commands and
    the generator version, so a config change reruns.
  - Only explicit build markers read as "does not compile" (Go `[build failed]`,
    esbuild `ERROR:` lines, Deno `error: TS` or `Module not found`); a runtime
    failure with a stack trace is a failing test and reads `killed`. The reason
    names the failing tests, else the first error line.
  - TypeScript and JavaScript arrows with an expression body are now broken, as
    `(true ? (undefined as any) : (expr))`; consts that are not functions read
    "not a function".
  - `--promise` prints only the selected runs; `breaks.json` still merges.
- **`lattice audit`:** a survived break counts as `fake` / `passes_when_broken`
  only if every verifier of the promise ran. With a verifier in
  `not_run_verifiers` the row reads `unknown` / `partial_run`, with a reason naming
  it, and each enforcer's outcome is in the row's evidence. `breaks.json` files
  without the new fields read as before.

### Behaviour changes to plan for

- **`lattice brd approve`, `brd ack` and `brd overturn` are deprecated.** They still work and
  still set `status: approved`, so `validate` reads existing approvals as before. They now print
  a deprecation line and add `local: true` to the approval block, so expect one new key in the
  next approval diff. Move to `lattice prepare` (a decision branch you merge through a reviewed
  pull request) and `lattice confirm` (reads the platform's approval).
- **`lattice audit` and `lattice check` count only a platform confirmation.** Their `confirmed`
  signal reads `lattice/.cache/confirm.json`, which `lattice confirm` writes; `approved_by` is
  display only. Until you run `lattice confirm` (in CI, with a token that can read pull request
  reviews), every promise reads unconfirmed there and `check` reports UNPROVEN, never ASSURED.
  `validate` is unchanged for BRDs that are already approved.

## [0.14.0] — 2026-10-26

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
