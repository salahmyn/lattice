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

## Scope and precedence

- **feature** — a symbol's own annotation wins; otherwise the enclosing
  class's, otherwise the module's.
- **enforces_invariant, depends_on_feature, verifies** — unioned across every
  source.
- **Inheritance** — class annotations propagate to methods, base-class
  annotations to subclasses. Drop an inherited invariant only via
  `suppresses_invariant` with a mandatory `reason`.
