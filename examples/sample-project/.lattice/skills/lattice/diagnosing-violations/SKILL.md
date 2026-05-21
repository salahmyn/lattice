---
name: diagnosing-violations
description: What each validation rule means and how to act on next_action fields.
---

# Diagnosing violations

`lattice validate` emits structured violations. Every violation has a `code`,
a `severity`, a `message`, a `location`, and — for actionable ones — a
`next_action`. Branch on `next_action.kind`, never on the prose.

## Common codes

- `ORPHAN_ANNOTATION_FEATURE` — code references a feature with no manifest.
  Fix: create the manifest or correct the annotation.
- `UNENFORCED_INVARIANT` — an invariant has no `@enforces_invariant`.
  Fix: annotate the enforcing symbol (`next_action.kind = add_annotation`).
- `UNVERIFIED_INVARIANT` — no test `@verifies` the invariant. Add one.
- `STRUCTURAL_CHECK_MISSING` — an invariant declares `verifiable_by:
  structural` but no `structural_check` covers it.
- `MUTATION_SCORE_BELOW_THRESHOLD` — the test suite kills too few mutants.
  Strengthen the verifying tests, do not lower the threshold.
- `SUPPRESSION_WITHOUT_REASON` — a suppression has no `reason:`. Add one.
- `DEPENDS_ON_CYCLE` / `TASK_DEPENDENCY_CYCLE` — break one edge in the cycle.

## next_action kinds

`add_annotation`, `add_invariant`, `add_capability`, `add_structural_check`,
`create_manifest`, `edit_manifest`, `strengthen_tests`, `run_command`. Each
carries the fields needed to act (e.g. `ref`, `annotation`, `suggested_files`).
