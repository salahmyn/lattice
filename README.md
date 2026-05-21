# Lattice

> A substrate for software meaning.

Lattice is a language-agnostic toolkit that treats software meaning as a
first-class, queryable, version-controlled substrate of a codebase. It captures
features, capabilities, invariants, dependencies, decisions, and work-in-flight
as structured artifacts that live alongside source code and are kept in sync
mechanically.

This repository is the **core engine**: a single static Go binary, `lattice`.
The MCP server is a separate package, [`@salahmyn/mcp-server`](lattice-mcp/).

Supported languages for extraction: **Python, TypeScript/JavaScript, PHP 8.0+**.

## Install

Build from source (Go 1.23+, a C toolchain for the tree-sitter bindings):

```sh
go build -o lattice ./cmd/lattice
./lattice version
```

## Quick start

```sh
# 1. Scaffold a repository.
lattice init

# 2. Write a feature manifest at features/checkout/refund.yaml:
#    id: checkout.refund
#    version: 1
#    status: production
#    purpose: Lets customers refund completed orders.
#    owners: { business: payments, engineering: checkout-eng }
#    invariants:
#      - id: INV-1
#        statement: A refund never exceeds the original charge.

# 3. Annotate the implementing code (Python shown):
#    @feature("checkout.refund")
#    @enforces_invariant("INV-1")
#    def validate_amount(order_id, amount, original): ...

# 4. Verify it with a test:
#    @verifies("checkout.refund:INV-1")
#    def test_refund_within_charge(): ...

# 5. Extract and validate.
lattice extract
lattice validate          # exits 0 when clean

# 6. Try an agentic capability (works without an LLM, too).
lattice agent narrate
```

A complete, validating three-language example lives in
[`examples/sample-project/`](examples/sample-project/).

## Commands

| Command | Purpose |
|---|---|
| `lattice init` | Scaffold the repository layout |
| `lattice doctor` | Check optional prerequisites |
| `lattice extract` | Build the knowledge graph (`lattice.json`) |
| `lattice validate` | Run every validation rule |
| `lattice analyze proposal <f>` | Conflict and impact analysis |
| `lattice patch --from-file <f>` | Preview/apply a typed artifact edit |
| `lattice view <name>` | Render developer / product / business views |
| `lattice agent <cap>` | LLM-backed capabilities (deterministic fallback) |
| `lattice mutation run` | Mutation-test invariant-enforcing code |
| `lattice structural-checks run` | Run structural invariant checks |
| `lattice skills list` | List shipped agent skills |

Every command supports `--json` for machine-readable output.

## Documentation

- [User guide](docs/user-guide.md) — full workflow + wiring Lattice into an AI assistant
- [Annotation conventions](docs/annotations.md) — per-language annotation syntax
- [MCP setup](docs/mcp-setup.md) — connecting Lattice to Claude Desktop
- [Structural checks](docs/structural-checks.md) — authoring custom checks
- [Agent skills](docs/skills.md) — authoring team-specific skills

## Architecture

The CLI is the canonical interface; the Go library (`pkg/lattice`) and the MCP
server are peers over the same JSON contract. Languages live only in adapters
(`pkg/lattice/adapters`) behind one interface — the core never imports a
parser.

## License

Apache-2.0 (intended).
