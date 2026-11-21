import { test } from 'node:test';
import assert from 'node:assert/strict';
import { placeOrder } from './checkout.ts';
import { refund } from './refunds.ts';

/** @verifies shop.checkout:INV-1 */
test('an order of 1 to 10 items is placed', () => { assert.equal(placeOrder(3), 'placed'); });
/** @verifies shop.checkout:INV-2 */
test('an order of 0 or more than 10 items is refused', () => { assert.equal(placeOrder(0), 'refused'); assert.equal(placeOrder(11), 'refused'); });
/** @verifies shop.refunds:INV-1 */
test('a refund needs an order id', () => { assert.equal(refund(''), false); });
