/**
 * @module-feature wallet
 */

/**
 * @enforces wallet:INV-1
 */
export function assertSufficientFunds(balance, amount) {
  if (amount > balance) {
    throw new Error("insufficient funds");
  }
}

/**
 * @enforces wallet:INV-2
 */
export function assertPositive(amount) {
  if (!(amount > 0)) {
    throw new Error("amount must be positive");
  }
}

// The guard is injectable so callers can swap the policy. A test that passes
// its own guard never runs assertSufficientFunds.
export function withdraw(balance, amount, guard = assertSufficientFunds) {
  guard(balance, amount);
  return balance - amount;
}

export function deposit(balance, amount) {
  assertPositive(amount);
  return balance + amount;
}

// Persists a debit through the database function public.debit.
export async function debitInDatabase(client, accountId, amount) {
  const { error } = await client.rpc("debit", { account_id: accountId, amount });
  if (error) {
    throw error;
  }
}
