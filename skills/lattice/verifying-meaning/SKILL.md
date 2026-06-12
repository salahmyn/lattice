---
name: verifying-meaning
description: Close the declared→wired→correctly-meant→demonstrated gap. Check SC↔invariant fidelity, spot vacuous invariants and tag-not-guard enforcers, and demonstrate scenarios.
---

# Verifying meaning

`lattice validate` proves a link is **declared and wired**. It does not prove
the link **means the right thing** or that the behaviour was **demonstrated**.
Four truth-levels sit on every chain — `declared → wired → correctly-meant →
demonstrated` — and the last two are yours to close by hand (and, in v0.8, with
new rules). Walk them in order.

## 1. Meaning fidelity: does the invariant *cover* the criterion?

For each `success_criterion` with `maps_to_invariant`, read the SC statement
and the invariant statement side by side and ask: **does the invariant, in
full, imply the criterion?**

- **Narrowing is the common failure.** Example: SC "ids are unique within a
  session, so complete(id) targets exactly one task" mapped to INV "two
  successful calls return distinct ids." Two-call distinctness does *not* imply
  session-wide uniqueness, and says nothing about "targets exactly one." The
  link is wired and *wrong*.
- **Fix by strengthening, not remapping.** Widen the invariant statement, or
  add invariants until their *union* covers the SC, then map the SC to the
  set. Never weaken the SC to match a convenient invariant.
- **A success_criterion with no `maps_to_invariant`, or a `user_scenario`
  with no `verified_by`, is unmeant until you link it** — orientation, not an
  error, but the next thing to do.

## 2. Is the invariant falsifiable here?

An invariant whose violating code path **cannot be reached** is enforced by
absence, not by a guard — it passes because the bad code does not exist yet,
not because anything prevents it.

- Ask: what concrete edit would violate this, and does the suite catch it?
  If nothing can violate it, rewrite it into something falsifiable or attach a
  **negative test** that would go red under the violating change.
- Mutation testing is the mechanical version of this check —
  `lattice mutation run` over the enforcers. A surviving mutant is an
  unfalsified invariant.

## 3. Enforcer = the guard, not the tag

The `@enforces` symbol must be **the code that rejects the violation**, not
every symbol in the file. A class + its constructor + a method all carrying
the same tag inflates "3 enforcers" while one line actually guards anything.

- Tag the branch/return/throw that does the rejecting.
- Pair every positive verifier with a negative one — proof it *fails closed*.

## 4. Demonstrated, not just declared

A scenario is demonstrated when a **passing** verifier exercises it **through a
declared entry point** — not when a pure-logic unit test exists.

- Tag the journey test `@verifies brd.<id>:US-N`; ensure the BRD scenario's
  `verified_by` names it and the entry point it drives.
- Ingest the result so Lattice sees the green/red, not just the wiring:
  `lattice results ingest <junit.xml>`. The RTM row should read
  `demonstrated`, and journey coverage should count the scenario.

## 5. Gate zero: the app must actually run

A green suite atop an app that won't start means the tests are not testing
the application — treat it as a critical finding, not a footnote.

- Run `lattice runs-clean` (alias `lattice v0`) after every slice and before
  reporting anything demonstrated: clean install → build → boot → smoke
  probes, from the workspace's `runtime:` config block.
- Nothing is `demonstrated` while V0 fails, no matter how green the suite is.

## Honesty about who checked

The RTM header carries the workspace's attestation level
(`autonomy.attestation`): `self` (you checked your own work — the header says
so), `isolated` (a different actor re-ran the checks), `bound` (CI ran them).
Never report claims above the configured level; if you authored the code and
the tests, say so and ask for an independent verification pass.

## What stays human

Equivalence judgement on **regulatory / legal / financial** criteria is
high-risk to automate — confirm those with a human owner, never let an LLM
equivalence check rubber-stamp them.

Related: [[achieving-goals-with-lattice]], [[authoring-manifests]], [[writing-annotations]].
