# Lattice

`lattice` is a Go CLI + library that builds a knowledge graph linking code to
business intent. Module: `github.com/salahmyn/lattice`. Source lives under
`pkg/lattice/<subpackage>`, CLI wiring in `internal/cli`, entrypoint in
`cmd/lattice`.

## Environment

Verify the toolchain before any build/lint/deploy chain — drift here has
repeatedly blocked work mid-task. The fastest path is to run the `/preflight`
skill, which checks all of the below.

- **Node.js**: must be `>= 18`. Node 17 silently breaks Expo CLI, lint
  validation, and npx-based MCP servers. If `node -v` reports 17.x, stop and
  upgrade (e.g. `nvm install --lts && nvm use --lts`) before running anything
  Node-dependent.
- **iOS builds** (when working on the Expo/mobile side): `xcode-select -p` must
  point at `Xcode.app` (e.g. `/Applications/Xcode.app/Contents/Developer`), not
  `CommandLineTools`. CocoaPods must be installed (`pod --version`). If a build
  fails with derived-data module-map errors, clear DerivedData and re-pod
  before chasing code.
- **Go**: `go version` should be `>= 1.24` (matches `go.mod`).

## Lattice Conventions

The graph has two implemented axes plus a BRD layer on top:

- **Features** (`lattice/features/*.yaml`) — meaning. A feature links up to its
  BRD via `implements_brd: <brd-id>` (the reverse link).
- **Entry points** (`schema.EntryPoint`) — invocation: HTTP route, CLI, cron,
  queue, webhook, event consumer, gRPC. Each has a `handler` symbol and a
  `flow` of features it reaches.
- **BRDs** (`lattice/brds/<id>.yaml`, `schema.BRD`) — business intent. A BRD
  points down to features via `implements_via: [<feature-id>, ...]` (the
  forward link). One BRD → many features; one feature → at most one BRD.

Rules to honor when generating or editing artifacts:

- **Default to the reverse chain** (code → feature → BRD) for brownfield work,
  unless explicitly told to author forward. `lattice brd from-code` regenerates
  BRDs from feature evidence via LLM.
- **`llm_from_code` BRDs are always `draft`** and carry
  `provenance.source: llm_from_code`. They require a human approval pass
  (`lattice brd approve <id> --by <email>`) before becoming source of truth —
  never flip status to `approved` on the user's behalf. The validator enforces
  this (`BRD_UNAPPROVED_LLM`).
- **BRD lifecycle**: `draft → proposed → approved → superseded`. Don't invent
  other statuses.
- **Link integrity**: an `implements_via` entry naming a missing feature is a
  hard error (`BRD_PHANTOM_FEATURE`). Create the feature first, or drop the
  reference.
- **Constraints are high-risk to fabricate** — `regulatory` / `legal` /
  `financial` constraints must come from human input, never be invented by the
  LLM regenerator.
- **Canonical YAML**: field order on disk follows struct order so unrelated
  edits don't produce noisy reorderings. Preserve it; don't reorder keys by
  hand.

The **PRD / Story layer is design-phase, not yet in code.** When it lands,
reuse the established naming convention (do not coin new names), require every
entry point to declare which story it fulfills, and keep the reverse-chain
default. Confirm the exact field/naming convention with the user before
generating story artifacts — this was a prior source of rework.

Before generating any BRD/story/YAML chain, restate the conventions you'll
follow (naming, link direction, defaults) and wait for confirmation. When the
design is novel, produce a concrete YAML prototype chain on one example feature
for the user to critique *before* touching implementation.

## Validation Before Declaring Done

- After any multi-file Go change, run `go build ./... && go vet ./...` and the
  relevant `go test ./...`. The PostToolUse hook in `.claude/settings.json`
  runs `go build`+`go vet` automatically after `.go` edits — don't ignore its
  output.
- **Watch for import cycles** among `pkg/lattice/{agentic,extract,brd}`. When a
  consumer needs a provider, define a small local interface in the consumer
  rather than importing the provider package (this was the fix for a prior
  cycle). Sketch package boundaries before adding cross-package imports.
- **Verify JSON struct tags** match expected `snake_case` when (de)serializing
  metadata structs (e.g. `ProjectMetadata`). A mismatched/absent tag silently
  produces the wrong wire field.

## Working Style

- For multi-phase implementations, track phases with tasks and only advance a
  phase on green tests.
- For broad codebase questions, fan out parallel `Explore` agents (e.g. one per
  subpackage) and synthesize, rather than serial reads.
