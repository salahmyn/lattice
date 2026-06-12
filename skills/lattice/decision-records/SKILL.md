---
name: decision-records
description: When to write an ADR, how to structure it, and how ADRs link to manifests.
---

# Decision records

An Architecture Decision Record (ADR) captures a decision, its context, and
its consequences. ADRs live under `decisions/`.

## When to write one

Write an ADR when a choice is hard to reverse, affects multiple features, or
will be questioned later: a migration strategy, a contract shape, a dependency
direction, a suppression of an invariant across a subtree.

Do **not** write an ADR for routine, easily-reversed implementation choices.

## Structure

- **Title** — `ADR-NNNN: short imperative summary`.
- **Status** — proposed | accepted | superseded.
- **Context** — the forces and constraints in play.
- **Decision** — what was chosen, stated plainly.
- **Consequences** — what becomes easier and what becomes harder.

## Linking to manifests

Reference an ADR from a manifest's `decisions` list as `{adr, summary}`. The
conflict analyzer can check a proposal against linked ADRs for contradiction.
A task may list `related_decisions` so an agent picking up the task sees the
reasoning behind the code it is about to change.

## Superseding (v0.8.1)

A decision is never deleted or edited away — it is **superseded**, and only
through the CR flow. A change request that contradicts a recorded decision is
classed `contradiction` and stays blocked until the human approval names the
retired record: `lattice cr decide CR-<n> --approve --supersedes-decision
ADR-NNNN --by <human>`. Mark the old ADR `superseded` with a pointer to its
successor; the original rationale stays readable so the idea isn't
re-litigated from zero. Agents never supersede decisions on their own — it is
part of the non-delegable floor, outside any mandate.
