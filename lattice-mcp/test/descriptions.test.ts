// Build-time test: every tool description must follow the strict template
// (purpose, when-to-call, returns, common errors). This guards the contract
// that agents rely on the same shape for every tool.

import { test } from "node:test";
import assert from "node:assert/strict";

import { renderDescription, tools } from "../src/tools/index.ts";

test("every tool description follows the template", () => {
  assert.ok(tools.length >= 21, `expected >= 21 tools, got ${tools.length}`);

  for (const def of tools) {
    const desc = renderDescription(def);
    assert.ok(desc.startsWith(`${def.name}:`), `${def.name}: must start with its name`);
    assert.match(desc, /\nWhen to call:\n/, `${def.name}: missing "When to call:" section`);
    assert.match(desc, /\nReturns: /, `${def.name}: missing "Returns:" section`);
    assert.match(desc, /\nCommon errors:\n/, `${def.name}: missing "Common errors:" section`);
    assert.ok(def.whenToCall.length > 0, `${def.name}: needs at least one when-to-call entry`);
    assert.ok(def.commonErrors.length > 0, `${def.name}: needs at least one common error`);
    assert.ok(def.summary.length > 0, `${def.name}: needs a summary`);
  }
});

test("tool names are unique and namespaced", () => {
  const seen = new Set<string>();
  for (const def of tools) {
    assert.ok(def.name.startsWith("lattice_"), `${def.name}: must be lattice_-prefixed`);
    assert.ok(!seen.has(def.name), `duplicate tool name ${def.name}`);
    seen.add(def.name);
  }
});
