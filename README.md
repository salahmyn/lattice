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
  <img alt="go" src="https://img.shields.io/badge/go-1.26+-7FB6D0?style=flat-square&labelColor=0F1218">
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

Supported languages for extraction: **Python, TypeScript/JavaScript, PHP 8.0+,
Go**, plus opt-in PostgreSQL migrations and pgTAP tests (a SQL enforcer that a
declared database API role can execute is reported as `ENFORCER_EXPOSED`, info; see
`sql:` in the user guide; a second writer of the columns an enforcer writes is
reported as `UNGUARDED_WRITER`, info, and `lattice writers <table>[.<column>]` lists who
writes a table). A parameterized
TypeScript test (`test.each`, a template-literal title in a loop) is one test
whose report results join by its expanded names, so it passes only when every
run passed.

| For engineers | For product and leadership | For AI agents |
|---|---|---|
| Full context for any code change without re-reading the repo. | A queryable catalogue of features and a generated business narrative. | Pre-assembled context bundles and structured tools over MCP. |

## Install

Download a binary for Linux or macOS (on Windows, use the Linux one under WSL) from the
[latest release](https://github.com/salahmyn/lattice/releases/latest) and check it against `checksums.txt`.
A merge to `master` publishes a release when `CHANGELOG.md` names a new version.

Or build from source (Go 1.26+, a C toolchain for the tree-sitter bindings).
A source build reports its commit from the VCS information Go embeds:

```sh
go build -o dist/lattice ./cmd/lattice
./dist/lattice version
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
A BRD may list its `sources` (tickets, roadmap items, help pages, API docs,
ADRs); only a human marks a source checked.

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

### Acceptance cases

A long invariant pulls in many tests, and when one fails the report names a
paragraph, not a claim. An invariant may list its claims as `cases` (ids `a`,
`b`, …, kind `happy | edge | error | security`), and a test that checks one
says so with `@verifies wallet.fees:INV-1#a`; it still counts as a verifier of
`INV-1`. The audit computes each case's coverage (`covered`, `partial`,
`failing`, `uncovered`; only `covered` is positive) from the ingested results
and the break runs, and shows it as `1a` in `lattice audit`, `lattice feature
show` and `lattice rtm`. Cases are part of what a human confirms: editing one
voids the confirmation. `INVARIANT_BROAD` (info) names invariants with no
cases that make several claims: 60 words or more in two sentences, or in three
sentences or semicolon clauses, or 70 words or more however punctuated.

## Assurance: done right, not just done

A test that exists is not a test that works. For each change, Lattice asks
whether the promises it touches are still kept, and answers with one of three
verdicts. Unknown is never a pass.

```
  ASSURED   every touched promise is traced, confirmed, live and break-tested
  UNPROVEN  something is not shown yet (a gap); never drawn as a pass
  BROKEN    something fell: a break the base caught now survives, an enforcer
            or verifier was removed, the suite is red, a confirmed promise
            was edited
```

An honest refactor that moves enforcement does not read BROKEN: when a change
removes an enforcer of a tier 1 or 2 promise and adds another enforcer of it,
the removal reads `ENFORCER_MOVED`, and the change cannot merge until a break
of each added enforcer is killed at head (`lattice break`), so a move never
weakens enforcement silently.

- `lattice break` breaks each promise's enforcer in a temporary worktree and
  runs only its verifiers. A test that still passes is a **fake**.
- `lattice break --fails-on-base <rev>` runs a change's new or edited tests on
  the old code. A fix's test should fail there.
- `lattice audit` classifies every promise (fake, break-tested, exercised,
  demonstrated only, a gap) and names the enforcer a finding is about.
- `lattice check --base <rev>` gives the change one verdict and a merge rule:
  no new gap, no new untraced lines, tier 3 must be ASSURED. It also notes an
  enforcer that changed under an unchanged statement, and lines no promise
  guards.
- `lattice impact --since <rev> --tests` lists the tests that matter for a
  change, grouped by runner, so a pre-push run stays short. It also finds the
  tests behind lines no promise guards (coverage, call graph, or a neighbouring
  test file), then, as labelled hints, a test file that changed in the same
  small commits or names the symbol or its route. It lists only real tests,
  never a test file's helpers, and anything it can't place as `uncovered`.
- `lattice prepare` and `lattice confirm` make confirmation a human act that
  the platform vouches for. An agent can prepare a decision branch. Only a
  human's approval counts: an approving review by someone other than the
  author, or `lattice sign` with a hardware security key listed in
  `lattice/allowed_signers` and on the signer's GitHub account. A key added
  to `allowed_signers` counts only when a key already trusted endorses the
  change (`lattice sign --signers`). `confirm.routes: [signature]` in
  `lattice/config.yaml` makes the signature the only route that counts.

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
| `lattice doctor [--probe-llm]` | Check optional prerequisites and the preflight checks of `runtime:` and `break:` |
| `lattice extract` | Build the knowledge graph (`lattice.json`) |
| `lattice validate` | Run every validation rule |
| `lattice coverage` | Adoption report: discovery / docs / verification / BRD |
| `lattice brd new\|list\|show\|link\|approve` | Manage Business Requirements Documents; `brd link --reconcile` completes one-sided links without voiding approval |
| `lattice brd from-code <feature-id>` | LLM-regenerate a BRD from a feature (brownfield) |
| `lattice feature show <id>` | Show one feature + reverse links, with each invariant's cases, their coverage and their confirmation |
| `lattice feature spec <id>` | Emit the ≤500-word `.ai-spec.md` loading contract |
| `lattice ep list\|show\|accept\|reject` | Manage entry points, including the proposals `extract` makes from `serve` handlers, cron jobs and detected routes |
| `lattice initiative list\|show\|kanban\|critical-path` | Inspect work in flight |
| `lattice initiative add\|evidence\|status` | Record a proposal, attach its `evidence/v1`, move its status |
| `lattice import scan\|review\|inscribe` | Brownfield import session |
| `lattice analyze proposal <f>` | Conflict and impact analysis |
| `lattice patch --from-file <f>` | Preview/apply a typed artifact edit |
| `lattice serve` | Web UI (live updates, search); block map at `/map` (with assurance and layer lenses), views at `/views` |
| `lattice refresh` | Rebuild the block graph from the workspace's sources, judgments and features (`serve` and `export map` also rebuild it) |
| `lattice record list\|get\|put` | Authored records (judgments, initiatives, evidence, BRDs) in the store |
| `lattice store search\|relations\|export` | Query the stored block graph |
| `lattice export map\|views\|features` | Self-contained map (graph and audit inlined) or views page; Gherkin `.feature` files |
| `lattice view <name>` | Render developer / product / business / c4 views |
| `lattice journey <brd>` \| `lattice actor list` | Journey and actor views |
| `lattice rtm` | Requirements traceability matrix with attestation header, the criteria figure, and a second figure, `invariants N/M required`; `lattice validate` adds `INVARIANT_UNREQUIRED`, `STATUS_DRIFT` and `FEATURE_UNREACHED` for unpromoted features (all info) when the spec drifts from the code |
| `lattice check --base <rev>` | One verdict for a change (ASSURED / UNPROVEN / BROKEN) and the merge rule; notes `break.runners` preflight problems without changing the verdict |
| `lattice impact --since <rev> [--tests]` | What a change touches, the acceptance cases included; `--tests` lists the verifier tests to run, by runner |
| `lattice break [--promise f:INV-n[#case]] [--edges] [--lines] [--fails-on-base <rev>] [--fail-on survived,unknown]` | Break each enforcer and see whether its verifiers notice; `--lines` also records which lines each promise's own verifiers run (each baseline runs with coverage, which a runner asks for with `{coverage}`); `--promise f:INV-n#a` breaks one case against its tagged tests; `--edges` (or `break.edges: true`) also breaks each resolved hop on a promise's slice paths at its out end (a request path, an rpc, table or channel name gets `-lattice-break` appended) and reports `EDGE_BREAK_SURVIVED` when no verifier notices; a runner can run Playwright browser journeys as the verifiers (against the dev server; see the user guide); `--fails-on-base` runs a change's new or edited tests on the old code. Exits 0 whatever the outcomes unless `--fail-on` lists one a shown run has (exit 1, `BREAK_OUTCOME`); says so when no break ran. Runs each identical baseline command once per tree and counts the reused ones. Reuses a killed or survived outcome only on the same tree, or across commits when the runners declare `inputs`. One run per repository at a time (`lattice/.cache/results/break.lock`; a second fails with `BREAK_LOCKED`). A SQL break goes on the statement that sets the object's effective definition (a policy's last `ALTER POLICY` that sets `USING`, else its `CREATE`; a named `CHECK` constraint becomes `CHECK (true OR (expr))`), and its runner may name `{patched_sql}` and `{original_sql}` to apply only the broken object instead of resetting the database; the swap reads unavailable when a later statement touches the object. `break.restore` commands run after each swap run and re-run `break.setup` if one fails; a baseline that fails after a swap is retried once on a fresh setup. Preflight first |
| `lattice audit [--promise f:INV-n] [--lines]` | Classify every promise: fake, break-tested, exercised, a gap; each case covered, partial, failing or uncovered; each break-tested promise also gets an entry-point proof (`entrypoint`, `none` or `unknown`): whether a verifier that killed a break ran the enforcer through an entry point's handler, with `BREAK_NO_ENTRYPOINT` (info) in `lattice validate` for `none`; `--lines` adds each block's intent (enforces, serves, shared, none) and how many of its lines its own verifiers ran, from `lattice break --lines` (`{coverage}` in a runner command); a line with no data reads unknown; `coverage.floors.verified_lines` turns on `VERIFIED_LINES_BELOW_FLOOR` |
| `lattice slice <f:INV-n>` | Which ways into the system reach the code that keeps a promise, hop by hop, with the layers each path is carried by; a client `fetch` joins the route that serves it as an `http` hop when both ends are extracted and their keys are equal; a queue or `NOTIFY` joins its publisher to its consumers through a channel node as `publish` and `consume` hops, counted as `async_hops` and never counted as live reach; unknown hops are marked, and a slice with one is never complete; with per-test coverage, a hop is drawn `=kind=>` when one verifier's own run executed symbols on both sides of it, and each verifier is listed with the layers its run covered; a hop an edge break killed (`lattice break --edges`) is marked `kind*` and `break_tested` |
| `lattice writers <table>[.<column>]` | Who writes a table or column, from the migrations: SQL functions (`INSERT`, `UPDATE`, `DELETE`, `MERGE`, `NEW.col` in a trigger function), migrations that run DML, and roles holding a table privilege, each with its file and line and the promises it is guarded or unguarded for; ends with why the writer set is incomplete (a dynamic `EXECUTE`, a write through a view, a foreign-key action) or says it is complete; `--json` prints `lattice.writers/1`; `lattice validate` reports an unguarded writer as `UNGUARDED_WRITER` and an unknown set as `WRITERS_UNKNOWN`, both info |
| `lattice prepare brd\|promise\|waiver …` | Prepare a decision branch for a human to approve; never pushes |
| `lattice confirm [--offline] [--unsigned]` | Which decisions a human confirmed: an approving review on the merged PR, or a hardware-key signature; `--unsigned` lists what a signature could re-confirm, with the commands |
| `lattice sign <decision>` \| `--signers` | A human signs a decision, or endorses a change to `allowed_signers`, with an SSH security key (`ed25519-sk`); refuses CI, agents and software keys |
| `lattice agent <cap>` | LLM-backed capabilities (deterministic fallback) |
| `lattice mutation run` | Mutation-test invariant-enforcing code |
| `lattice structural-checks run` | Run structural invariant checks |
| `lattice next` | Rank the highest-value next actions by weakest link |
| `lattice results ingest <junit.xml>...` | Ingest test results (unit and e2e reports of one run, together) so the RTM can say *demonstrated*; lists the failed tests with their `@verifies` refs |
| `lattice lease acquire\|release\|list` | Claim work units in a multi-agent fleet |
| `lattice ledger [rebuild\|runs]` | Append-only ledger of decisions and state changes (gates, CRs, flags, sign-offs, transitions); `runs` shows the latest check runs from the untracked cache |
| `lattice runs-clean` (alias `v0`) | Gate zero: clean install → build → boot → smoke probes. Preflight first |
| `lattice flag raise\|list\|clear` | Meaning flags: ride alongside green, humans clear |
| `lattice cr propose\|price\|decide\|reconverge` | Change requests against grounded intent, with demotions |
| `lattice backprop [--since <ref>]` | Code→docs scan: which grounded criteria a change touched |
| `lattice sweep` | Forbidden-move sweep: no verifier disappears without a CR |
| `lattice demonstrate` | QAE sign-off: every gate executed now, then ledgered. Preflight first when V0 runs |
| `lattice skills list` | List shipped agent skills |

Every command supports `--json` for machine-readable output; `--actor`
(or `LATTICE_ACTOR`) names the agent identity on leases and ledger entries.

`runs-clean`, `demonstrate` and `break` run a preflight first, in seconds and
without running a test. It checks only what the config declares: the
executables of the `runtime:` and `break:` commands are on PATH, each runner's
`dir` exists, a Node minimum in `package.json` `engines.node` is met, the
Docker daemon answers for a command that runs `docker`, each
`runtime.services` / `break.services` address (`host:port`) answers on TCP,
and, for `break`, the tree is clean. It lists every problem at once, each
with a code, the config key and a fix (`--json`: `PREFLIGHT_FAILED` with
`problems`), and exits 2, the setup-error code, before any step runs.
`check` runs no test, so it only notes the problems of `break.runners` (the
report's `preflight` field, in text and Markdown too) and keeps its verdict,
`merge_allowed` and exit code. `--no-preflight` skips the checks;
`lattice doctor` lists the same checks.

## Block maps (0.9, preview)

Declare a workspace's sources in `lattice/workspace.yaml` (repos pinned to
commits, each publishing `catalogue/v1` files), add judgments for what the
catalogues cannot say, and run `lattice refresh`. The result is one graph
of blocks and edges, stamped with the commit and release each source
reflects. Every feature and invariant is a block, a dotted feature nested
in the one its id extends (`orders.refunds` in `orders`). `lattice serve`
shows it at `/map`; `lattice export map` writes the same page as one file
that needs no network (its layout library, elkjs, is inlined; see
[third-party notices](THIRD_PARTY_NOTICES.md)). Both rebuild the graph
first, as `refresh` does, and save it when it changed; when it cannot be
rebuilt they draw the stored one and say why (`export map --no-refresh`
skips the rebuild). Storage is files by default, or PostgreSQL
(`storage: {backend: postgres}`) for search and relation queries. The
map's first view follows the window's shape: loosely linked blocks are
packed in rows rather than stacked in one tall column.

The map's **Assurance** toggle (`a`) colours each block by the audit
class of the promises it holds, the weakest by the audit's own order:
fake, a gap, unknown, demonstrated only, exercised or break-tested. The
classes, the order and the colour of each come with the map's data
(`/api/graph`, or inlined by `export map`); the page computes none of them.
Only break-tested and exercised are green. A promise with no audit row, or
an export whose audit could not run, reads unknown (hatched grey).

## Web UI

`lattice serve` opens a dark-by-default, single-binary web UI (no build
pipeline, no node_modules) styled with the **Crystal brand system**:
cool slate (`#0F1218`) + mica violet (`#8C7DD6`), Geist + JetBrains Mono,
and the hexagonal unit-cell mark. It works offline: the stylesheet, the
fonts and the Mermaid renderer for flow diagrams are served from the
binary, and the pages load nothing from another origin
([third-party notices](THIRD_PARTY_NOTICES.md)). Live updates via SSE: a
CLI edit to any manifest shows up on the next page render without a manual
refresh.

Pages: **Overview**, **BRDs**, **Features**, **Entry points**,
**Coverage**, **Validation**, **Import**, **Config**.
Auth: localhost is open; non-loopback binds require either
`--token <…>` or `--basic-auth user:pass`.
The map and `/api/v2` reuse the last graph and audit until any input
changes (`X-Lattice-Cache: hit|miss|bypass`).

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
├── lattice.json         gitignored: the knowledge graph (or shard index), rebuilt by every extract
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

[MIT](LICENSE). Bundled third-party code is listed in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/brand/lattice-mark-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="assets/brand/lattice-mark-light.svg">
    <img alt="" src="assets/brand/lattice-mark-dark.svg" height="22">
  </picture>
  <br><sub><em>a substrate for software meaning</em></sub>
</p>
