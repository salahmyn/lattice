package results

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Each verifier comes with the filter its runner takes and the slug its
// per-test coverage report is named by.
// @verifies results:INV-5
func TestSelectors(t *testing.T) {
	kg := schema.KnowledgeGraph{Tests: []schema.GraphSymbol{
		{FQN: "internal.cli.TestApprovalActions", Name: "TestApprovalActions", Language: "go", File: "internal/cli/steering_test.go", Verifies: []string{"steering:INV-1"}},
		{FQN: "pkg.refund.TestSettle.rejects_a_double_refund", Name: "rejects a double refund", Language: "go", File: "pkg/refund/settle_test.go", Verifies: []string{"refund:INV-1"}},
		{FQN: "tests.wallet.test.test.rejects-an-overdraft", Name: "rejects an overdraft (USD)", Language: "typescript", File: "tests/wallet.test.js", Verifies: []string{"wallet:INV-1"}},
		{FQN: "tests.test_wallet.TestWallet.test_overdraft", Name: "test_overdraft", Language: "python", File: "tests/test_wallet.py", Verifies: []string{"wallet:INV-1"}},
		{FQN: "supabase.tests.wallet_test.test.a-debit-lowers-the-balance", Language: "postgres", File: "supabase/tests/wallet_test.sql", Verifies: []string{"wallet:INV-3"}},
		{FQN: "tests.Unit.WalletTest.testOverdraft", Language: "php", File: "tests/Unit/WalletTest.php", Verifies: []string{"wallet:INV-1"}},
		{FQN: "a.test.test.rejects-an-overdraft", Language: "typescript", File: "a.test.js"},
		{FQN: "internal.cli.TestHelper", Language: "go", File: "internal/cli/x_test.go"},
	}}
	sel := Selectors(kg, true)
	if len(sel) != 6 {
		t.Fatalf("verifiers only: %d selectors", len(sel))
	}
	by := map[string]Selector{}
	for _, s := range sel {
		by[s.Test] = s
	}
	check := func(test, runner, want string) {
		t.Helper()
		if got := strings.Join(by[test].Filters[runner], " "); got != want {
			t.Errorf("%s %s = %q, want %q", test, runner, got, want)
		}
	}
	check("internal.cli.TestApprovalActions", "go", "-run ^TestApprovalActions$")
	check("pkg.refund.TestSettle.rejects_a_double_refund", "go", "-run ^TestSettle$/^rejects_a_double_refund$")
	check("tests.wallet.test.test.rejects-an-overdraft", "node", `--test-name-pattern ^rejects an overdraft \(USD\)$`)
	check("tests.wallet.test.test.rejects-an-overdraft", "vitest", `-t rejects an overdraft \(USD\)$`)
	check("tests.wallet.test.test.rejects-an-overdraft", "deno", `--filter /^rejects an overdraft \(USD\)$/`)
	check("tests.test_wallet.TestWallet.test_overdraft", "pytest", "tests/test_wallet.py::TestWallet::test_overdraft")
	check("tests.test_wallet.TestWallet.test_overdraft", "pytest-k", "-k test_overdraft")
	check("supabase.tests.wallet_test.test.a-debit-lowers-the-balance", "pg_prove", "supabase/tests/wallet_test.sql")
	check("tests.Unit.WalletTest.testOverdraft", "phpunit", "--filter /::testOverdraft$/")
	if p := by["internal.cli.TestApprovalActions"].Package; p != "./internal/cli" {
		t.Errorf("package = %q", p)
	}
	// Two tests share the leaf rejects-an-overdraft, so each report is named
	// by its whole FQN's slug; a unique leaf names its own.
	if s := by["tests.wallet.test.test.rejects-an-overdraft"].Slug; s != "tests-wallet-test-test-rejects-an-overdraft" {
		t.Errorf("shared leaf slug = %q", s)
	}
	if s := by["internal.cli.TestApprovalActions"].Slug; s != "testapprovalactions" {
		t.Errorf("unique leaf slug = %q", s)
	}
	if all := Selectors(kg, false); len(all) != 8 {
		t.Errorf("all tests: %d", len(all))
	}
}
