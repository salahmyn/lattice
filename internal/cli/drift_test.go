package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// driftFixture is a committed TypeScript repo, built from testdata/drift, whose
// one criterion is demonstrated by a hand-written passing result while the
// spec and the code disagree:
//
//   - shop.checkout is still a proposal, yet the flow of a production entry
//     point names it, and one of its two invariants is mapped by no criterion;
//   - shop.refunds is a proposal with code that no flow and no root reaches,
//     and its only invariant is mapped by no criterion.
func driftFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := copyTree(t, "testdata/drift")
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "fixture")
	junit := filepath.Join(t.TempDir(), "results.junit.xml")
	body := `<testsuites><testsuite name="shop">
<testcase classname="route" name="an order of 1 to 10 items is placed"/>
<testcase classname="route" name="an order of 0 or more than 10 items is refused"/>
<testcase classname="route" name="a refund needs an order id"/>
</testsuite></testsuites>`
	if err := os.WriteFile(junit, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, dir, newResultsCommand, "ingest", junit); err != nil {
		t.Fatalf("ingest: %v\n%s", err, out)
	}
	return dir
}

func driftValidate(t *testing.T, dir string, args ...string) (map[string][]schema.Violation, bool, int) {
	t.Helper()
	out, _ := run(t, dir, newValidateCommand, args...)
	var rep struct {
		Violations []schema.Violation `json:"violations"`
		Warnings   int                `json:"warnings"`
		OK         bool               `json:"ok"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("validate output: %v\n%s", err, out)
	}
	by := map[string][]schema.Violation{}
	for _, v := range rep.Violations {
		by[v.Code] = append(by[v.Code], v)
	}
	return by, rep.OK, rep.Warnings
}

// On the fixture, validate states the three drift facts and the RTM's header
// carries the second figure beside the unchanged criteria figure; a run that
// fails on warnings stays green with all of them present.
// @verifies validation:INV-18
// @verifies validation:INV-19
// @verifies validation:INV-20
// @verifies validation:INV-21
// @verifies rtm:INV-9
func TestDriftFindingsOnFixture(t *testing.T) {
	dir := driftFixture(t)
	by, ok, warnings := driftValidate(t, dir, "--fail-on", "warning")
	if warnings != 0 || !ok {
		t.Errorf("fail_on warning: ok=%v warnings=%d; the drift findings are info", ok, warnings)
	}

	subjects := func(code string) map[string]schema.Violation {
		m := map[string]schema.Violation{}
		for _, v := range by[code] {
			m[v.FeatureID] = v
			if v.Severity != schema.SeverityInfo {
				t.Errorf("%s on %s is %s, want info", code, v.FeatureID, v.Severity)
			}
		}
		return m
	}
	un := subjects(schema.CodeInvariantUnrequired)
	if len(un) != 2 || !strings.Contains(un["shop.checkout"].Message, "shop.checkout:INV-2") ||
		!strings.Contains(un["shop.refunds"].Message, "shop.refunds:INV-1") ||
		!strings.Contains(un["shop.checkout"].Message, "mapped by no criterion") {
		t.Errorf("INVARIANT_UNREQUIRED = %+v", by[schema.CodeInvariantUnrequired])
	}
	drift := subjects(schema.CodeStatusDrift)
	if len(drift) != 1 || !strings.Contains(drift["shop.checkout"].Message, "shop.orders_route") {
		t.Errorf("STATUS_DRIFT = %+v", by[schema.CodeStatusDrift])
	}
	unreached := subjects(schema.CodeFeatureUnreached)
	if len(unreached) != 1 || !strings.Contains(unreached["shop.refunds"].Message, "proposal feature") {
		t.Errorf("FEATURE_UNREACHED = %+v", by[schema.CodeFeatureUnreached])
	}

	out, err := run(t, dir, newRTMCommand)
	if err != nil {
		t.Fatalf("rtm: %v\n%s", err, out)
	}
	var rep struct {
		Coverage          rtm.Coverage          `json:"coverage"`
		InvariantCoverage rtm.InvariantCoverage `json:"invariant_coverage"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("rtm output: %v\n%s", err, out)
	}
	if rep.Coverage.TotalCriteria != 1 || rep.Coverage.VerifiedCriteria != 1 {
		t.Errorf("criteria figure = %+v, want 1 of 1", rep.Coverage)
	}
	if rep.InvariantCoverage.TotalInvariants != 3 || rep.InvariantCoverage.RequiredInvariants != 1 {
		t.Errorf("invariant figure = %+v, want 1 of 3", rep.InvariantCoverage)
	}

	var text, errOut bytes.Buffer
	cmd := newRTMCommand(&IO{Repo: dir, Out: &text, Err: &errOut})
	cmd.SetArgs([]string{"--summary"})
	cmd.SetOut(&text)
	cmd.SetErr(&errOut)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rtm text: %v\n%s", err, errOut.String())
	}
	if !strings.Contains(text.String(), "(1/1 criteria verified)") || !strings.Contains(text.String(), "invariants:                1/3 required") {
		t.Errorf("rtm header:\n%s", text.String())
	}
}
