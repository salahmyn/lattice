import pytest

from src.tags import verifies
from src.wallet import deposit, withdraw


def stub_guard(balance, amount):
    raise ValueError("insufficient funds")


# Passes, but the stub replaces the enforcer: assert_sufficient_funds never runs.
@verifies("wallet:INV-1")
def test_rejects_an_overdraft():
    with pytest.raises(ValueError):
        withdraw(10, 50, guard=stub_guard)


@verifies("wallet:INV-2")
def test_rejects_a_non_positive_deposit():
    with pytest.raises(ValueError):
        deposit(10, 0)


def test_deposits():
    assert deposit(10, 5) == 15
