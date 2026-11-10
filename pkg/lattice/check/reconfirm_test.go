package check

import (
	"errors"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/confirm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// A change re-confirms its promises with a decision file of either naming
// style: <id>-v<N>[.<action>] (written before names carried the packet) or
// <id>-v<N>[.<action>]-<packet8>. The content decides, not the name.
// @verifies confirm:INV-7
func TestReconfirmedReadsBothNamingStyles(t *testing.T) {
	brds := []schema.BRD{
		{ID: "brd.fees", Version: 2, SuccessCriteria: []schema.BRDCriterion{{ID: "SC-1", MapsToInvariant: schema.InvariantRefs{"fees:INV-1"}}}},
		{ID: "brd.wallet", Version: 1, SuccessCriteria: []schema.BRDCriterion{{ID: "SC-1", MapsToInvariant: schema.InvariantRefs{"wallet:INV-2"}}}},
	}
	approve := confirm.Decision{Action: confirm.ActionApprove, ID: "brd.fees", Version: 2, PacketHash: "sha256:0123456789abcdef"}
	promise := confirm.Decision{Action: confirm.ActionPromiseConfirm, ID: "brd.wallet", Version: 1, PacketHash: "sha256:fedcba9876543210",
		Rows: []confirm.Row{{Promise: "wallet:INV-2"}}}
	for _, tc := range []struct {
		name string
		path func(confirm.Decision) string
	}{
		{"legacy", func(d confirm.Decision) string { return "lattice/decisions/approvals/" + d.LegacyStem() + ".yaml" }},
		{"packet", func(d confirm.Decision) string { return "lattice/" + d.RelPath() }},
	} {
		files := map[string]confirm.Decision{tc.path(approve): approve, tc.path(promise): promise}
		var paths []string
		for p := range files {
			paths = append(paths, p)
		}
		read := func(p string) ([]byte, error) {
			d, ok := files[p]
			if !ok {
				return nil, errors.New("missing")
			}
			return d.Marshal()
		}
		got := Reconfirmed(paths, "lattice", read, brds)
		if !got["fees:INV-1"] || !got["wallet:INV-2"] || len(got) != 2 {
			t.Errorf("%s names (%v): re-confirmed %v", tc.name, paths, got)
		}
	}
	if approve.LegacyStem() == approve.Stem() {
		t.Fatal("fixture: the two styles must differ")
	}
}
