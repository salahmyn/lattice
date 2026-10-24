/**
 * @module-feature wallet
 */
import { withdraw, deposit, debitInDatabase } from "./wallet.js";
import { computeFee } from "./fees.js";

// Entry point: `wallet <withdraw|deposit> <balance> <amount>`.
export async function main(argv, client) {
  const [cmd, balance, amount] = [argv[0], Number(argv[1]), Number(argv[2])];
  if (cmd === "withdraw") {
    const fee = computeFee(amount, "USD", "standard");
    const next = withdraw(balance, amount + fee);
    await debitInDatabase(client, "acct", amount + fee);
    return next;
  }
  return deposit(balance, amount);
}
