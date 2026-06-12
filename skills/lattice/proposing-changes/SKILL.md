---
name: proposing-changes
description: The proposal lifecycle and how to read the conflict analyzer's output.
---

# Proposing changes

A proposal is a manifest with `status: proposal`, kept under a
`proposals/` directory so it is not part of the live corpus.

**Scope note (v0.8.1):** proposals change the *feature/architecture* layer.
Changing **grounded business intent** — an approved BRD's criteria, scope,
tiers, or a recorded decision — is a different flow: `lattice cr
propose|price|decide` (see [[verifying-meaning]]). If your proposal would
make a grounded criterion false or narrower, open a CR first; the feature
proposal then implements whatever the CR approves.

## Lifecycle

1. Draft the proposal manifest (or use `lattice agent draft-proposal`).
2. Run `lattice analyze proposal <path>` to get an impact report.
3. Resolve every item under "resolutions required".
4. Promote: set `status` to `accepted`, then `production`.

## Reading the impact report

- **Deterministic findings** are reliable graph facts: surface collisions,
  dependency cycles, breaking surface changes. Treat errors as blocking.
- **Semantic findings** are embedding-based similarity hints. They are
  advisory — review each one; high similarity may mean genuine duplication
  or just related wording.
- **Open invariant requirements** list what each new invariant still needs
  before the feature can reach `production`.

## Split vs. extend

If a proposal adds capabilities that belong to a different unit of meaning,
split it into its own feature. If the conflict analyzer reports capability
overlap with an unrelated feature, that is a signal to reconsider scope.
