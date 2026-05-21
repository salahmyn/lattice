import { creditWallet } from "../src/wallet/credit";

/**
 * @verifies wallet:INV-1
 */
export function testWalletBalanceNeverNegative(): void {
  const balance = creditWallet("acct-1", 0, 25);
  if (balance < 0) {
    throw new Error("wallet balance went negative");
  }
}
