// Package wallet is a coverage fixture: a withdrawal guard a test can stub.
package wallet

// @module-feature wallet

import "errors"

// AssertSufficientFunds refuses a withdrawal larger than the balance.
// @enforces wallet:INV-1
func AssertSufficientFunds(balance, amount int) error {
	if amount > balance {
		return errors.New("insufficient funds")
	}
	return nil
}

// AssertPositive refuses a deposit that is not positive.
// @enforces wallet:INV-2
func AssertPositive(amount int) error { return check(amount > 0) }

func check(ok bool) error {
	if !ok {
		return errors.New("amount must be positive")
	}
	return nil
}

// Withdraw takes amount from balance, asking guard first.
func Withdraw(balance, amount int, guard func(int, int) error) (int, error) {
	if guard == nil {
		guard = AssertSufficientFunds
	}
	if err := guard(balance, amount); err != nil {
		return balance, err
	}
	return balance - amount, nil
}

// Deposit adds a positive amount to balance.
func Deposit(balance, amount int) (int, error) {
	if err := AssertPositive(amount); err != nil {
		return balance, err
	}
	return balance + amount, nil
}
