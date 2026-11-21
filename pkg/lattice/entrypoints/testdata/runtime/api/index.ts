// @module-feature orders
import { createOrder } from './orders.ts';

Deno.serve((req: Request) => createOrder(req));
