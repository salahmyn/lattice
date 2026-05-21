# Authoring structural checks

Some invariants are enforced by code *structure* rather than by a per-symbol
annotation — type rules, import constraints, custom AST checks. Lattice runs
these as **subprocesses**: a structural check is any executable in any
language.

## The contract

A structural check **reads** a JSON object on stdin:

```json
{
  "scope": {
    "modules": ["src/checkout/refund/settlement/"],
    "files": ["src/**/*.py"]
  },
  "config": {},
  "repo_path": "/abs/path/to/repo"
}
```

It **writes** a JSON object on stdout:

```json
{
  "violations": [
    { "file": "src/checkout/processor.py", "line": 47,
      "message": "Uses raw float where Money is expected" }
  ]
}
```

It **exits** 0 on success (with or without violations), non-zero on internal
error. A check that exceeds the configured timeout is reported as
`STRUCTURAL_CHECK_TIMED_OUT`.

## Declaring a check

In the manifest of the feature whose invariant the check verifies:

```yaml
structural_checks:
  - id: no-raw-money-types
    command: ["python3", "tools/check_money.py"]
    verifies_invariants:
      - INV-1
    scope:
      modules:
        - src/checkout
    config: {}
```

The `command` may be in any language — Python, Node, Go, a shell script.
Lattice spawns it, pipes JSON, and parses JSON.

## Running checks

```sh
lattice structural-checks list      # show declared checks
lattice structural-checks run       # run them, exit non-zero on violations
```

If an invariant declares `verifiable_by: [structural]` but no structural check
covers it, validation reports `STRUCTURAL_CHECK_MISSING`.

## Security

Structural checks execute user-provided code in the same trust boundary as
`tests/`. `lattice validate --no-structural-checks` disables them; any
structural-only-verified invariant is then reported as `UNVERIFIED_INVARIANT`.
For maximum-trust CI, run checks in a separate restricted-permission step.
