---
name: working-tasks
description: How to pick up a task, read the agent context bundle, and handle blocked dependencies.
---

# Working tasks

Tasks live under `work/initiatives/<id>/tasks/`. Each belongs to a stream of
an initiative.

## Picking up a task

1. `lattice agent context --task <id>` returns a self-contained bundle: the
   task, the manifests it touches, the invariants to preserve, the current
   code, the verifying tests, related decisions, and the skills to load.
2. You do **not** need to re-analyze the repository — the bundle is sufficient
   to act.
3. In a multi-agent workspace (v0.8): identify yourself (`--actor` or
   `LATTICE_ACTOR`) and claim the unit before editing —
   `lattice lease acquire <unit>`; release on handoff. `lattice next` ranks
   the weakest links when you have no assignment.

## Task status

Status is normally **derived from git**: branch existence, PR state, CI, and
mutation thresholds. Do not hand-edit `status` unless `status_source` is
`manual` (off-PR work).

## Dependencies

- A task may `depends_on` another task or a `contract` (a locked schema).
- If a dependency is unfinished, the task is `blocked`. Do not start blocked
  work; pick another task whose dependencies are satisfied.
- `unblocks` is computed — it lists the tasks that this task gates.

## Scoping to a stream

Stay within your task's `stream`. Cross-stream changes belong to a different
task and risk colliding with parallel work.
