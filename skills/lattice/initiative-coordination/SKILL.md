---
name: initiative-coordination
description: How to decompose work into streams, lock contracts, and choose parallel versus sequential streams.
---

# Initiative coordination

An initiative is a coordinated piece of work that proposes manifest changes
and is decomposed into streams and tasks.

## Streams

A **stream** is a parallel work track (e.g. `backend`, `frontend`). Put tasks
that can proceed independently in different streams; put tasks that must be
sequenced in the same stream or wire explicit `depends_on` edges.

- Use **parallel streams** when the work touches disjoint code and the
  interface between them is settled.
- Use a **sequential** chain when one task's output is another's input and the
  interface is still moving.

## Contracts

A **contract** is a locked schema or spec that tasks depend on. Lock a contract
(`LockContract`) once its shape is agreed — this lets dependent streams start
against a stable interface. A task may `depends_on` a `contract` path; that
path must appear in the initiative's `contracts`.

## Tasks and the critical path

Each task declares `produces`, `depends_on`, `verifies`, and `suitability`
(agent-autonomous, agent-pair, human-only). The critical path is computed from
the task graph — surface it with `lattice initiative critical-path <id>`.
