# Changelog

All notable changes to Lattice are documented in this file.

## [0.1.1]

### Added
- **Consolidated `lattice/` directory.** Every Lattice-maintained artifact —
  manifests, initiatives, tasks, decisions, schemas, config, skills, and the
  knowledge graph — now lives under one visible `lattice/` directory. The
  gitignored runtime cache moved to `lattice/.cache/`.
- **`lattice migrate`** moves a v0.1.0 flat-layout repository into the new
  `lattice/` layout.
- **Knowledge-graph sharding.** With `knowledge.sharding.enabled`, `lattice.json`
  becomes a shard index and per-group graphs are written to `lattice/graph/`.
  Strategies: `by_feature_group` and `by_size`. `graph.Load` transparently
  reassembles either form.
- **Workspace model.** A `lattice/workspace.yaml` selects `embedded` (lattice/
  inside one code repo) or `standalone` (lattice/ as its own repo governing
  external code roots) — supporting multi-repo projects and use as a git
  submodule.
- **Review mode.** When no code root is accessible, Lattice runs manifest-only
  validation (schema, dependency, initiative/task integrity), deferring the
  code-coupled checks. Auto-detected, or forced with `--review`. This lets
  PMs/QA interact with Lattice without access to code.
- **C4 architecture view.** `lattice view c4` emits all three C4 levels —
  System Context, Container, and Component — as Mermaid diagrams, or as a
  Structurizr DSL workspace with `--format structurizr`. Containers come from
  code roots, components from top-level feature groups, relationships from
  `depends_on` and surface emit/consume pairs.
- **`lattice/context.yaml`** declares the C4 Level-1 elements that code cannot
  reveal — people (actors) and external systems — so the System Context
  diagram is first-class rather than inferred.

### Changed
- The flat layout (`features/`, `work/` at the repo root) is no longer read;
  run `lattice migrate` to upgrade an existing repository.
- SCIP indexes and embeddings now live under `lattice/.cache/`.

## [0.1.0]

### Added
- Initial implementation: Go core engine and TypeScript MCP server — schema,
  CLI, tree-sitter adapters (Python, TypeScript, PHP), extract/graph/validate
  pipeline, patch engine, conflict/impact analyzer, SCIP and mutation
  orchestration, agentic capabilities, views, shipped agent skills.
