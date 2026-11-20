package schema

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// An invariant's cases decode in either YAML style, are written after the
// invariant's own fields in struct order, and a second format pass changes
// nothing. A key a case does not have (a hand-typed confirmation) is never
// silently dropped: the file is skipped with the reason.
// @verifies schema:INV-2
func TestInvariantCasesRoundTrip(t *testing.T) {
	in := `id: wallet.fees
version: 1
status: production
purpose: Charges withdrawal fees.
owners: {business: payments, engineering: wallet}
invariants:
  - id: INV-1
    statement: A fee never exceeds five percent of the amount.
    cases:
      - { id: a, kind: happy, statement: A 100.00 withdrawal pays at most 5.00. }
      - { id: b, kind: edge,  statement: A 0.01 withdrawal pays no fee. }
      - { id: c, kind: error, statement: A negative amount is rejected first., enforcer: wallet.fees.validateAmount, retired: true }
`
	res, err := Canonicalize([]byte(in), &Manifest{})
	if err != nil || res.Skipped != "" {
		t.Fatalf("canonicalize: %+v, %v", res, err)
	}
	out := string(res.Canonical)
	want := `invariants:
  - id: INV-1
    statement: A fee never exceeds five percent of the amount.
    cases:
      - id: a
        kind: happy
        statement: A 100.00 withdrawal pays at most 5.00.
      - id: b
        kind: edge
        statement: A 0.01 withdrawal pays no fee.
      - id: c
        kind: error
        statement: A negative amount is rejected first.
        enforcer: wallet.fees.validateAmount
        retired: true
`
	if !strings.HasSuffix(out, want) {
		t.Errorf("canonical cases:\n%s\nwant suffix:\n%s", out, want)
	}
	again, err := Canonicalize(res.Canonical, &Manifest{})
	if err != nil || again.Changed {
		t.Errorf("canonical output is not stable: %+v, %v", again, err)
	}
	var m Manifest
	if err := yaml.Unmarshal(res.Canonical, &m); err != nil {
		t.Fatal(err)
	}
	cs := m.Invariants[0].Cases
	if len(cs) != 3 || cs[2].Kind != CaseError || cs[2].Enforcer != "wallet.fees.validateAmount" || !cs[2].Retired || cs[0].Retired {
		t.Errorf("decoded cases = %+v", cs)
	}
	b, _ := json.Marshal(m.Invariants[0])
	if !strings.Contains(string(b), `"cases":[{"id":"a","kind":"happy","statement":"A 100.00 withdrawal pays at most 5.00."}`) {
		t.Errorf("json = %s", b)
	}
	noCases, _ := json.Marshal(Invariant{ID: "INV-2", Statement: "s"})
	if strings.Contains(string(noCases), "cases") {
		t.Errorf("an invariant without cases gains a cases key: %s", noCases)
	}

	typed := strings.Replace(in, "edge,  statement", "edge, confirmed: true, statement", 1)
	res, err = Canonicalize([]byte(typed), &Manifest{})
	if err != nil || res.Canonical != nil || !strings.Contains(res.Skipped, "confirmed") {
		t.Errorf("a typed confirmation must not be dropped silently: %+v, %v", res, err)
	}
}

func TestCaseRefs(t *testing.T) {
	for ref, want := range map[string][3]string{
		"wallet.fees:INV-1#a":  {"wallet.fees:INV-1", "a", "true"},
		"INV-2#ab":             {"INV-2", "ab", "true"},
		"wallet.fees:INV-1":    {"wallet.fees:INV-1", "", "false"},
		"wallet.fees:INV-1#":   {"wallet.fees:INV-1", "", "true"},
		"brd.wallet:SC-1":      {"brd.wallet:SC-1", "", "false"},
		"wallet.fees:INV-12#c": {"wallet.fees:INV-12", "c", "true"},
	} {
		base, id, ok := SplitCaseRef(ref)
		if base != want[0] || id != want[1] || (ok != (want[2] == "true")) {
			t.Errorf("SplitCaseRef(%q) = %q, %q, %v", ref, base, id, ok)
		}
	}
	for in, want := range map[[2]string]string{
		{"INV-1", "a"}: "1a", {"INV-12", "ab"}: "12ab", {"LIMIT", "a"}: "LIMIT#a", {"INV-", "a"}: "INV-#a",
	} {
		if got := CaseLabel(in[0], in[1]); got != want {
			t.Errorf("CaseLabel(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}
