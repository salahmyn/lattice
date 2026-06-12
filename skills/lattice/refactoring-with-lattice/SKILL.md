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

## After the refactor merges (v0.8.1)

- Run `lattice backprop` — it classifies the change against grounded intent.
  A pure refactor should come back **"no doc impact"** (a ledgered outcome,
  not silence). If it lists affected criteria, the change wasn't a pure
  refactor: route it to the amendment gate or a CR.
- Never drop a verifier test during a refactor. `lattice sweep` compares the
  verifier inventory against its baseline; a disappeared verifier without an
  approved CR retirement item is `TEST_RETIRED_ILLEGALLY`. Moving a test is
  fine — re-run `lattice sweep --update-baseline` once validate is clean and
  the moved test is re-linked.
