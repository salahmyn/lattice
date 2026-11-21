// @module-feature shop.digest

/** @capability send_digest */
export function sendDigest(orderCount: number): string {
  return `digest of ${orderCount} orders`;
}
