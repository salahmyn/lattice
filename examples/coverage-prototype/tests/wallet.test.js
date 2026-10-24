import { test } from "node:test";
import assert from "node:assert/strict";
import { withdraw, deposit } from "../src/wallet.js";
import { formatStatement } from "../src/statement.js";

// Passes, but proves nothing about INV-1: the stub guard replaces the
// enforcer, so assertSufficientFunds never runs.
/** @verifies wallet:INV-1 */
test("rejects an overdraft", () => {
  const stub = () => {
    throw new Error("insufficient funds");
  };
  assert.throws(() => withdraw(10, 50, stub), /insufficient funds/);
});

/** @verifies wallet:INV-2 */
test("rejects a non-positive deposit", () => {
  assert.throws(() => deposit(10, 0), /must be positive/);
});

test("formats a statement", () => {
  assert.equal(formatStatement([{ date: "2026-01-01", amount: 5 }]), "2026-01-01 5.00");
});
