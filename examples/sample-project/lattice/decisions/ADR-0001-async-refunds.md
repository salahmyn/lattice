# ADR-0001: Process refunds asynchronously

Status: accepted

## Context

Refunds were processed synchronously inside the checkout HTTP request. This
made the request as slow as the slowest downstream payment call and coupled
refund success to request latency. A transient payment-gateway slowdown
turned into customer-visible checkout failures.

## Decision

Refunds are enqueued as `refund-event.v1` messages and processed by an
asynchronous consumer. The HTTP request returns as soon as the event is
durably enqueued. The consumer is idempotent: each event is processed at most
once, which preserves `checkout.refund:INV-2`.

## Consequences

- Refund requests return quickly and are decoupled from gateway latency.
- The system must guarantee at-least-once delivery and consumer idempotency.
- Observability shifts: a refund "succeeding" now means "enqueued", and a
  separate signal reports settlement.
