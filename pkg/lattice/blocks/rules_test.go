package blocks

import (
	"strings"
	"testing"
)

func TestCompileID(t *testing.T) {
	cases := []struct {
		pattern, id string
		want        bool
	}{
		{"fe:payment.gateway.*", "fe:payment.gateway.tap", true},
		{"fe:payment.gateway.*", "fe:payment.gateway.tap.otp", false},
		{"fe:**", "fe:payment.gateway.tap.otp", true},
		{"fe:**", "be:orders", false},
		{"fe:cart.?tems", "fe:cart.items", true},
		{"a+b.*", "a+b.c", true}, // regexp metacharacters are literal
	}
	for _, c := range cases {
		if got := compileID(c.pattern).MatchString(c.id); got != c.want {
			t.Errorf("%s ~ %s = %v want %v", c.pattern, c.id, got, c.want)
		}
	}
	if compileID("") != nil {
		t.Error("an empty pattern should compile to nil")
	}
}

func TestEdgeJudgmentPatterns(t *testing.T) {
	in := shopInputs(t)
	in.Judgments = append(in.Judgments,
		Judgment{ID: "ext", Type: "external", Entries: []JudgmentEntry{{ID: "ext.x", Title: "X"}}},
		Judgment{ID: "fan", Type: "edge", Entries: []JudgmentEntry{
			{From: "fe:cart.*", To: "ext.x", Kind: "uses", Exclude: []string{"fe:cart.checkout"}},
			{From: "fe:nothing.*", To: "ext.x", Kind: "uses"},
		}},
	)
	r := Build(in)
	e := edgeSet(r)
	if _, ok := e["fe:cart.items -uses-> ext.x"]; !ok {
		t.Errorf("pattern edge missing: %v", e)
	}
	for k := range e {
		if strings.HasPrefix(k, "fe:cart.checkout") && strings.HasSuffix(k, "-uses-> ext.x") {
			t.Errorf("excluded or nested block got an edge: %s", k)
		}
	}
	if !strings.Contains(strings.Join(r.Warnings, "\n"), `"fe:nothing.*" matches no block`) {
		t.Errorf("warnings = %v", r.Warnings)
	}
}
