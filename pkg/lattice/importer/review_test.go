package importer

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadDecisionsFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "decisions.yaml")
	body := `
version: 1
decisions:
  cand_a: accept
  cand_b: rejected
  cand_c: ACCEPT
`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadDecisionsFile(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := map[string]string{
		"cand_a": DecisionAccepted,
		"cand_b": DecisionRejected,
		"cand_c": DecisionAccepted,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestLoadDecisionsFileRejectsUnknownAction(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.yaml")
	_ = os.WriteFile(p, []byte(`version: 1
decisions:
  cand_x: maybe
`), 0o644)
	if _, err := LoadDecisionsFile(p); err == nil {
		t.Error("expected error for unknown action")
	}
}

// TestParseWhere covers the predicate syntax brownfield reviewers need: a
// prefix-match on package and numeric thresholds on confidence/symbols.
func TestParseWhere(t *testing.T) {
	c := Candidate{Package: "modules/Accounts/Http", Confidence: 0.7,
		Symbols: []string{"a", "b", "c"}}

	cases := []struct {
		expr string
		want bool
	}{
		{"package=modules/Accounts", true},
		{"package=modules/Webhook", false},
		{"confidence>=0.7", true},
		{"confidence>0.7", false},
		{"confidence<=0.5", false},
		{"confidence=0.7", true},
		{"symbols<5", true},
		{"symbols>3", false},
		{"symbols=3", true},
	}
	for _, tc := range cases {
		p, err := ParseWhere(tc.expr)
		if err != nil {
			t.Errorf("%q: parse: %v", tc.expr, err)
			continue
		}
		if got := p(c); got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestParseWhereErrors(t *testing.T) {
	for _, bad := range []string{
		"",                   // empty
		"package",            // no op
		"package>=foo",       // wrong op for string key
		"confidence=banana",  // non-numeric
		"unknown_key=x",      // unknown key
		"=foo",               // missing key
	} {
		if _, err := ParseWhere(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestAndPredicates(t *testing.T) {
	c := Candidate{Package: "modules/X", Confidence: 0.8, Symbols: []string{"a", "b"}}
	p1, _ := ParseWhere("package=modules/X")
	p2, _ := ParseWhere("confidence>=0.5")
	p3, _ := ParseWhere("symbols>5") // false
	if !AndPredicates([]CandidatePredicate{p1, p2})(c) {
		t.Error("two true predicates should AND to true")
	}
	if AndPredicates([]CandidatePredicate{p1, p3})(c) {
		t.Error("any false predicate should AND to false")
	}
	if !AndPredicates(nil)(c) {
		t.Error("empty AND should be a tautology")
	}
}
