---
name: refactoring-with-lattice
description: When refactors need manifest changes and how to keep annotations in sync during code moves.
---

# Refactoring with Lattice

A refactor changes code structure without changing meaning. Most refactors
need **no** manifest change — but the annotations must move with the code.

## When the manifest does NOT change

Renaming a function, splitting a module, extracting a helper: the feature,
capabilities, and invariants are unchanged. Just carry the annotations to the
new symbol locations.

## When the manifest DOES change

- A capability genuinely disappears or merges → `RemoveCapability` /
  `ModifyCapability`.
- An invariant is no longer meaningful → `RemoveInvariant` (rare; prefer to
  keep invariants).
- A surface moves or changes shape → `ModifySurface`, possibly with
  `breaking_change_from`.

## Keeping annotations in sync

After moving code, run `lattice extract` and then `lattice validate`. The
common failures are:

- `ORPHAN_ANNOTATION_*` — an annotation survived a move but its manifest
  reference is stale.
- `UNENFORCED_INVARIANT` — the enforcing symbol was moved or renamed and lost
  its `@enforces_invariant`.

Fix by re-attaching the annotation to the new symbol, not by editing the
manifest.
