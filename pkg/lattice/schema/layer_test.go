package schema

import "testing"

// The first valid rule whose glob matches decides; an invalid rule is skipped
// and a file no rule matches reads unknown.
func TestLayerOfFile(t *testing.T) {
	rules := []LayerRule{
		{Glob: "web/**", Layer: "frontend"}, // not in the vocabulary: skipped
		{Glob: "", Layer: LayerAPI},         // no glob: skipped
		{Glob: "web/admin/**", Layer: LayerService},
		{Glob: "web/**", Layer: LayerUI},
	}
	for file, want := range map[string]string{
		"web/admin/page.ts": LayerService,
		"web/cart.ts":       LayerUI,
		"lib/round.ts":      LayerUnknown,
	} {
		if got := LayerOfFile(rules, file); got != want {
			t.Errorf("%s = %q, want %q", file, got, want)
		}
	}
	if got := LayerOfFile([]LayerRule{{Glob: "web/**", Layer: LayerUnknown}, {Glob: "web/**", Layer: LayerUI}}, "web/a.ts"); got != LayerUnknown {
		t.Errorf("a rule that says unknown still wins first: %q", got)
	}
}
