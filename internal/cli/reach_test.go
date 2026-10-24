package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// The reachability example end to end through the CLI (docs/rnd/0009,
// phase 2): extract classifies every runtime symbol into lattice.json and
// writes the call graph to the cache; validate, with the suite's coverage
// ingested, raises the four enabled codes. The live sql.public.debit is
// reached through client.rpc("debit"), its replaced sql.public.debit_v1 is
// unreached, formatStatement is test-only, and the trigger's function is
// reached because the database runs the trigger.
// @verifies reachability:INV-3, reachability:INV-4, reachability:INV-6
func TestReachabilityExampleCLI(t *testing.T) {
	repo := copyTree(t, "../../examples/coverage-prototype")
	abs := func(p string) string { return filepath.Join(repo, p) }

	if out, err := run(t, repo, newExtractCommand); err != nil {
		t.Fatalf("extract: %v\n%s", err, out)
	}
	var kg schema.KnowledgeGraph
	data, err := os.ReadFile(abs("lattice/lattice.json"))
	if err != nil || json.Unmarshal(data, &kg) != nil {
		t.Fatalf("lattice.json: %v", err)
	}
	class := map[string]string{}
	for _, s := range kg.Symbols {
		class[s.FQN] = s.Reach
	}
	for fqn, want := range map[string]string{
		"sql.public.debit":                           schema.ReachEntry,
		"sql.public.debit_v1":                        schema.ReachUnreached,
		"sql.public.touch_account":                   schema.ReachEntry,
		"sql.trigger.public.accounts.accounts_touch": schema.ReachEntry,
		"src.statement.formatStatement":              schema.ReachTestOnly,
		"src.wallet.assertSufficientFunds":           schema.ReachEntry,
		"src.cli.main":                               schema.ReachEntry,
	} {
		if class[fqn] != want {
			t.Errorf("%s: reach %q, want %q", fqn, class[fqn], want)
		}
	}
	var cache reach.Cache
	data, err = os.ReadFile(abs("lattice/" + reach.CacheFile))
	if err != nil || json.Unmarshal(data, &cache) != nil {
		t.Fatalf("callgraph.json: %v", err)
	}
	found := false
	for _, e := range cache.Edges {
		if e.From == "src.wallet.debitInDatabase" && e.To == "sql.public.debit" && e.Kind == "rpc" {
			found = true
		}
	}
	if !found || cache.Stats.Unresolved != 0 || len(cache.Roots) != 2 {
		t.Errorf("callgraph: rpc edge %v, stats %+v, roots %+v", found, cache.Stats, cache.Roots)
	}

	if out, err := run(t, repo, newResultsCommand, "ingest", abs("coverage/junit.xml"), abs("coverage/all.lcov"),
		"--per-test", abs("coverage/per-test")); err != nil {
		t.Fatalf("ingest: %v\n%s", err, out)
	}
	out, _ := run(t, repo, newValidateCommand)
	var rep validateReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("validate output: %v\n%s", err, out)
	}
	var got []string
	for _, v := range rep.Violations {
		switch v.Code {
		case schema.CodeSymbolUnreached, schema.CodeSymbolTestOnly, schema.CodeSymbolUntested,
			schema.CodeUnusedExport, schema.CodeReachabilityNoRoots, schema.CodeRPCUnresolved,
			schema.CodeFeatureUnreached:
			got = append(got, string(v.Severity)+" "+v.Code+" "+v.Subject)
		}
	}
	sort.Strings(got)
	want := []string{
		"info SYMBOL_TEST_ONLY src.statement.formatStatement",
		"info SYMBOL_UNTESTED src.cli.main",                     // src/cli.js is in the include set, not loaded
		"info SYMBOL_UNTESTED src.wallet.assertSufficientFunds", // the stubbed verifier never runs it
		"info SYMBOL_UNTESTED src.wallet.debitInDatabase",
		"info UNUSED_EXPORT src.wallet.assertPositive",
		"info UNUSED_EXPORT src.wallet.assertSufficientFunds",
		"warning SYMBOL_UNREACHED sql.public.debit_v1",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("validate reachability findings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
