package importer

import (
	"bytes"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

func sampleModules() []ir.Module {
	return []ir.Module{
		{
			File: "src/billing/refund.py", Language: "python",
			Symbols: []ir.Symbol{
				{FQN: "billing.refund.create"},
				{FQN: "billing.refund.validate"},
				{FQN: "billing.refund.test_create", IsTest: true},
			},
			Surfaces: []ir.Surface{{Type: "http", Method: "POST", Path: "/refund"}},
		},
		{
			File: "src/billing/charge.py", Language: "python",
			Symbols: []ir.Symbol{
				{FQN: "billing.charge.run"},
				{FQN: "billing.charge.helper"},
			},
		},
		{
			File: "src/users/user.ts", Language: "typescript",
			Symbols: []ir.Symbol{{FQN: "users.user.get"}},
		},
	}
}

func TestDiscoverClustersByDirectory(t *testing.T) {
	cf := Discover(sampleModules(), Options{MinCandidateSymbols: 3})

	if len(cf.Candidates) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(cf.Candidates))
	}
	c := cf.Candidates[0]
	if c.Package != "src/billing" {
		t.Errorf("package = %q, want src/billing", c.Package)
	}
	if len(c.Symbols) != 4 {
		t.Errorf("symbols = %d, want 4 (test symbol excluded)", len(c.Symbols))
	}
	if c.Language != "python" {
		t.Errorf("language = %q, want python", c.Language)
	}
	if c.Confidence != 0.9 {
		t.Errorf("confidence = %v, want 0.9 (package+surface+tests)", c.Confidence)
	}
	signals := map[string]bool{}
	for _, e := range c.Evidence {
		signals[e.Signal] = true
	}
	for _, want := range []string{SignalPackage, SignalSurface, SignalTestGroup} {
		if !signals[want] {
			t.Errorf("missing evidence signal %q", want)
		}
	}
}

func TestDiscoveryCoverage(t *testing.T) {
	cf := Discover(sampleModules(), Options{MinCandidateSymbols: 3})
	d := cf.Coverage.Discovery

	if d.TotalSymbols != 5 {
		t.Errorf("total = %d, want 5", d.TotalSymbols)
	}
	if d.ClusteredSymbols != 4 {
		t.Errorf("clustered = %d, want 4", d.ClusteredSymbols)
	}
	if d.Ratio != 0.8 {
		t.Errorf("ratio = %v, want 0.8", d.Ratio)
	}
	if len(d.ByPackage) != 2 {
		t.Fatalf("by_package = %d entries, want 2", len(d.ByPackage))
	}
	// src/users has one symbol, below the floor, so it stays unclustered.
	users := d.ByPackage[1]
	if users.Package != "src/users" || users.ClusteredSymbols != 0 {
		t.Errorf("src/users = %+v, want 0 clustered", users)
	}
}

func TestDiscoverIsDeterministic(t *testing.T) {
	mods := sampleModules()
	reversed := []ir.Module{mods[2], mods[1], mods[0]}

	a, err := Marshal(Discover(mods, Options{MinCandidateSymbols: 3}))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Marshal(Discover(reversed, Options{MinCandidateSymbols: 3}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("discovery is not deterministic under input reordering:\n%s\n---\n%s", a, b)
	}
}

func TestDiscoverScopeFilter(t *testing.T) {
	cf := Discover(sampleModules(), Options{Scope: "src/users", MinCandidateSymbols: 1})
	if len(cf.Candidates) != 1 || cf.Candidates[0].Package != "src/users" {
		t.Fatalf("scope did not restrict discovery: %+v", cf.Candidates)
	}
	if cf.Coverage.Discovery.TotalSymbols != 1 {
		t.Errorf("scoped total = %d, want 1", cf.Coverage.Discovery.TotalSymbols)
	}
}
