<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/brand/lattice-lockup-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="assets/brand/lattice-lockup-light.svg">
    <img alt="Lattice" src="assets/brand/lattice-lockup-dark.svg" height="56">
  </picture>
</p>

<p align="center">
  <em>A substrate for software meaning.</em><br>
  Features, capabilities, invariants: kept in sync with code, mechanically.
</p>

<p align="center">
  <img alt="go" src="https://img.shields.io/badge/go-1.24+-7FB6D0?style=flat-square&labelColor=0F1218">
  <img alt="status" src="https://img.shields.io/badge/status-alpha-E5B66C?style=flat-square&labelColor=0F1218">
  <img alt="license" src="https://img.shields.io/badge/license-MIT-C7BEEC?style=flat-square&labelColor=0F1218">
  <img alt="mcp" src="https://img.shields.io/badge/mcp-server-8C7DD6?style=flat-square&labelColor=0F1218">
</p>

---

Lattice treats software meaning (business intent, features, capabilities,
invariants, dependencies, decisions, entry points, work-in-flight) as a
first-class, queryable, version-controlled substrate that lives **alongside
source code and is kept in sync mechanically**.

This repository is the **core engine**: one static Go binary, `lattice`.
The MCP server ships separately as [`@salahmyn/mcp-server`](lattice-mcp/).

Supported languages for extraction: **Python, TypeScript/JavaScript, PHP 8.0+**.

| For engineers | For product and leadership | For AI agents |
|---|---|---|
| Full context for any code change without re-reading the repo. | A queryable catalogue of features and a generated business narrative. | Pre-assembled context bundles and structured tools over MCP. |

## Install

Build from source (Go 1.24+, a C toolchain for the tree-sitter bindings):

```sh
go build -o lattice ./cmd/lattice
./lattice version
```

```text
$ lattice feature show checkout.refund

⬡ checkout.refund  [production]
  Lets customers refund completed orders.

  invariant  INV-1                      A refund never exceeds the original charge.
  enforcer   validate_amount            @enforces_invariant(INV-1)      [wired]
  verifier   test_refund_within_charge  passed on ingested results      [demonstrated]
  brd        brd.checkout.refund        approved by pm@example.com      [grounded]
```

## The four artifact axes

```
  ⬡  BRD          why it exists, who signed off, success criteria
  ▸  Feature      what it does — capabilities + invariants
  ⇌  Entry point  how the world reaches it — HTTP / CLI / cron / queue
  §  Decision     what we ruled out and why
```

One BRD points down at many features; one feature points up at one BRD.
Greenfield authoring (`lattice brd new`) and brownfield regeneration
(`lattice brd from-code`) produce the same on-disk shape.

## Four truth-levels, honestly computed

Every requirement chain reports one of four truth-levels:

```
  declared → wired → correctly-meant → demonstrated
```

- **declared / wired** are decidable facts: the artifact exists, its links
  resolve to real code and tests. No model in the loop.
- **demonstrated** means the verifier *passed on ingested results* on this
  commit. Existence is not evidence; mutation testing backstops tests that
  assert nothing.
- **correctly-meant** is a human judgment. Lattice routes suspicious links to
  a person (meaning flags, narrowing checks). It flags; it never decides.

Grounded intent changes only through a governed change request
(`lattice cr`): the blast radius is priced before commitment, approval
demotes touched criteria so stale green never rides, and deleting a verifier
is legal only against an approved retirement item (`lattice sweep` enforces
it). `lattice backprop` maps merged code changes back onto the requirements,
so the requirement never silently follows the code.

## Quick start

```sh
# 1. Scaffold a workspace (creates the lattice/ directory).
lattice init

# 2. (Optional) Declare business intent first.
lattice brd new brd.checkout.refund --title "Customer self-service refunds"
# Edit lattice/brds/brd.checkout.refund.yaml — problem, goals, criteria.

# 3. Write a feature manifest at lattice/features/checkout/refund.yaml:
#    id: checkout.refund
#    status: production
#    implements_brd: brd.checkout.refund
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
| `lattice doctor [--probe-llm]` | Check optional prerequisites |
| `lattice extract` | Build the knowledge graph (`lattice.json`) |
| `lattice validate` | Run every validation rule |
| `lattice coverage` | Adoption report: discovery / docs / verification / BRD |
| `lattice brd new\|list\|show\|link\|approve` | Manage Business Requirements Documents |
| `lattice brd from-code <feature-id>` | LLM-regenerate a BRD from a feature (brownfield) |
| `lattice feature show <id>` | Show one feature + reverse links |
| `lattice feature spec <id>` | Emit the ≤500-word `.ai-spec.md` loading contract |
| `lattice ep list\|show\|accept\|reject` | Manage entry points |
| `lattice initiative list\|show\|kanban\|critical-path` | Inspect work in flight |
| `lattice import scan\|review\|inscribe` | Brownfield import session |
| `lattice analyze proposal <f>` | Conflict and impact analysis |
| `lattice patch --from-file <f>` | Preview/apply a typed artifact edit |
| `lattice serve` | Web UI (live updates, search) |
| `lattice view <name>` | Render developer / product / business / c4 views |
| `lattice journey <brd>` \| `lattice actor list` | Journey and actor views |
| `lattice rtm` | Requirements traceability matrix with attestation header |
| `lattice agent <cap>` | LLM-backed capabilities (deterministic fallback) |
| `lattice mutation run` | Mutation-test invariant-enforcing code |
| `lattice structural-checks run` | Run structural invariant checks |
| `lattice next` | Rank the highest-value next actions by weakest link |
| `lattice results ingest <junit.xml>` | Ingest test results so the RTM can say *demonstrated* |
| `lattice lease acquire\|release\|list` | Claim work units in a multi-agent fleet |
| `lattice ledger [rebuild]` | Append-only attribution ledger of truth-level transitions |
| `lattice runs-clean` (alias `v0`) | Gate zero: clean install → build → boot → smoke probes |
| `lattice flag raise\|list\|clear` | Meaning flags: ride alongside green, humans clear |
| `lattice cr propose\|price\|decide\|reconverge` | Change requests against grounded intent, with demotions |
| `lattice backprop [--since <ref>]` | Code→docs scan: which grounded criteria a change touched |
| `lattice sweep` | Forbidden-move sweep: no verifier disappears without a CR |
| `lattice demonstrate` | QAE sign-off: every gate executed now, then ledgered |
| `lattice skills list` | List shipped agent skills |

Every command supports `--json` for machine-readable output; `--actor`
(or `LATTICE_ACTOR`) names the agent identity on leases and ledger entries.

## Web UI

`lattice serve` opens a dark-by-default, single-binary web UI (no build
pipeline, no node_modules) styled with the **Crystal brand system**:
cool slate (`#0F1218`) + mica violet (`#8C7DD6`), Geist + JetBrains Mono,
and the hexagonal unit-cell mark. Live updates via SSE: a CLI edit to any
manifest shows up on the next page render without a manual refresh.

Pages: **Overview**, **BRDs**, **Features**, **Entry points**,
**Coverage**, **Validation**, **Import**, **Config**.
Auth: localhost is open; non-loopback binds require either
`--token <…>` or `--basic-auth user:pass`.

## Documentation

- [User guide](docs/user-guide.md) — full workflow + wiring Lattice into an AI assistant
- [Annotation conventions](docs/annotations.md) — per-language annotation syntax
- [MCP setup](docs/mcp-setup.md) — connecting Lattice to Claude Desktop
- [Structural checks](docs/structural-checks.md) — authoring custom checks
- [Agent skills](docs/skills.md) — authoring team-specific skills
- [Known gaps](docs/known-gaps.md) — honest limits of the current engine
- [Changelog](CHANGELOG.md) — release notes

## Workspace layout

Every Lattice-maintained artifact lives under one `lattice/` directory:

```
lattice/
├── config.yaml, adapters.yaml, mcp.yaml, workspace.yaml
├── context.yaml         C4 actors + external systems
├── brds/                ⬡  Business Requirements Documents
├── features/            ▸  feature manifests
├── entry-points/        ⇌  accepted entry points
├── initiatives/         ◷  initiatives and tasks
├── decisions/           §  ADRs
├── revisions/           change requests against grounded intent
├── schemas/             locked contracts
├── skills/              shipped + custom agent skills
├── views/               view-template overrides
├── import/              brownfield import session
├── graph/               knowledge-graph shards (when sharding is enabled)
├── lattice.json         the knowledge graph (or shard index)
└── .cache/              gitignored: SCIP indexes, embeddings
```

`workspace.yaml` picks **embedded** mode (lattice/ inside one code repo) or
**standalone** mode (lattice/ as its own repo governing external code roots,
usable as a git submodule, and accessible to PMs/QA without code access).

## Architecture

The CLI is the canonical interface; the Go library (`pkg/lattice`) and the MCP
server are peers over the same JSON contract. Languages live only in adapters
(`pkg/lattice/adapters`) behind one interface; the core never imports a
parser. Entry points and BRDs are pluggable axes built on top of the same
extraction pass.

## License

[MIT](LICENSE).

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/brand/lattice-mark-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="assets/brand/lattice-mark-light.svg">
    <img alt="" src="assets/brand/lattice-mark-dark.svg" height="22">
  </picture>
  <br><sub><em>a substrate for software meaning</em></sub>
</p>
