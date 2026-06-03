# Changelog

All notable changes to Lattice are documented in this file.

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
