# Lattice — known gaps

Backlog of shortcomings found while dogfooding Lattice on a real project
(`cart-service`: a Hono + Cloudflare Workers app).

**All five gaps below are now fixed** (see the resolution note under each).
A sixth, single-line-JSDoc parsing, was uncovered while fixing gap 1 and is
also fixed.

| # | Gap | Severity | Status |
|---|---|---|---|
| 1 | TS adapter ignores annotations on `it()`/`describe()`/`test()` calls | Medium | Fixed |
| 2 | `@module-feature` turns type-only declarations into `impl` edges | Medium | Fixed |
| 3 | Symbol extraction keeps private/non-exported symbols as feature impls | Low | Fixed |
| 4 | `feature show` does not surface sub-feature `children` | Low | Fixed |
| 5 | Views render multi-line `purpose` verbatim into inline markdown | Low | Fixed |
| 6 | TS adapter drops single-line `/** @tag */` JSDoc | Medium | Fixed |

---

## 1. TS adapter ignores annotations on test-framework calls

**What.** Design §13 says a symbol may "wrap an `it()`, `test()`, or
`describe(...).it()` call (handled by treating the test framework call as the
symbol)". The adapter does not do this.

**Evidence.** In `pkg/lattice/adapters/typescript/typescript_parse.go`, `walk()`
only threads pending JSDoc onto declaration node types (`function_declaration`,
`class_declaration`, `lexical_declaration`, …). A bare `describe(...)` /
`it(...)` is an `expression_statement`, which falls into the `default` case and
**discards** any pending `@verifies` annotation.

**Impact.** Idiomatic vitest/jest tests cannot carry `@verifies`. In
`cart-service` the tests had to be written as named function declarations
registered via `it("…", namedFn)` so the `@verifies` JSDoc had a declaration to
attach to. That works but is not how people write tests.

**Fixed.** `walkStatements()` now handles `expression_statement` whose inner
`call_expression` callee is `it`/`test`/`describe`/`suite`/`context` (member
forms like `it.skip` included). The call becomes a synthetic test symbol named
from its first string argument; `describe`/`suite`/`context` bodies are walked
recursively so annotations on nested `it()` calls are captured. Regression test:
`TestParseTestCalls`.

## 2. `@module-feature` makes type-only declarations into `impl` edges

**What.** A file-level `@module-feature` propagates the feature to *every*
symbol the adapter records — including `interface` declarations.

**Evidence.** `lattice feature show cart` on `cart-service` lists
`src.cart-do.CartView` (an `interface`) as an `impl` of the `cart` feature.

**Impact.** The implementation list is polluted with non-implementation
symbols. Anything derived from impl edges (impact reports, blast radius, the
auto-populated `implementations` manifest field) inherits the noise.

**Fixed.** `graph.isImplementationEdge` now gates impl edges on
`implementationKinds` (`class`, `function`, `method`, `trait`); `interface`
symbols stay in the graph but are no longer implementation edges.

## 3. Private / non-exported symbols are kept as feature implementations

**What.** The TS adapter records non-exported top-level functions and private
class methods as full feature symbols.

**Evidence.** `feature show cart` lists `src.cart-do.toView` (a non-exported
helper) and `Cart.load` / `Cart.commit` / `Cart.mutate` (private methods) as
`impl` edges.

**Impact.** Internal helpers are presented with the same weight as the public
surface of a feature. For a *meaning*-tracking tool this is misleading — a
private helper is an implementation detail, not a feature implementation.

**Fixed.** `ir.Symbol` gained an `Exported` flag, populated by all three
adapters: TypeScript `export` keyword and method `accessibility_modifier`,
PHP visibility modifiers, and the Python leading-underscore convention.
`isImplementationEdge` requires `Exported`, so private helpers stay in the
graph but are no longer feature implementations.

## 4. `feature show` does not surface sub-feature children

**What.** The auto-populated `children` field is computed but not displayed.

**Evidence.** `cart` has child `cart.pricing`; `lattice feature show cart` does
not mention it anywhere in its output.

**Fixed.** `lattice feature show` now prints a `sub-feature <id>` line for each
entry in `Manifest.Children`.

## 5. Views render multi-line `purpose` verbatim into inline markdown

**What.** A `purpose` written as a YAML block scalar (`|`) keeps its newlines,
and views drop that multi-line string straight into inline contexts.

**Evidence.** `lattice view product` emits the `cart` purpose as a list bullet
with embedded newlines, so the wrapped lines break out of the list. `feature
show` similarly prints a stray double blank line after a block `purpose`.

**Fixed.** Added `schema.InlineText` (collapses whitespace). View templates get
an `inline` and a `trimSpace` func: `product.tmpl` uses `inline`, `developer.tmpl`
uses `trimSpace`. `feature list` collapses the purpose; `feature show` trims it.

## 6. TS adapter drops single-line `/** @tag */` JSDoc

**What.** Uncovered while fixing gap 1. `parseJSDoc` stripped a leading `*`
per line, so it only saw tags on `*`-prefixed continuation lines. A one-line
`/** @verifies x */` (and a line carrying several `@tags`) was silently dropped.

**Fixed.** `parseJSDoc` now strips the `/**`, `/*`, `*` openers and the `*/`
closer from each line, then splits a cleaned line on `@` so multiple tags on
one line are all captured.

---

# Second pass — comparing the cart-service code against Lattice's outcomes

A line-by-line comparison of the `cart-service` source against what Lattice
extracted exposed a deeper gap: Lattice modelled the *pure logic* faithfully
but not the *edges of the system*.

| # | Gap | Status |
|---|---|---|
| 7 | HTTP/event surfaces declared as unverified manifest prose | Fixed |
| 8 | `it("desc", namedFn)` double-counted as a test symbol | Fixed |
| 9 | Error/response contract (codes → HTTP status) uncaptured | Fixed |
| 10 | Persistence / strong-consistency guarantees uncaptured | Open |

## 7. Surfaces were unverified prose

**What.** A manifest's `surface:` entries were hand-written and nothing checked
them against code. `cart-service` served 6 HTTP routes; the manifest declared
4; `validate` stayed green.

**Fixed.** Surfaces are now a first-class, verified concept:
- `ir.Surface` + `Module.Surfaces`; the TS adapter auto-detects `app.METHOD(path,…)`
  route calls and parses an `@surface` annotation (`#[Surface]` / `@surface`
  in PHP/Python).
- `graph.buildSurfaces` fuses declared and code surfaces into
  `KnowledgeGraph.Surfaces` — the interaction inventory.
- New rules `SURFACE_UNDECLARED` and `SURFACE_UNIMPLEMENTED` (warnings) catch
  drift in both directions.
- `feature show` and the product view list every interaction with its status.

## 8. Test double-count

**What.** The gap-1 fix synthesized a symbol for every `it()` call. With the
`it("desc", namedFn)` pattern the named function *and* the `it()` were both
counted — `cart-service` showed 10 "tests" for ~4 logical ones.

**Fixed.** `handleTestCall` synthesizes a symbol only for an `it()`/`test()`
with an inline callback or pending annotations; `describe()` becomes a symbol
only when annotated. `it("desc", namedFn)` no longer duplicates.

## 9. Error/response contract

**What.** The `CartError` codes and their HTTP-status mapping were a real API
contract with no Lattice representation.

**Fixed.** Mirrors the surface design: a feature manifest declares an `errors:`
catalog (`code`, `status`, `description`); code carries `@error <code> <status>`
annotations; `graph.buildErrors` fuses them into `KnowledgeGraph.Errors`; rules
`ERROR_UNDECLARED` / `ERROR_UNIMPLEMENTED` (warnings) catch drift. `feature
show` and the product view list the error contract.

## 10. Persistence guarantees — open

The Durable Object's "one instance per cart, strong consistency" is behaviour
no invariant expresses. Likely needs an invariant kind for stateful/storage
guarantees, or acceptance that this is infrastructure outside Lattice's scope.

---

_Last updated: 2026-05-22 — gaps 1-9 fixed; 10 open. Surface and error-contract
verification landed after comparing `cart-service` against the extracted graph._
