package schema

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// maps_to_invariant takes one ref or a list. One ref is written back as a
// plain string, so files from before lists keep their shape on a rewrite.
func TestInvariantRefsYAML(t *testing.T) {
	for name, tc := range map[string]struct {
		in, out string
		want    InvariantRefs
	}{
		"one":           {"maps_to_invariant: a:INV-1\n", "maps_to_invariant: a:INV-1\n", InvariantRefs{"a:INV-1"}},
		"list":          {"maps_to_invariant: [a:INV-1, b:INV-2]\n", "maps_to_invariant:\n    - a:INV-1\n    - b:INV-2\n", InvariantRefs{"a:INV-1", "b:INV-2"}},
		"one-item list": {"maps_to_invariant: [a:INV-1]\n", "maps_to_invariant: a:INV-1\n", InvariantRefs{"a:INV-1"}},
		"blanks":        {"maps_to_invariant: [' ', a:INV-1]\n", "maps_to_invariant: a:INV-1\n", InvariantRefs{"a:INV-1"}},
		"empty":         {"maps_to_invariant: \"\"\n", "{}\n", nil},
	} {
		var c struct {
			MapsToInvariant InvariantRefs `yaml:"maps_to_invariant,omitempty"`
		}
		if err := yaml.Unmarshal([]byte(tc.in), &c); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if c.MapsToInvariant.String() != tc.want.String() || len(c.MapsToInvariant) != len(tc.want) {
			t.Errorf("%s: decoded %q, want %q", name, c.MapsToInvariant, tc.want)
		}
		out, err := yaml.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != tc.out {
			t.Errorf("%s: wrote %q, want %q", name, out, tc.out)
		}
	}

	var c BRDCriterion
	if err := yaml.Unmarshal([]byte("maps_to_invariant: {a: 1}\n"), &c); err == nil ||
		!strings.Contains(err.Error(), "maps_to_invariant") {
		t.Errorf("a mapping is refused naming the field, got %v", err)
	}
}

// A graph extracted before lists stores the ref as a JSON string.
func TestInvariantRefsJSON(t *testing.T) {
	var old, cur BRDCriterion
	if err := json.Unmarshal([]byte(`{"MapsToInvariant":"a:INV-1"}`), &old); err != nil || old.MapsToInvariant.String() != "a:INV-1" {
		t.Errorf("string form: %q, %v", old.MapsToInvariant, err)
	}
	b, _ := json.Marshal(BRDCriterion{MapsToInvariant: InvariantRefs{"a:INV-1", "b:INV-2"}})
	if err := json.Unmarshal(b, &cur); err != nil || len(cur.MapsToInvariant) != 2 {
		t.Errorf("list round trip: %s → %q, %v", b, cur.MapsToInvariant, err)
	}
}
