---
name: writing-annotations
description: Per-language annotation conventions and when to use module, role, or symbol scope.
---

# Writing annotations

Annotations link code to manifest concepts. The effective annotation set of a
symbol is the union of its own annotations, module-level annotations,
role-based annotations, and inherited base-class annotations.

## Per language

- **Python** — decorators imported from the no-op `lattice` package:
  `@feature("checkout.refund")`, `@enforces_invariant("INV-1")`.
- **TypeScript** — JSDoc tags: `@feature checkout.refund`, `@enforces INV-1`.
  Module-level tags go in the first JSDoc block of the file.
- **PHP** — PHP 8 attributes: `#[Feature('checkout.refund')]`. No docblock
  fallback.

## Scope

- **Symbol-level** — the default. Annotate the function/class/method directly.
- **Module-level** — applies to every symbol in the file. Use when an entire
  file belongs to one feature. `feature` from a symbol overrides the module;
  invariants are unioned.
- **Role-based** — declare a role in a manifest, then tag symbols with
  `@role`. Every roled symbol enforces the role's invariants automatically.

## Inheritance

Class annotations propagate to methods; base-class annotations propagate to
subclasses. To drop an inherited invariant you must `@suppresses_invariant`
with a mandatory `reason` — suppression is loud, never silent.
