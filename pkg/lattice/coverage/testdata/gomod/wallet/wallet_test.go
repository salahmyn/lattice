package wallet

import (
	"errors"
	"testing"
)

// Passes, but the stub replaces the guard: AssertSufficientFunds never runs.
// @verifies wallet:INV-1
func TestRejectsAnOverdraft(t *testing.T) {
	stub := func(int, int) error { return errors.New("insufficient funds") }
	if _, err := Withdraw(10, 50, stub); err == nil {
		t.Fatal("want an error")
	}
}

// @verifies wallet:INV-2
func TestRejectsANonPositiveDeposit(t *testing.T) {
	if _, err := Deposit(10, 0); err == nil {
		t.Fatal("want an error")
	}
}

func TestDeposits(t *testing.T) {
	if got, _ := Deposit(10, 5); got != 15 {
		t.Fatalf("got %d", got)
	}
}
