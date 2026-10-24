/**
 * @module-feature fees
 */

/**
 * @enforces fees:INV-1
 */
export function computeFee(amount, currency, tier) {
  let fee = amount * 0.02;
  if (currency !== "USD") {
    fee += 0.5;
  }
  if (tier === "gold") {
    fee = fee / 2;
  } else if (tier === "staff") {
    fee = 0;
  }
  const cap = amount * 0.05;
  if (fee > cap) {
    fee = cap;
  }
  return Math.round(fee * 100) / 100;
}
