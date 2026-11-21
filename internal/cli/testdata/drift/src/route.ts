// @module-feature shop
import { placeOrder } from './checkout.ts';

/** The one production route. */
export function ordersRoute(path: string): string {
  return path === '/orders' ? placeOrder(1) : 'not found';
}
