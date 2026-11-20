package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// runText executes one command with text output.
func runText(t *testing.T, repo string, mk func(*IO) *cobra.Command, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	io := &IO{Repo: repo, Out: &out, Err: &out, Actor: "test"}
	cmd := mk(io)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs(args)
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

// `lattice feature show` and `lattice rtm` print each case of an invariant
// under it, with the coverage the audit computed from the ingested results:
// a tagged test that passed with no break run is partial, a case no test is
// tagged with is uncovered.
// @verifies audit:INV-8
func TestCasesInFeatureShowAndRTM(t *testing.T) {
	clearCIEnv(t)
	f := newCheckFixture(t)
	f.write("lattice/features/shop.yaml", shopFeature(1)+
		"    cases:\n      - id: a\n        kind: happy\n        statement: A price of 1 is positive.\n"+
		"      - id: b\n        kind: error\n        statement: A price of 0 is refused.\n")
	f.write("shop/shop_test.go", "package shop\n\nimport \"testing\"\n\n// @verifies shop:INV-1#a\nfunc TestPriceIsPositive(t *testing.T) {\n\tif !Positive(1) {\n\t\tt.Fatal(\"want positive\")\n\t}\n}\n")
	f.write("junit.xml", `<testsuites><testsuite name="shop"><testcase classname="shop" name="TestPriceIsPositive"/></testsuite></testsuites>`)
	if out, err := run(t, f.dir, newResultsCommand, "ingest", f.dir+"/junit.xml"); err != nil {
		t.Fatalf("ingest: %v\n%s", err, out)
	}

	show := runText(t, f.dir, newFeatureCommand, "show", "shop")
	for _, want := range []string{
		"invariant   INV-1 — A price is positive.",
		"case      1a   partial   happy    A price of 1 is positive. [a tagged verifier passed; there is no break run of shop.Positive",
		"case      1b   uncovered error    A price of 0 is refused. [no test is tagged @verifies shop:INV-1#b]",
	} {
		if !strings.Contains(show, want) {
			t.Errorf("feature show lacks %q:\n%s", want, show)
		}
	}
	rtm := runText(t, f.dir, newRTMCommand)
	if !strings.Contains(rtm, "    case 1a   partial") || !strings.Contains(rtm, "    case 1b   uncovered") {
		t.Errorf("rtm lacks the case lines:\n%s", rtm)
	}
}
