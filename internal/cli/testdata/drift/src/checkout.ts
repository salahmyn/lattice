// @module-feature shop.checkout

/**
 * @capability place_order
 * @enforces INV-1
 * @enforces INV-2
 */
export function placeOrder(qty: number): string {
  if (qty < 1 || qty > 10) return 'refused';
  return 'placed';
}
