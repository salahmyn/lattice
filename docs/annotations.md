# Annotation conventions

Annotations link source code to manifest concepts. A symbol's **effective
annotation set** is the union of its own annotations, the file's module-level
annotations, role-based annotations, and inherited base-class annotations.

The six conceptual annotations are: `feature`, `capability`,
`enforces_invariant`, `verifies`, `verifies_capability`, `depends_on_feature`.
Plus `role`, `suppresses_invariant`, and the `module_*` forms.

## Python

Decorators imported from the no-op `lattice` package (published to PyPI only
so user code has real imports and IDE autocomplete):

```python
from lattice import feature, enforces_invariant, verifies, role

@feature("checkout.refund", capability="self_service_refund")
@enforces_invariant("INV-1", "INV-2")
def validate_amount(order_id, amount): ...

@role("monetary_handler")
async def issue_credit(amount, customer_id): ...
```

Module-level (top of file):

```python
module_feature("checkout.refund.settlement")
module_enforces_invariant("INV-12", "INV-13")
```

Arguments must be string literals or lists of string literals — names and
expressions are rejected with `ANNOTATION_ARG_NOT_LITERAL`.

## TypeScript / JavaScript

JSDoc tags (`.ts`, `.tsx`, `.js`, `.jsx`). Decorators are deliberately not
supported.

```typescript
/**
 * @feature checkout.refund
 * @capability self_service_refund
 * @enforces INV-2, INV-3
 */
export async function enqueueRefund(orderId: string): Promise<void> { ... }
```

Module-level tags go in the file's first JSDoc block:

```typescript
/**
 * @module-feature checkout.refund.settlement
 * @module-enforces INV-12, INV-13
 */
```

## PHP (8.0+)

PHP 8 attributes from the `Lattice\Attributes` namespace. There is no docblock
fallback.

```php
use Lattice\Attributes\{Feature, Capability, EnforcesInvariant};

#[Feature('checkout.refund')]
#[Capability('self_service_refund')]
final class RefundService
{
    #[EnforcesInvariant('INV-1')]
    public function validateAmount(string $orderId): bool { ... }
}
```

A class that `use`-es a trait inherits that trait's annotations — an explicit
Lattice semantic, even though the PHP runtime does not carry trait attributes
onto the consuming class.

## Go

Tags in the doc comment directly above a declaration (`.go`), with the
TypeScript tag names. The comment must touch the declaration: a blank line
between them, or a tag trailing code on the same line, attaches to nothing
and raises `ANNOTATION_ORPHANED`.

```go
// Settle pays a refund.
// @feature checkout.refund
// @capability self_service_refund
// @enforces checkout.refund:INV-1, checkout.refund:INV-2
func Settle(r Refund) error { ... }

// @enforces checkout.refund:INV-3
func (p *Processor) Run() error { ... }
```

Module-level tags go in any comment of the file, usually the package comment:

```go
// Package refund settles refunds.
//
// @module-feature checkout.refund.settlement
// @module-enforces checkout.refund:INV-12
package refund
```

- **Symbols** are named by package directory: `Settle` in
  `pkg/checkout/refund` is `pkg.checkout.refund.Settle`, the method is
  `pkg.checkout.refund.Processor.Run`. Functions, methods, struct and named
  types (classes) and interfaces are symbols; a package-level `const` or
  `var` only when it is tagged. A file at the module root uses its package
  name (`main.main`).
- **Tests** are the functions of `_test.go` files. A `t.Run("name", ...)`
  subtest of a `TestXxx` is a test of its own, named as `go test` names it
  (`pkg.checkout.refund.TestSettle.rejects_a_double_refund`), so a tag above
  the call verifies that subtest alone:

  ```go
  func TestSettle(t *testing.T) {
      // @verifies checkout.refund:INV-1
      t.Run("rejects a double refund", func(t *testing.T) { ... })

      // @verifies checkout.refund:INV-2
      for _, tc := range cases {
          t.Run(tc.name, func(t *testing.T) { ... })
      }
  }
  ```

  A subtest named by a variable (a table test) has no static name: a tag
  above it, or above the table's `for` loop, attaches to the enclosing test,
  which passes only when every row passes.
- **Prose is not a tag.** A wrapped doc comment line may start with `@`
  (`// @error annotations found in source ...`). A tag's arguments are
  comma-separated single tokens; a line whose arguments read as words
  separated by spaces is prose and is not parsed. `@surface` keeps its
  space-separated form and must start with an interaction type
  (`// @surface http POST /refunds`).
- `init` functions are not addressable (a package may declare several):
  tag a named function, or the file with `@module-*` tags.

## Scope and precedence

- **feature** — a symbol's own annotation wins; otherwise the enclosing
  class's, otherwise the module's.
- **enforces_invariant, depends_on_feature, verifies** — unioned across every
  source.
- **Inheritance** — class annotations propagate to methods, base-class
  annotations to subclasses. Drop an inherited invariant only via
  `suppresses_invariant` with a mandatory `reason`.
