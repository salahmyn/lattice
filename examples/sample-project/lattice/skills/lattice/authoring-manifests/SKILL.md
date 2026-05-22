---
name: authoring-manifests
description: How to write a well-formed Lattice feature manifest, and when to extend versus create.
---

# Authoring manifests

A manifest is one YAML file under `features/` declaring exactly one feature.

## Required fields

- `id` — lowercase, dot-separated (`checkout.refund`). Dots denote sub-features.
- `version` — integer ≥ 1, never decreasing across git history.
- `status` — `proposal | accepted | production | deprecated`.
- `purpose` — a human-readable description of what the feature does.
- `owners` — `business` and `engineering` team slugs.

## Capabilities and invariants

- A **capability** is a named behavior with at least one prose `rule`.
- An **invariant** is a constraint the feature must always satisfy. Give it an
  `INV-N` id and a precise `statement`.
- Every invariant needs an enforcer (`@enforces_invariant` in code) and a
  verifier (a `@verifies` test) or `lattice validate` will fail.

## Extend vs. create

- **Extend** an existing feature when the change is a new behavior of the same
  unit of meaning. Bump `version`.
- **Create** a new feature (or sub-feature with a dotted id) when the change is
  a distinct unit of meaning with its own owners.

## Good practice

- Keep `purpose` concrete and free of jargon — it feeds the business view.
- Never hand-edit `implementations`, `verifications`, `children`, or
  `mutation_scores`; those are auto-populated.
- Edit manifests with `lattice patch`, not by hand, so output stays canonical.
