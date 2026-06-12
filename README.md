<h1 align="center">
  ⬡ LATT<span style="color:#8C7DD6">I</span>CE
</h1>

<p align="center"><em>A substrate for software meaning.</em></p>

<p align="center">
  <a href="CHANGELOG.md"><img alt="version" src="https://img.shields.io/badge/version-0.8.1-8C7DD6?style=flat-square&labelColor=0F1218"></a>
  <img alt="go" src="https://img.shields.io/badge/go-1.24+-0F1218?style=flat-square">
  <img alt="status" src="https://img.shields.io/badge/status-alpha-E5B66C?style=flat-square&labelColor=0F1218">
  <img alt="license" src="https://img.shields.io/badge/license-Apache--2.0-C7BEEC?style=flat-square&labelColor=0F1218">
</p>

---

Lattice treats software meaning — business intent, features, capabilities,
invariants, dependencies, decisions, entry points, work-in-flight — as a
first-class, queryable, version-controlled substrate that lives **alongside
source code and is kept in sync mechanically**.

This repository is the **core engine**: one static Go binary, `lattice`.
The MCP server ships separately as [`@salahmyn/mcp-server`](lattice-mcp/).

Supported languages for extraction: **Python, TypeScript/JavaScript, PHP 8.0+**.

## The four artifact axes

```
  ⬡  BRD          why it exists, who signed off, success criteria
  ▸  Feature      what it does — capabilities + invariants
  ⇌  Entry point  how the world reaches it — HTTP / CLI / cron / queue
  §  Decision     what we ruled out and why
```

`⬡ BRD` is new in v0.5.0 — the business-intent layer above features.
One BRD → many features; one feature → at most one BRD. Greenfield
authoring (`lattice brd new`) and brownfield regeneration
(`lattice brd from-code`) both produce the same on-disk shape.

## Install

Build from source (Go 1.24+, a C toolchain for the tree-sitter bindings):

```sh
go build -o lattice ./cmd/lattice
./lattice version
```

## Quick start

```sh
# 1. Scaffold a workspace (creates the lattice/ directory).
lattice init
# Upgrading a v0.1.0 repository? Run `lattice migrate` instead.

# 2. (Optional, v0.5.0) Declare business intent first.
lattice brd new brd.checkout.refund --title "Customer self-service refunds"
# Edit lattice/brds/brd.checkout.refund.yaml — problem, goals, criteria.

# 3. Write a feature manifest at lattice/features/checkout/refund.yaml:
#    id: checkout.refund
#    version: 1
#    status: production
#    implements_brd: brd.checkout.refund        # v0.5.0 upward link
#    purpose: Lets customers refund completed orders.
#    owners: { business: payments, engineering: checkout-eng }
#    invariants:
#      - id: INV-1
#        statement: A refund never exceeds the original charge.

# 4. Annotate the implementing code (Python shown):
#    @feature("checkout.refund")
#    @enforces_invariant("INV-1")
#    def validate_amount(order_id, amount, original): ...

# 5. Verify it with a test:
#    @verifies("checkout.refund:INV-1")
#    def test_refund_within_charge(): ...

# 6. Extract, validate, sign off.
lattice extract
lattice validate                                # exits 0 when clean
lattice brd approve brd.checkout.refund --by pm@example.com

# 7. Explore in the browser.
lattice serve --open                            # → http://127.0.0.1:7070
```

A complete, validating three-language example lives in
[`examples/sample-project/`](examples/sample-project/).

## Commands

| Command | Purpose |
|---|---|
| `lattice init` | Scaffold a workspace (the `lattice/` directory) |
| `lattice migrate` | Upgrade a v0.1.0 flat-layout repository |
| `lattice doctor [--probe-llm]` | Check optional prerequisites |
| `lattice extract` | Build the knowledge graph (`lattice.json`) |
| `lattice validate` | Run every validation rule |
| `lattice coverage` | 4-ratio adoption report (discovery / docs / verification / **BRD**) |
| `lattice brd new\|list\|show\|link\|approve` | Manage Business Requirements Documents |
| `lattice brd from-code <feature-id>` | LLM-regenerate a BRD from a feature (brownfield) |
| `lattice feature show <id>` | Show one feature + reverse links |
| `lattice ep list\|show\|accept\|reject` | Manage entry points |
| `lattice initiative list\|show\|kanban\|critical-path` | Inspect work in flight |
| `lattice import scan\|review\|inscribe` | Brownfield import session |
| `lattice analyze proposal <f>` | Conflict and impact analysis |
| `lattice patch --from-file <f>` | Preview/apply a typed artifact edit |
| `lattice serve` | Web UI (Crystal brand, live updates, search) |
| `lattice view <name>` | Render developer / product / business / c4 views |
| `lattice agent <cap>` | LLM-backed capabilities (deterministic fallback) |
| `lattice mutation run` | Mutation-test invariant-enforcing code |
| `lattice structural-checks run` | Run structural invariant checks |
| `lattice skills list` | List shipped agent skills |
| `lattice rtm` | Requirements traceability matrix with attestation header (v0.6+) |
| `lattice journey <brd>` \| `lattice actor list` | Journey and actor views (v0.6) |
| `lattice feature spec <id>` | Emit the ≤500-word `.ai-spec.md` loading contract (v0.7) |
| `lattice next` | Rank the highest-value next actions by weakest link (v0.8) |
| `lattice results ingest <junit.xml>` | Ingest test results so the RTM can say *demonstrated* (v0.8) |
| `lattice lease acquire\|release\|list` | Claim work units in a multi-agent fleet (v0.8) |
| `lattice ledger [rebuild]` | Append-only attribution ledger of truth-level transitions (v0.8) |
| `lattice runs-clean` (alias `v0`) | Gate zero: clean install → build → boot → smoke probes (v0.8) |
| `lattice flag raise\|list\|clear` | Meaning flags — ride alongside green, humans clear (v0.8.1) |
| `lattice cr propose\|price\|decide\|reconverge` | Change requests against grounded intent, with demotions (v0.8.1) |
| `lattice backprop [--since <ref>]` | Code→docs scan: which grounded criteria a change touched (v0.8.1) |
| `lattice sweep` | Forbidden-move sweep: no verifier disappears without a CR (v0.8.1) |
| `lattice demonstrate` | QAE sign-off: V0+V4+V5+V10+sweep, executed now, ledgered (v0.8.1) |

Every command supports `--json` for machine-readable output; `--actor`
(or `LATTICE_ACTOR`) names the agent identity on leases and ledger entries.

## Web UI

`lattice serve` opens a dark-by-default, single-binary web UI (no build
pipeline, no node_modules) styled with the **Crystal brand system** —
cool slate (`#0F1218`) + mica violet (`#8C7DD6`), Geist + JetBrains Mono,
hex unit-cell mark `⬡`. Live updates via SSE — a CLI edit to any manifest
shows up on the next page render without a manual refresh.

Pages: **Overview**, **BRDs**, **Features**, **Entry points**,
**Coverage**, **Validation**, **Import**, **Config**.
Auth: localhost is open; non-loopback binds require either
`--token <…>` or `--basic-auth user:pass`.

## Documentation

- [User guide](docs/user-guide.md) — full workflow + wiring Lattice into an AI assistant
- [v0.8 design](docs/v0.8-agent-steerable-knowledge-graph.md) — the agent-steerable knowledge graph
- [v0.5.0 design](docs/v0.5.0-brd-and-onboarding-proposal.md) — BRD, onboarding, Crystal brand
- [Annotation conventions](docs/annotations.md) — per-language annotation syntax
- [MCP setup](docs/mcp-setup.md) — connecting Lattice to Claude Desktop
- [Structural checks](docs/structural-checks.md) — authoring custom checks
- [Agent skills](docs/skills.md) — authoring team-specific skills
- [Changelog](CHANGELOG.md) — release notes

## Workspace layout

Every Lattice-maintained artifact lives under one `lattice/` directory:

```
lattice/
├── config.yaml, adapters.yaml, mcp.yaml, workspace.yaml
├── context.yaml         C4 actors + external systems
├── brds/                ⬡  Business Requirements Documents     (v0.5.0)
├── features/            ▸  feature manifests
├── entry-points/        ⇌  accepted entry points               (v0.3+)
├── initiatives/         ◷  initiatives and tasks
├── decisions/           §  ADRs
├── schemas/             locked contracts
├── skills/              shipped + custom agent skills
├── views/               view-template overrides
├── import/              brownfield import session
├── graph/               knowledge-graph shards (when sharding is enabled)
├── lattice.json         the knowledge graph (or shard index)
└── .cache/              gitignored: SCIP indexes, embeddings
```

`workspace.yaml` picks **embedded** mode (lattice/ inside one code repo) or
**standalone** mode (lattice/ as its own repo governing external code roots —
usable as a git submodule, and accessible to PMs/QA without code access).

## Architecture

The CLI is the canonical interface; the Go library (`pkg/lattice`) and the MCP
server are peers over the same JSON contract. Languages live only in adapters
(`pkg/lattice/adapters`) behind one interface — the core never imports a
parser. Entry points and BRDs are pluggable axes built on top of the same
extraction pass.

## What's new in 0.8.x

The agent-steerable knowledge graph (0.8.0) plus lifecycle governance
(0.8.1). Every requirement chain reports one of four truth-levels —
`declared → wired → correctly-meant → demonstrated` — and the graph
becomes the substrate a fleet of autonomous agents steers by, honestly:

- **Demonstration** — `lattice results ingest` feeds real test outcomes
  into the RTM; *demonstrated* means the verifier passed, not that it
  exists. `lattice demonstrate` composes the full QAE sign-off
  (V0+V4+V5+V10+sweep) and ledgers it.
- **Meaning fidelity** — rules catch tag-not-guard enforcers,
  unfalsifiable invariants, and invariants narrower than their criteria;
  open **meaning flags** ride alongside green rows (`demonstrated⚑`)
  until a human clears them.
- **Change control** — grounded intent changes only through
  `lattice cr` (propose → price → decide → reconverge): the blast radius
  is priced before commitment, approval demotes touched criteria (stale
  green never rides), and test deletion is legal only against a CR
  retirement item (`lattice sweep` enforces it). `lattice backprop`
  scans merged code changes back onto the requirements.
- **Steering** — `lattice next` (weakest-link ranking), `lattice lease`
  (work claims), `lattice ledger` (the single attributed event stream),
  `--actor` identity, tiers (criticality 1–3), opt-in `autonomy:` modes
  and human-pinned mandates with a hard non-delegable floor.
- **Governance** — `lattice runs-clean` ("gate zero": the app must
  install, build, boot, and answer before anything is demonstrated), an
  honest attestation header (`self | isolated | bound`) on every RTM,
  V8 author-separation, and a `lite` profile whose ceiling is honestly
  capped at *wired*.

Full release notes: [`CHANGELOG.md`](CHANGELOG.md).

## License

Apache-2.0 (intended).

<p align="center"><sub>⬡ &nbsp;Lattice 0.8.1 — <em>a substrate for software meaning.</em></sub></p>
