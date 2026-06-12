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

## Demonstration & meaning codes (v0.8)

- `VERIFIER_FAILING` — an ingested result for a `@verifies` test is red.
  Fix the code (or the test's expectation via the proper flow), re-run,
  re-ingest.
- `BRD_SCENARIO_UNVERIFIED` / `SCENARIO_NO_ENTRYPOINT` — the scenario lacks a
  resolving verifier / is verified only below its entry point. Add a journey
  test through the real trigger.
- `ENFORCER_NOT_GUARD` / `INVARIANT_UNFALSIFIABLE` /
  `CRITERION_INVARIANT_NARROWER` — meaning-fidelity signals; see
  [[verifying-meaning]]. Fix by strengthening, never by remapping.

## Governance codes (v0.8.1)

- `CRITERION_FLAGGED` (info) — an open meaning flag on the unit. Not yours to
  silence: a human clears it (`lattice flag clear <unit> --by <human>`) after
  re-verification. Do the re-verification; do not edit the flag store.
- `MUTATION_REQUIRED_TIER` (warning) — a tier-2+ criterion chain has no
  mutation evidence. Run `lattice mutation run` against the feature; at
  tier 2+ a verifier must be shown to constrain the enforcer.
- `TEST_RETIRED_ILLEGALLY` (warning, from `lattice sweep`) — a baselined
  verifier disappeared with no approved CR retirement item. Restore the test,
  or route the descoping through `lattice cr` (narrowing class).
- `AUTHOR_NOT_SEPARATED` (info) — one actor wired and demonstrated the unit.
  Request an independent review pass or adversarial verifier extension.
- `UNATTRIBUTED_CHANGE` / `LEASE_SCOPE_OVERLAP` — fleet hygiene: set
  `--actor` (or `LATTICE_ACTOR`) and respect live leases.

## next_action kinds

`add_annotation`, `add_invariant`, `add_capability`, `add_structural_check`,
`create_manifest`, `edit_manifest`, `strengthen_tests`, `run_command`. Each
carries the fields needed to act (e.g. `ref`, `annotation`, `suggested_files`).
