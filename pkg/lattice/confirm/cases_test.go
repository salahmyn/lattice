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

// The structural diff a reviewer sees names every case added, edited (with
// what changed), retired or removed since the last confirmation recorded it,
// and any such line makes the review take full friction. A promise whose
// confirmation recorded no cases says so; one never confirmed adds nothing.
// @verifies confirm:INV-21
func TestStructuralDiffNamesCaseEdits(t *testing.T) {
	b := schema.BRD{ID: "brd.x", Version: 2, SuccessCriteria: []schema.BRDCriterion{
		{ID: "SC-1", MapsToInvariant: schema.InvariantRefs{"f:INV-1"}}, {ID: "SC-2", MapsToInvariant: schema.InvariantRefs{"g:INV-2"}}}}
	stamp := func(id string, kind schema.CaseKind, st string) schema.CaseStamp {
		return schema.NewCaseStamp(id, kind, st, "", false)
	}
	rec := func(state string, version int, cases []schema.PromiseCases) Record {
		return Record{ID: "brd.x", Version: version, Action: ActionApprove, State: state, ApproverLogin: "ana", PR: 7, Cases: cases, CasesCheck: CasesVerified}
	}
	held := []schema.PromiseCases{{Promise: "f:INV-1", Cases: []schema.CaseStamp{
		stamp("a", schema.CaseHappy, "A 100.00 withdrawal pays at most 5.00."),
		stamp("b", schema.CaseEdge, "A 0.01 withdrawal pays no fee."),
		stamp("c", schema.CaseError, "A negative amount is rejected first."),
		stamp("d", schema.CaseEdge, "Zero pays nothing."),
	}}, {Promise: "g:INV-2", Cases: []schema.CaseStamp{}}}
	rows := []audit.Row{
		{Promise: "f:INV-1", Cases: []audit.CaseRow{
			{ID: "a", Kind: "happy", Statement: "A 100.00 withdrawal pays at most 5.00."},
			{ID: "b", Kind: "error", Statement: "A 0.01 withdrawal pays no fee."},
			{ID: "c", Kind: "error", Statement: "A negative amount is rejected first.", Retired: true},
			{ID: "e", Kind: "security", Statement: "Only the owner sees the fee."},
		}},
		{Promise: "g:INV-2", Cases: []audit.CaseRow{{ID: "a", Kind: "happy", Statement: "x"}}},
	}
	cache := &File{Records: []Record{rec(StateStale, 2, held)}}
	got := CaseDiff(cache, b, rows)
	want := []string{
		"case 1b of f:INV-1 edited: kind edge->error",
		"case 1c of f:INV-1 retired",
		"case 1d of f:INV-1 removed",
		"case 1e of f:INV-1 added",
		"case 2a of g:INV-2 added",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("case diff:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	diff := WithCaseDiff([]string{"none"}, got)
	if diff[0] != want[0] || len(diff) != len(want) || FrictionOf(diff) != FrictionFull {
		t.Errorf("a case edit is structural: %v %s", diff, FrictionOf(diff))
	}
	if FrictionOf([]string{"none"}) != FrictionLight || FrictionOf([]string{"initial"}) != FrictionFull || FrictionOf(nil) != FrictionFull {
		t.Error("friction: none is light; initial and unknown are full")
	}
	if WithCaseDiff([]string{"none"}, nil)[0] != "none" {
		t.Error("no case lines keep none")
	}

	// Unchanged cases add nothing; an older version's confirmation is the
	// base when the current version has none.
	same := []audit.Row{{Promise: "f:INV-1", Cases: []audit.CaseRow{
		{ID: "a", Kind: "happy", Statement: "A 100.00 withdrawal pays at most 5.00."},
		{ID: "b", Kind: "edge", Statement: "A 0.01 withdrawal pays no fee."},
		{ID: "c", Kind: "error", Statement: "A negative amount is rejected first."},
		{ID: "d", Kind: "edge", Statement: "Zero pays nothing."},
	}}}
	if got := CaseDiff(&File{Records: []Record{rec(StateConfirmed, 1, held)}}, b, same); len(got) != 0 {
		t.Errorf("unchanged: %v", got)
	}
	// A case list that contradicts its packet, or one never bound to it,
	// is never read as unchanged: the review stays full.
	mismatch, unchecked := rec(StateConfirmed, 1, held), rec(StateConfirmed, 1, held)
	mismatch.CasesCheck, unchecked.CasesCheck = CasesMismatch, ""
	if got := CaseDiff(&File{Records: []Record{mismatch}}, b, same); len(got) != 2 ||
		got[0] != "cases of f:INV-1 recorded at the last confirmation do not match its packet" || FrictionOf(WithCaseDiff([]string{"none"}, got)) != FrictionFull {
		t.Errorf("mismatch: %v", got)
	}
	if got := CaseDiff(&File{Records: []Record{unchecked}}, b, same); len(got) != 1 ||
		got[0] != "cases of f:INV-1 at the last confirmation were not checked against its packet" {
		t.Errorf("unchecked: %v", got)
	}
	// A confirmation that recorded no cases says so; none at all adds nothing.
	if got := CaseDiff(&File{Records: []Record{rec(StateConfirmed, 2, nil)}}, b, rows); len(got) != 2 ||
		got[0] != "cases of f:INV-1 not recorded at the last confirmation" {
		t.Errorf("not recorded: %v", got)
	}
	for name, c := range map[string]*File{"no cache": nil, "no record": {}, "pending": {Records: []Record{rec(StatePending, 2, held)}}} {
		if got := CaseDiff(c, b, rows); len(got) != 0 {
			t.Errorf("%s: %v", name, got)
		}
	}
}

// A decision's case list counts only when lattice confirm binds it to the
// packet the decision pinned. Its cases are hashed in the packet, so while
// the packet still matches, the cases now are the confirmed ones and the
// list must equal them. Here the decision's cases: list is edited by hand
// after confirmation (the packet and the cases are untouched): the record
// reads mismatch, and the audit reads the edited case CASE_STAMP_MISMATCH,
// never confirmed. The honest list is verified, a verified list is carried
// from an intact cache once the packet moves on, and nothing else is.
// @verifies confirm:INV-20#f
func TestHandEditedCaseListIsNotConfirmed(t *testing.T) {
	cases := []audit.CaseRow{{ID: "a", Kind: "happy", Statement: "A 100.00 withdrawal pays at most 5.00."},
		{ID: "b", Kind: "edge", Statement: "A 0.01 withdrawal pays no fee."}}
	rows := []audit.Row{{Promise: "f:INV-1", Cases: cases}}
	current := func(refs []string) []schema.PromiseCases { return CasesFor(rows, refs) }

	honest := decision(ActionApprove)
	honest.Rows = []Row{{Promise: "f:INV-1"}}
	honest.Cases = CasesFor(rows, []string{"f:INV-1"})
	// After confirmation, someone edits the decision file to claim case b
	// was confirmed as an error case (say, to cover a later edit).
	forged := honest
	forged.Cases = []schema.PromiseCases{{Promise: "f:INV-1", Cases: append([]schema.CaseStamp(nil), honest.Cases[0].Cases...)}}
	forged.Cases[0].Cases[1] = schema.NewCaseStamp("b", schema.CaseError, "A 0.01 withdrawal pays no fee.", "", false)
	// Or adds an entry for a promise the decision does not cover.
	extra := honest
	extra.Cases = append(append([]schema.PromiseCases(nil), honest.Cases...), schema.PromiseCases{Promise: "g:INV-9", Cases: []schema.CaseStamp{}})

	compute := func(d Decision, matches bool, cache *File) Record {
		plat := &fake{pr: merged("dev", "h2"), reviews: []Review{approval("ana", "h2")}}
		in := Input{Commit: "head1", Branch: "main", Found: foundOne(d), Platform: plat, Now: fixedNow, CurrentCases: current, Cache: cache}
		in.Packet = func(Decision) (string, bool) {
			if matches {
				return d.PacketHash, true
			}
			return "sha256:other", true
		}
		return Compute(context.Background(), in).Records[0]
	}
	if r := compute(honest, true, nil); r.CasesCheck != CasesVerified {
		t.Errorf("honest list: %q", r.CasesCheck)
	}
	for name, d := range map[string]Decision{"edited stamp": forged, "extra promise": extra} {
		if r := compute(d, true, nil); r.State != StateConfirmed || r.CasesCheck != CasesMismatch {
			t.Errorf("%s: state %s, check %q", name, r.State, r.CasesCheck)
		}
	}
	if r := compute(honest, false, nil); r.State != StateStale || r.CasesCheck != "" {
		t.Errorf("a stale packet cannot vouch for the list: %s %q", r.State, r.CasesCheck)
	}
	verified := compute(honest, true, nil)
	cache := Seal(File{Contract: Schema, Commit: "head0", Records: []Record{verified}}, Stamp{CIRun: "local"})
	if r := compute(honest, false, &cache); r.CasesCheck != CasesVerified {
		t.Errorf("a list an intact cache verified stays verified: %q", r.CasesCheck)
	}
	if r := compute(forged, false, &cache); r.CasesCheck != "" {
		t.Errorf("a list the cache did not verify is unchecked: %q", r.CasesCheck)
	}

	// The audit reads the record from confirm.json and does not confirm the
	// edited case, while the promise still reads human.
	body, _ := json.Marshal(compute(forged, true, nil))
	var rec audit.ConfirmRecord
	if err := json.Unmarshal(body, &rec); err != nil {
		t.Fatal(err)
	}
	rec.ID = "brd.x"
	kg := schema.KnowledgeGraph{GeneratedFromCommit: "head1",
		Features: []schema.Manifest{{ID: "f", Invariants: []schema.Invariant{{ID: "INV-1", Statement: "s", Cases: []schema.InvariantCase{
			{ID: "a", Kind: schema.CaseHappy, Statement: cases[0].Statement}, {ID: "b", Kind: schema.CaseError, Statement: cases[1].Statement}}}}}},
		BRDs: []schema.BRD{{ID: "brd.x", Version: 2, SuccessCriteria: []schema.BRDCriterion{{ID: "SC-1", MapsToInvariant: schema.InvariantRefs{"f:INV-1"}}}}},
	}
	row := audit.Compute(audit.Sources{KG: kg, Confirm: &audit.ConfirmData{Records: []audit.ConfirmRecord{rec}}}).Rows[0]
	if row.Signals.Confirmed.Value != audit.ConfirmedHuman {
		t.Errorf("promise = %s", row.Signals.Confirmed.Value)
	}
	for _, c := range row.Cases {
		if c.Confirmation.State == audit.CaseConfirmed || c.Confirmation.Code != audit.CaseCodeStampMismatch {
			t.Errorf("%s = %+v", c.Label, c.Confirmation)
		}
	}
}
