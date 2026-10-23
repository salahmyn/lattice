# Contributing

## Branch names

Every branch pushed to this repository is named `<type>/<topic>`:

```
feat/v0.9-blocks
fix/e2e-playwright-traceability
ci/build-workflow
docs/branch-naming
```

- **type** says what kind of change the branch carries. Use one of:

  | type | for |
  |---|---|
  | `feat` | a new capability (engine, CLI, UI, MCP) |
  | `fix` | a bug fix |
  | `docs` | documentation only |
  | `refactor` | a code change with no behaviour change |
  | `perf` | a performance improvement |
  | `test` | tests only |
  | `ci` | workflows under `.github/` and `ci-templates/` |
  | `build` | build, release or dependency changes |
  | `chore` | anything else that ships nothing (cleanup, tooling) |
  | `rnd` | design notes and experiments under `docs/rnd/` |

- **topic** names the change, not the person or the tool making it:
  lowercase, words joined with `-`, short enough to read in a list
  (aim for 2–5 words). A version may lead the topic when the branch is
  that release's work (`feat/v0.9-blocks`).
- One branch, one change. A branch that would need two types is two
  branches.

Not allowed on the remote:

- generated or placeholder names (`worktree/upbeat-pasteur-sqmuh8`,
  `claude/…`, `tmp`, `test2`, `wip`);
- personal prefixes (`salah/…`) and ticket-only names (`VPE-123`);
  put a ticket id after the topic if one exists
  (`fix/refund-rounding-vpe-123`).

A branch created by an agent or a worktree tool must be renamed to this
form before it is pushed:

```sh
git branch -m <type>/<topic>
```

Dependabot's own `dependabot/…` branches are the only exception.

## Commits

Subjects are short and say what changed. Prefix with the area touched,
`area: …` (`views: …`, `blocks: …`) or `type(area): …`
(`fix(typescript): …`); keep the same style within a branch.
