# Authoring agent skills

Agent skills are markdown folders that external AI agents load to use Lattice
expertly without re-deriving how it works. Lattice **ships** skills; it never
"runs" them — an agent host decides which to load.

## Shipped skills

Eight skills ship with the binary and are copied into `lattice/skills/lattice/`
on `lattice init`, so they version-control with the project:

```sh
lattice skills list
lattice skills export lattice/authoring-manifests --out ./tmp
```

| Skill | Purpose |
|---|---|
| `authoring-manifests` | Writing well-formed manifests |
| `writing-annotations` | Per-language annotation conventions |
| `proposing-changes` | The proposal lifecycle |
| `working-tasks` | Picking up and scoping tasks |
| `refactoring-with-lattice` | Keeping annotations in sync during refactors |
| `diagnosing-violations` | Interpreting validation output |
| `initiative-coordination` | Decomposing work into streams |
| `decision-records` | When and how to write ADRs |

## Skill structure

Each skill is a folder:

```
lattice/skills/lattice/authoring-manifests/
├── SKILL.md            # the primary, operational instructions
├── examples/           # optional worked examples
└── reference/          # optional deep-dive material, loaded on demand
```

`SKILL.md` starts with a YAML frontmatter header and stays short and
operational (ideally under 1000 words):

```markdown
---
name: authoring-manifests
description: How to write a well-formed Lattice feature manifest.
---

# Authoring manifests

...
```

## Custom skills

Add team-specific skills under `lattice/skills/<org>/<skill-id>/`. They ship
with the repo and are listed alongside the built-in skills — useful for
codifying conventions like "how we name capabilities at Acme Corp".

The `lattice_get_agent_context` tool includes a `relevant_skills` array that
suggests which skills an agent should load for the current task.
