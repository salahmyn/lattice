package confirm

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func brdWithSources(t *testing.T, sources string) schema.BRD {
	t.Helper()
	var b schema.BRD
	body := "id: brd.fees\nversion: 2\nstatus: approved\ntitle: Fees\nbusiness_problem: p\n" +
		"success_criteria: [{id: SC-1, statement: s, maps_to_invariant: 'fees:INV-1'}]\n" + sources
	if err := yaml.Unmarshal([]byte(body), &b); err != nil {
		t.Fatal(err)
	}
	return b
}

// A source is part of what the reviewer read: editing one makes the decision
// stale, as a text-only change that the structural diff does not list. A
// human's checked mark records a later check and never voids the decision.
// @verifies confirm:INV-3
func TestPacketHashSources(t *testing.T) {
	base := brdWithSources(t, "sources: [{kind: ticket, ref: WAL-12, url: 'https://tracker.example/WAL-12'}]\n")
	hash := func(b schema.BRD) string { return PacketHash(PacketInput{BRD: b, Diff: []string{"initial"}}) }
	h := hash(base)

	checked := brdWithSources(t, "sources: [{kind: ticket, ref: WAL-12, url: 'https://tracker.example/WAL-12', checked_by: ana@example.com, checked_at: 2026-11-10}]\n")
	if hash(checked) != h {
		t.Error("a checked mark must not change the packet hash")
	}
	if out, _ := yaml.Marshal(checked); !strings.Contains(string(out), "checked_by: ana@example.com") {
		t.Errorf("PacketHash must not clear the caller's checked mark:\n%s", out)
	}

	for name, src := range map[string]string{
		"ref":   "sources: [{kind: ticket, ref: WAL-13, url: 'https://tracker.example/WAL-12'}]\n",
		"url":   "sources: [{kind: ticket, ref: WAL-12, url: 'https://tracker.example/WAL-13'}]\n",
		"title": "sources: [{kind: ticket, ref: WAL-12, title: Fees, url: 'https://tracker.example/WAL-12'}]\n",
		"added": "sources: [{kind: ticket, ref: WAL-12, url: 'https://tracker.example/WAL-12'}, {kind: adr, ref: ADR-7}]\n",
	} {
		edited := brdWithSources(t, src)
		if hash(edited) == h {
			t.Errorf("editing a source's %s must change the packet hash", name)
		}
		if d := StructuralDiff(&base, edited); len(d) != 1 || d[0] != "none" {
			t.Errorf("editing a source's %s is text-only, not structural: %v", name, d)
		}
	}
}
