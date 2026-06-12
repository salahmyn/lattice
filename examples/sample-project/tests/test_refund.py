"""Verification tests for the checkout.refund feature."""

from lattice import verifies

from src.checkout.refund import validate_amount, mark_refunded


@verifies("checkout.refund:INV-1")
def test_refund_never_exceeds_charge():
    assert validate_amount("order-1", 5, 10) is True
    assert validate_amount("order-1", 20, 10) is False
    assert validate_amount("order-1", 0, 10) is False


@verifies("checkout.refund:INV-2")
def test_refund_is_not_repeatable():
    assert mark_refunded("order-1", already_refunded=False) is True
    assert mark_refunded("order-1", already_refunded=True) is False
