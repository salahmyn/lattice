import { test } from "node:test";
import assert from "node:assert/strict";
import { computeFee } from "../src/fees.js";

// Exercises one path of computeFee: USD, standard tier, no cap. The other
// currency, the gold and staff tiers and the cap never run.
/** @verifies fees:INV-1 */
test("charges two percent in USD", () => {
  assert.equal(computeFee(100, "USD", "standard"), 2);
});
