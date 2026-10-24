"""A wallet whose withdrawal guard can be swapped out (coverage fixture)."""
from src.tags import enforces_invariant, module_feature

module_feature("wallet")


@enforces_invariant("INV-1")
def assert_sufficient_funds(balance, amount):
    if amount > balance:
        raise ValueError("insufficient funds")


@enforces_invariant("INV-2")
def assert_positive(amount):
    if not amount > 0:
        raise ValueError("amount must be positive")


def withdraw(balance, amount, guard=assert_sufficient_funds):
    guard(balance, amount)
    return balance - amount


def deposit(balance, amount):
    assert_positive(amount)
    return balance + amount
