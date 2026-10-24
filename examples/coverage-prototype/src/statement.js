/**
 * @module-feature wallet
 */

// Only the tests call this: no entry point reaches it.
export function formatStatement(lines) {
  return lines.map((l) => `${l.date} ${l.amount.toFixed(2)}`).join("\n");
}
