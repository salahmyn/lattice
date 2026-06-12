---
name: lattice-extract
description: Generate Lattice BRD/feature/story artifacts from existing code via the reverse chain. Use when asked to reverse-engineer BRDs, extract business intent, or scaffold a PRD/story chain from a feature.
---

# lattice-extract

Encodes the conventions for generating Lattice knowledge-graph artifacts so they
don't have to be restated each time. See `CLAUDE.md` → *Lattice Conventions* for
the authoritative rules.

## Before generating anything

1. **Restate the conventions you will follow** for (a) naming, (b) link
   direction, (c) defaults — then wait for confirmation. Misalignment here has
   caused full redos.
2. **Default to the reverse chain**: code → feature → BRD, unless the user
   explicitly asks to author forward (intent-first).
3. For a **novel design**, produce a concrete YAML *prototype chain* on one
   example feature for the user to critique **before** writing implementation
   code.

## Link model (must be exact)

- Feature → BRD: `implements_brd: <brd-id>` (reverse link, at most one).
- BRD → Features: `implements_via: [<feature-id>, ...]` (forward link, many).
- An `implements_via` entry naming a non-existent feature is a hard error
  (`BRD_PHANTOM_FEATURE`) — create the feature first or drop the reference.

## BRD rules

- Reverse-engineered BRDs use `provenance.source: llm_from_code` and **must
  stay `status: draft`**. They require a human approval pass
  (`lattice brd approve <id> --by <email>`) — never set `approved` yourself.
  Validation enforces this via `BRD_UNAPPROVED_LLM`.
- Lifecycle is exactly: `draft → proposed → approved → superseded`.
- Do **not** fabricate `regulatory` / `legal` / `financial` constraints — those
  must come from a human. Inventing them is high-risk.
- Preserve canonical YAML field order (struct order); don't reorder keys.

## Story / PRD layer (design-phase — not in code yet)

When asked to work on stories/PRDs: this layer is not yet implemented. Confirm
the field name and naming convention with the user first (do not coin new
names), require every entry point to declare which story it fulfills, and keep
the reverse-chain default.

## CLI you'll typically use

- `lattice brd from-code <feature-id>` — LLM-regenerate a BRD from feature
  evidence (supports `--dry-run`).
- `lattice extract` / `lattice validate` — build and check the graph.
- `lattice brd link <brd-id> <feature-id>` — add a forward link.

## After generating

Run `lattice validate` and resolve any `BRD_*` violations before declaring
done. Surface unresolved `BRD_UNAPPROVED_LLM` / `BRD_PHANTOM_FEATURE` to the
user rather than silently working around them.
