// @module-feature shop.refunds

/**
 * @capability refund
 * @enforces INV-1
 */
export function refund(orderId: string): boolean {
  return orderId.length > 0;
}
