/**
 * @module-feature wallet
 */

/**
 * @feature wallet
 * @capability wallet_credit
 * @enforces INV-1
 */
export function creditWallet(accountId: string, balance: number, amount: number): number {
  if (amount <= 0) {
    throw new Error("credit amount must be positive");
  }
  // A credit only ever increases the balance, so INV-1 (never negative) holds.
  return balance + amount;
}
