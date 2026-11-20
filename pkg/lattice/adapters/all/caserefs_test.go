package all

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/adapters"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/golang"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/php"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/postgres"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/python"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/typescript"
	"github.com/salahmyn/lattice/pkg/lattice/graph"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// A `@verifies f:INV-1#a` tag, in every language, reaches the graph as a
// verifier of f:INV-1 (so every existing signal counts it) and keeps the
// case ref as written. A plain ref beside it is unchanged.
// @verifies graph:INV-5
func TestCaseRefsInEveryAdapter(t *testing.T) {
	for _, tc := range []struct {
		adapter adapters.LanguageAdapter
		file    string
		src     string
	}{
		{golang.New(), "wallet/fees_test.go",
			"package wallet\n\n// @verifies wallet.fees:INV-1#a, wallet.fees:INV-2\nfunc TestFee(t *testing.T) {}\n"},
		{typescript.New(), "src/fees.test.ts",
			"/** @verifies wallet.fees:INV-1#a, wallet.fees:INV-2 */\nit('charges a fee', () => {});\n"},
		{python.New(), "tests/test_fees.py",
			"@verifies(\"wallet.fees:INV-1#a\", \"wallet.fees:INV-2\")\ndef test_fee():\n    pass\n"},
		{php.New(), "tests/FeesTest.php",
			"<?php\nclass FeesTest {\n  #[Verifies('wallet.fees:INV-1#a', 'wallet.fees:INV-2')]\n  public function testFee() {}\n}\n"},
		{postgres.New(nil, nil), "supabase/tests/fees_test.sql",
			"begin;\nselect plan(1);\n-- @verifies wallet.fees:INV-1#a, wallet.fees:INV-2\nselect ok(true, 'a fee is charged');\nselect * from finish();\nrollback;\n"},
	} {
		mod, err := tc.adapter.Parse(context.Background(), tc.file, []byte(tc.src))
		if err != nil {
			t.Fatalf("%s: %v", tc.file, err)
		}
		kg := graph.Build(graph.Input{Modules: []ir.Module{mod}}, graph.Options{GeneratedAt: time.Unix(0, 0)})
		var test *schema.GraphSymbol
		for i := range kg.Tests {
			if len(kg.Tests[i].Verifies) > 0 {
				test = &kg.Tests[i]
			}
		}
		if test == nil {
			t.Fatalf("%s: no verifier in the graph: %+v", tc.file, kg.Tests)
		}
		if got := strings.Join(test.Verifies, " "); got != "wallet.fees:INV-1 wallet.fees:INV-2" {
			t.Errorf("%s: verifies = %q, want the invariant refs", tc.file, got)
		}
		if got := strings.Join(test.VerifiesCases, " "); got != "wallet.fees:INV-1#a" {
			t.Errorf("%s: verifies_cases = %q, want the case ref as written", tc.file, got)
		}
	}
}
