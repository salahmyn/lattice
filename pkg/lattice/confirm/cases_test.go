package confirm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// A promise's cases are part of its packet. Adding, editing or retiring a
// case after confirmation changes the hash, so the confirmed decision reads
// stale (PACKET_MISMATCH); a promise with no cases hashes exactly as it did
// before cases existed, so no earlier decision is voided.
// @verifies confirm:INV-16
func TestCasesArePartOfThePacket(t *testing.T) {
	b := schema.BRD{ID: "brd.x", Version: 2, Status: schema.BRDApproved, Title: "t",
		SuccessCriteria: []schema.BRDCriterion{{ID: "SC-1", Statement: "s", MapsToInvariant: schema.InvariantRefs{"f:INV-1"}}}}
	row := audit.Row{Promise: "f:INV-1", Statement: "A fee never exceeds five percent.", Tier: 2,
		Signals: audit.Signals{Enforced: audit.Signal{Items: []string{"f.Charge"}}, Verified: audit.Signal{Items: []string{"f.TestFee"}}}}
	hash := func(r audit.Row) string {
		return PacketHash(PacketInput{BRD: b, Promises: FactsFor([]audit.Row{r}, PromisesOf(b)), Diff: []string{"initial"}})
	}

	// The hash of a promise without cases, as the packet computed it before
	// cases were hashed (pinned from the release before them).
	const beforeCases = "sha256:1942b978f39f4455c83281d00c64d903ef45284fc34968f55996063fd17dd435"
	if got := hash(row); got != beforeCases {
		t.Errorf("a promise with no cases must hash as before: %s", got)
	}

	withCases := row
	withCases.Cases = []audit.CaseRow{
		{ID: "a", Kind: "happy", Statement: "A 100.00 withdrawal pays at most 5.00.", Coverage: audit.CaseCovered},
		{ID: "b", Kind: "edge", Statement: "A 0.01 withdrawal pays no fee.", Coverage: audit.CaseUncovered},
	}
	confirmed := hash(withCases)
	if confirmed == beforeCases {
		t.Fatal("adding a case must change the hash")
	}

	edit := func(f func(cs []audit.CaseRow) []audit.CaseRow) string {
		r := withCases
		r.Cases = f(append([]audit.CaseRow(nil), withCases.Cases...))
		return hash(r)
	}
	for name, h := range map[string]string{
		"statement edited": edit(func(cs []audit.CaseRow) []audit.CaseRow { cs[1].Statement = "A 0.01 withdrawal pays 0.01."; return cs }),
		"kind edited":      edit(func(cs []audit.CaseRow) []audit.CaseRow { cs[1].Kind = "error"; return cs }),
		"enforcer named":   edit(func(cs []audit.CaseRow) []audit.CaseRow { cs[0].Enforcer = "f.Charge"; return cs }),
		"retired":          edit(func(cs []audit.CaseRow) []audit.CaseRow { cs[1].Retired = true; return cs }),
		"added": edit(func(cs []audit.CaseRow) []audit.CaseRow {
			return append(cs, audit.CaseRow{ID: "c", Kind: "error", Statement: "x"})
		}),
	} {
		if h == confirmed {
			t.Errorf("%s: the hash must change", name)
		}
	}
	// Coverage is run-dependent: a better test run never voids a confirmation.
	if h := edit(func(cs []audit.CaseRow) []audit.CaseRow {
		cs[1].Coverage, cs[1].Reason = audit.CaseCovered, "killed"
		return cs
	}); h != confirmed {
		t.Error("a change in case coverage must not change the hash")
	}

	// End to end: a decision pinned on the confirmed packet reads stale once
	// a case is edited.
	d := decision(ActionApprove)
	d.PacketHash = confirmed
	plat := &fake{pr: merged("dev", "h2"), reviews: []Review{approval("ana", "h2")}}
	in := Input{Commit: "head1", Branch: "main", Found: foundOne(d), Platform: plat, Now: fixedNow}
	in.Packet = func(Decision) (string, bool) { return confirmed, true }
	if r := Compute(context.Background(), in).Records[0]; r.State != StateConfirmed {
		t.Fatalf("unchanged cases: %+v", r)
	}
	in.Packet = func(Decision) (string, bool) {
		return edit(func(cs []audit.CaseRow) []audit.CaseRow {
			cs[0].Statement = "A 100.00 withdrawal pays 5.00."
			return cs
		}), true
	}
	if r := Compute(context.Background(), in).Records[0]; r.State != StateStale || r.Assurance.Code != "PACKET_MISMATCH" {
		t.Errorf("an edited case must read stale: %+v", r)
	}
}

// A decision records the cases of each of its promises, every promise listed
// (one without cases with an empty list), and the verdict carries them into
// confirm.json, where readers compare them with the cases now. A decision
// file written before cases were recorded parses with none, and the packet
// hash does not depend on the list.
// @verifies confirm:INV-20
func TestDecisionRecordsCases(t *testing.T) {
	rows := []audit.Row{
		{Promise: "f:INV-1", Cases: []audit.CaseRow{{ID: "a", Kind: "happy", Statement: "A 100.00 withdrawal pays at most 5.00."},
			{ID: "b", Kind: "edge", Statement: "A 0.01 withdrawal pays no fee.", Enforcer: "f.Charge", Retired: true}}},
		{Promise: "f:INV-2"},
	}
	got := CasesFor(rows, []string{"f:INV-1", "f:INV-2", "f:INV-3"})
	if len(got) != 3 || len(got[0].Cases) != 2 || got[1].Cases == nil || len(got[1].Cases) != 0 || got[2].Cases == nil {
		t.Fatalf("cases = %+v", got)
	}
	b := got[0].Cases[1]
	if b.ID != "b" || b.Kind != schema.CaseEdge || b.Enforcer != "f.Charge" || !b.Retired ||
		b.Hash != schema.CaseHash(schema.CaseEdge, "A 0.01 withdrawal pays no fee.", "f.Charge") {
		t.Errorf("b = %+v", b)
	}

	d := decision(ActionApprove)
	d.Rows = []Row{{Promise: "f:INV-1"}, {Promise: "f:INV-2"}}
	d.Cases = got[:2]
	data, err := d.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "cases: []") {
		t.Errorf("a promise with no cases must be listed with an empty list:\n%s", data)
	}
	back, err := ParseDecision(data)
	if err != nil {
		t.Fatal(err)
	}
	if stamps, ok := schema.CasesOf(back.Cases, "f:INV-2"); !ok || len(stamps) != 0 {
		t.Errorf("f:INV-2 must read recorded with none: %v %v", stamps, ok)
	}
	if stamps, ok := schema.CasesOf(back.Cases, "f:INV-1"); !ok || len(stamps) != 2 || stamps[0].Hash != got[0].Cases[0].Hash {
		t.Errorf("f:INV-1 = %+v", stamps)
	}

	old, err := ParseDecision([]byte("action: brd.approve\nid: brd.x\nversion: 2\npacket_hash: sha256:aa\nrows:\n  - promise: f:INV-1\nprepared_by: d\nprepared_at: t\n"))
	if err != nil || old.Cases != nil {
		t.Fatalf("an old decision parses with no cases: %+v %v", old.Cases, err)
	}

	plat := &fake{pr: merged("dev", "h2"), reviews: []Review{approval("ana", "h2")}}
	in := Input{Commit: "head1", Branch: "main", Found: foundOne(back), Platform: plat, Now: fixedNow}
	in.Packet = func(Decision) (string, bool) { return back.PacketHash, true }
	r := Compute(context.Background(), in).Records[0]
	if r.State != StateConfirmed || len(r.Cases) != 2 || r.Cases[0].Promise != "f:INV-1" {
		t.Errorf("record = %+v", r)
	}
	body, _ := json.Marshal(r)
	if !strings.Contains(string(body), `"cases":[{"promise":"f:INV-1","cases":[{"id":"a"`) {
		t.Errorf("confirm.json record = %s", body)
	}
}
