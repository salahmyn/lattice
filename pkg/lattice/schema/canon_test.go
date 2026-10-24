package schema

import (
	"strings"
	"testing"
)

// A hand-ordered file is rewritten in struct order; a canonical file is
// left alone; a file whose rewrite would lose a comment, an anchor or an
// unknown key is skipped with the reason and no output.
// @verifies schema:INV-2
func TestCanonicalize(t *testing.T) {
	handOrdered := "id: brd.x\nversion: 1\nstatus: draft\ntitle: X\nbusiness_problem: p\nprovenance:\n  source: human\nopen_questions:\n  - q\n"
	res, err := Canonicalize([]byte(handOrdered), &BRD{})
	if err != nil || !res.Changed || res.Skipped != "" {
		t.Fatalf("hand-ordered: %+v, %v", res, err)
	}
	if out := string(res.Canonical); strings.Index(out, "open_questions:") > strings.Index(out, "provenance:") {
		t.Errorf("open_questions should come before provenance, got:\n%s", out)
	}
	again, err := Canonicalize(res.Canonical, &BRD{})
	if err != nil || again.Changed {
		t.Errorf("canonical output is not stable: %+v, %v", again, err)
	}

	for name, tc := range map[string]struct{ in, want string }{
		"head comment":   {"# owned by payments\nid: f\nversion: 1\n", "comment at line 2"},
		"line comment":   {"id: f\nversion: 1 # bumped\n", "comment at line 2"},
		"unknown key":    {"id: f\nversion: 1\nowner_team: x\n", "owner_team"},
		"anchor + alias": {"id: f\nversion: 1\npurpose: &p x\ntags: [*p]\n", "anchor or alias"},
	} {
		res, err := Canonicalize([]byte(tc.in), &Manifest{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.Changed || res.Canonical != nil || !strings.Contains(res.Skipped, tc.want) {
			t.Errorf("%s: %+v, want skipped naming %q", name, res, tc.want)
		}
	}
}
