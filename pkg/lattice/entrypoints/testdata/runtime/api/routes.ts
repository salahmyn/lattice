// @module-feature orders
// The handler is named but this module neither defines nor imports it.
declare const app: { get(path: string, handler: unknown): void };
app.get('/orders/missing', missingHandler);
