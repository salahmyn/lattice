"""Refund processing for the checkout.refund feature."""

from lattice import feature, enforces_invariant


@feature("checkout.refund", capability="self_service_refund")
@enforces_invariant("INV-1")
def validate_amount(order_id: str, amount: int, original_charge: int) -> bool:
    """Reject a refund whose amount exceeds the original charge (INV-1)."""
    return 0 < amount <= original_charge


@feature("checkout.refund")
@enforces_invariant("INV-2")
def mark_refunded(order_id: str, already_refunded: bool) -> bool:
    """Record a refund, refusing a second one for the same order (INV-2)."""
    if already_refunded:
        return False
    return True
