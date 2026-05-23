# Changelog

All notable changes to Lattice are documented in this file.

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
