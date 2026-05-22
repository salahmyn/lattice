# Lattice sample project

A small three-language project that exercises every Lattice feature. Use it to
see what a Lattice-managed repository looks like and to verify a build.

All Lattice-maintained artifacts live under `lattice/`; the source code
(`src/`, `tests/`, `tools/`) stays at the project root.

## What it contains

- **Features** — `checkout` (parent), `checkout.refund` (Python), `wallet`
  (TypeScript), `billing` (PHP), under `lattice/features/`.
- **Annotated source** under `src/` in all three languages.
- **Verifying tests** under `tests/`.
- **An initiative** — `refund-async-revamp` with two tasks and a locked
  contract, under `lattice/initiatives/`.
- **A decision record** — `lattice/decisions/ADR-0001-async-refunds.md`.
- **A proposal** —
  `lattice/features/checkout/refund/proposals/wallet-destination.yaml`.
- **A structural check** — `tools/check_money.py`.

## Try it

From the repository root, with `lattice` built:

```sh
lattice --repo examples/sample-project extract
lattice --repo examples/sample-project validate          # exits 0 — clean
lattice --repo examples/sample-project view developer
lattice --repo examples/sample-project analyze proposal \
  examples/sample-project/lattice/features/checkout/refund/proposals/wallet-destination.yaml
lattice --repo examples/sample-project agent narrate
```
