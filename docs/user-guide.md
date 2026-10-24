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
CGO_ENABLED=1 go build -o lattice ./cmd/lattice
sudo mv lattice /usr/local/bin/        # or anywhere on PATH
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
| **Annotation** | A decorator / JSDoc tag / PHP attribute linking code to a manifest. |
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

Link the implementing code to the manifest. (Python / TypeScript / PHP — see
[annotations.md](annotations.md).) TypeScript example:

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
  require_level: demonstrated   # or verified
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

## 5. Day-to-day commands

```sh
lattice extract                       # refresh the knowledge graph
lattice validate                      # check everything
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
lattice patch --from-file p.json --preview|--apply
lattice initiative show <id>           # initiative + tasks
lattice task pick-next                 # next actionable task
lattice agent context --task <id>      # full bundle for one task
lattice mutation run                   # mutation-test invariant code
lattice structural-checks run          # run custom structural checks
lattice skills list                    # shipped agent skills
```

Add `--json` to any command for machine-readable output.

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
