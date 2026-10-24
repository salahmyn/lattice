package rtm

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// fakeCoverage is hand-built coverage evidence: per test, the basis of its
// own data and the symbols it executed.
type fakeCoverage struct {
	perTest map[string]map[string]bool
	perFile map[string]map[string]bool // keyed by test, for brevity
	suite   map[string]bool
}

func (f fakeCoverage) TestRan(test, symbol string) (bool, string) {
	if m, ok := f.perTest[test]; ok {
		return m[symbol], EvidencePerTest
	}
	if m, ok := f.perFile[test]; ok {
		return m[symbol], EvidencePerFile
	}
	return false, EvidenceNone
}

func (f fakeCoverage) SuiteRan(symbol string) (bool, bool) {
	ran, ok := f.suite[symbol]
	return ran, ok
}

func (f fakeCoverage) EnforcerLines(_, _ []string) (int, int, bool, bool) { return 4, 3, true, true }

func (f fakeCoverage) HasPerTest() bool { return len(f.perTest) > 0 }

const (
	enforcer = "Checkout\\Refund"
	verifier = "Tests\\Checkout\\RefundTest"
)

func passing(string) (bool, bool) { return true, true }

// exercised: demonstrated, and a verifier's own per-test run executed an
// enforcer. per_file and suite evidence never promote, an undemonstrated row
// is never exercised, and with no coverage nothing changes.
// @verifies rtm:INV-8
func TestExercised(t *testing.T) {
	ran := fakeCoverage{perTest: map[string]map[string]bool{verifier: {enforcer: true}}}
	row := Build(fixture(), Options{ResultOf: passing, Coverage: ran}).Rows[0]
	if row.Status != StatusExercised || row.Evidence != EvidencePerTest || len(row.ExercisedBy) != 1 {
		t.Fatalf("per-test run executed the enforcer: %+v", row)
	}
	if row.EnforcerCoverage == nil || row.EnforcerCoverage.Percent != 75 || row.EnforcerCoverage.Over != "verifiers" {
		t.Errorf("enforcer coverage = %+v", row.EnforcerCoverage)
	}

	missed := fakeCoverage{perTest: map[string]map[string]bool{verifier: {}}}
	row = Build(fixture(), Options{ResultOf: passing, Coverage: missed}).Rows[0]
	if row.Status != StatusDemonstrated || !strings.Contains(row.CoverageReason, "no verifier's per-test coverage executed") {
		t.Errorf("per-test run missed the enforcer: %s %q", row.Status, row.CoverageReason)
	}

	perFile := fakeCoverage{perFile: map[string]map[string]bool{verifier: {enforcer: true}}}
	row = Build(fixture(), Options{ResultOf: passing, Coverage: perFile}).Rows[0]
	if row.Status != StatusDemonstrated || row.Evidence != EvidencePerFile || !strings.Contains(row.CoverageReason, "no per-test coverage ingested") {
		t.Errorf("per-file evidence must not promote: %s %s %q", row.Status, row.Evidence, row.CoverageReason)
	}

	suite := fakeCoverage{suite: map[string]bool{enforcer: true}}
	row = Build(fixture(), Options{ResultOf: passing, Coverage: suite}).Rows[0]
	if row.Status != StatusDemonstrated || row.Evidence != EvidenceSuite {
		t.Errorf("suite evidence must not promote: %s %s", row.Status, row.Evidence)
	}

	row = Build(fixture(), Options{Coverage: ran}).Rows[0]
	if row.Status != StatusVerified {
		t.Errorf("no result ingested: %s, want verified even though the run executed the enforcer", row.Status)
	}

	row = Build(fixture(), Options{ResultOf: passing}).Rows[0]
	if row.Status != StatusDemonstrated || row.Evidence != "" || row.CoverageReason != "" {
		t.Errorf("no coverage: %+v", row)
	}

	row = Build(fixture(), Options{ResultOf: passing, Coverage: ran, ProfileLite: true}).Rows[0]
	if row.Status != StatusVerified {
		t.Errorf("lite caps exercised too: %s", row.Status)
	}
}

// A criterion over several invariants is exercised only when each is.
func TestExercisedEveryInvariant(t *testing.T) {
	kg := fixture()
	kg.Features[0].Invariants = append(kg.Features[0].Invariants, schema.Invariant{ID: "INV-2", Statement: "s"})
	kg.Symbols = append(kg.Symbols, schema.GraphSymbol{FQN: "E2", Feature: "checkout", EnforcesInvariants: []string{"INV-2"}})
	kg.Tests = append(kg.Tests, schema.GraphSymbol{FQN: "T2", IsTest: true, Verifies: []string{"checkout:INV-2"}})
	kg.BRDs[0].SuccessCriteria[0].MapsToInvariant = schema.InvariantRefs{"checkout:INV-1", "checkout:INV-2"}

	half := fakeCoverage{perTest: map[string]map[string]bool{verifier: {enforcer: true}, "T2": {}}}
	row := Build(kg, Options{ResultOf: passing, Coverage: half}).Rows[0]
	if row.Status != StatusDemonstrated || !strings.HasPrefix(row.CoverageReason, "checkout:INV-2: ") {
		t.Errorf("INV-2 not exercised: %s %q", row.Status, row.CoverageReason)
	}
	both := fakeCoverage{perTest: map[string]map[string]bool{verifier: {enforcer: true}, "T2": {"E2": true}}}
	if row := Build(kg, Options{ResultOf: passing, Coverage: both}).Rows[0]; row.Status != StatusExercised {
		t.Errorf("both exercised: %s", row.Status)
	}
}

// Exercised rows count as demonstrated and as covered; the summary counts
// them apart, and a BRD whose rows are all exercised reads exercised.
func TestExercisedRollUp(t *testing.T) {
	kg := fixture()
	kg.BRDs[0].SuccessCriteria = append(kg.BRDs[0].SuccessCriteria, schema.BRDCriterion{
		ID: "SC-2", Statement: "direct", DirectWire: true, EnforcedBy: []string{"D"}, VerifiedBy: []string{"TD"}})
	cov := fakeCoverage{perTest: map[string]map[string]bool{verifier: {enforcer: true}, "TD": {"D": true}}}
	m := Build(kg, Options{ResultOf: passing, Coverage: cov})
	s := m.Summaries[0]
	if s.Demonstrated != 2 || s.Exercised != 2 || s.WorstStatus != StatusExercised || s.VerificationRate != 1 {
		t.Errorf("summary = %+v", s)
	}
	if c := ComputeCoverage(m); c.VerifiedCriteria != 2 {
		t.Errorf("exercised counts as covered: %+v", c)
	}
	cov.perTest["TD"] = map[string]bool{}
	s = Build(kg, Options{ResultOf: passing, Coverage: cov}).Summaries[0]
	if s.Demonstrated != 2 || s.Exercised != 1 || s.WorstStatus != StatusDemonstrated {
		t.Errorf("one demonstrated, one exercised: %+v", s)
	}
}

// A scenario is exercised when a verifier's per-test run executed the
// handler of an entry point it names.
func TestScenarioExercised(t *testing.T) {
	kg := fixture()
	kg.EntryPoints = []schema.EntryPoint{{ID: "ep.refund", Handler: schema.Handler{Symbol: "api.refund"}}}
	kg.BRDs[0].UserScenarios = []schema.UserScenario{{ID: "US-1", VerifiedBy: []string{verifier, "ep.refund"}}}
	cov := fakeCoverage{perTest: map[string]map[string]bool{verifier: {"api.refund": true}}}
	m := Build(kg, Options{ResultOf: passing, Coverage: cov})
	if s := m.Scenarios[0]; s.Status != StatusExercised || m.Summaries[0].ScenarioExercised != 1 || m.Summaries[0].ScenarioDemonstrated != 1 {
		t.Errorf("scenario = %+v, summary %+v", s, m.Summaries[0])
	}
	if jc := ComputeJourneyCoverage(m); jc.Demonstrated != 1 {
		t.Errorf("journey demonstrated counts exercised: %+v", jc)
	}
	kg.BRDs[0].UserScenarios[0].VerifiedBy = []string{verifier}
	if s := Build(kg, Options{ResultOf: passing, Coverage: cov}).Scenarios[0]; s.Status != StatusDemonstrated || !strings.Contains(s.CoverageReason, "names no entry point") {
		t.Errorf("no entry point named: %s %q", s.Status, s.CoverageReason)
	}
}
