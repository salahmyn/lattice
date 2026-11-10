package audit

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/confirm/stamp"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func brdFor(id string, status schema.BRDStatus, version int, ap *schema.BRDApproval) schema.BRD {
	return schema.BRD{ID: id, Status: status, Version: version, Approval: ap,
		SuccessCriteria: []schema.BRDCriterion{{ID: "SC-1", Statement: "s", Tier: 3, MapsToInvariant: schema.InvariantRefs{"f:INV-1"}}}}
}

func confirmedOf(t *testing.T, brds []schema.BRD, cd *ConfirmData) Row {
	t.Helper()
	kg := graph(inv("INV-1"), []string{"INV-1"}, map[string][]string{"INV-1": {"f.TestOne"}})
	kg.BRDs = brds
	return Compute(Sources{KG: kg, Results: pass("f.TestOne"), Confirm: cd}).Rows[0]
}

// The confirmed signal reads only confirm.json. A BRD's own approval block,
// however it is written, never makes a promise human-confirmed.
// @verifies audit:INV-5
func TestConfirmedReadsOnlyConfirmJSON(t *testing.T) {
	typed := &schema.BRDApproval{ApprovedBy: "ana@example.com", ApprovedVersion: 2}
	council := &schema.BRDApproval{ApprovedBy: "council:m1", Mandate: "m1", ApprovedVersion: 2,
		HumanReview: schema.HumanReviewAcknowledged, ReviewedBy: "ana@example.com"}
	local := &schema.BRDApproval{ApprovedBy: "ana@example.com", ApprovedVersion: 2, Local: true}
	rec := func(action, state string, version int, rows ...string) ConfirmRecord {
		return ConfirmRecord{ID: "b", Version: version, Action: action, State: state, ApproverLogin: "ana", PR: 7, Rows: rows}
	}
	empty := &ConfirmData{}

	cases := []struct {
		name string
		b    schema.BRD
		cd   *ConfirmData
		want string
	}{
		{"draft", brdFor("b", schema.BRDDraft, 2, nil), empty, ConfirmedDraft},
		{"proposed", brdFor("b", schema.BRDProposed, 2, nil), nil, ConfirmedDraft},
		{"hand-written approval, no confirm data", brdFor("b", schema.BRDApproved, 2, typed), nil, ConfirmedUnknown},
		{"hand-written approval, no record", brdFor("b", schema.BRDApproved, 2, typed), empty, ConfirmedUnknown},
		{"hand-written council ack stays agent", brdFor("b", schema.BRDApproved, 2, council), empty, ConfirmedAgent},
		{"agent actor", brdFor("b", schema.BRDApproved, 2, &schema.BRDApproval{ApprovedBy: "agent:approver", ApprovedVersion: 2}), empty, ConfirmedAgent},
		{"local approval is pending", brdFor("b", schema.BRDApproved, 2, local), empty, ConfirmedPending},
		{"verified approval", brdFor("b", schema.BRDDraft, 2, nil),
			&ConfirmData{Records: []ConfirmRecord{rec("brd.approve", "confirmed", 2)}}, ConfirmedHuman},
		{"verified ack of a council approval", brdFor("b", schema.BRDApproved, 2, council),
			&ConfirmData{Records: []ConfirmRecord{rec("brd.ack", "confirmed", 2)}}, ConfirmedHuman},
		{"promise confirm covers the promise", brdFor("b", schema.BRDDraft, 2, nil),
			&ConfirmData{Records: []ConfirmRecord{rec("promise.confirm", "confirmed", 2, "f:INV-1")}}, ConfirmedHuman},
		{"promise confirm of another promise", brdFor("b", schema.BRDDraft, 2, nil),
			&ConfirmData{Records: []ConfirmRecord{rec("promise.confirm", "confirmed", 2, "f:INV-9")}}, ConfirmedDraft},
		{"prepared, not merged", brdFor("b", schema.BRDDraft, 2, nil),
			&ConfirmData{Records: []ConfirmRecord{rec("brd.approve", "pending", 2)}}, ConfirmedPending},
		{"packet changed", brdFor("b", schema.BRDDraft, 2, nil),
			&ConfirmData{Records: []ConfirmRecord{rec("brd.approve", "stale", 2)}}, ConfirmedDrifted},
		{"confirmed at an older version", brdFor("b", schema.BRDDraft, 2, nil),
			&ConfirmData{Records: []ConfirmRecord{rec("brd.approve", "confirmed", 1)}}, ConfirmedDrifted},
		{"returned", brdFor("b", schema.BRDDraft, 2, nil),
			&ConfirmData{Records: []ConfirmRecord{rec("brd.approve", "confirmed", 2), rec("brd.return", "confirmed", 2)}}, ConfirmedDraft},
		{"another BRD's record", brdFor("other", schema.BRDApproved, 2, typed),
			&ConfirmData{Records: []ConfirmRecord{rec("brd.approve", "confirmed", 2)}}, ConfirmedUnknown},
	}
	for _, c := range cases {
		r := confirmedOf(t, []schema.BRD{c.b}, c.cd)
		if got := r.Signals.Confirmed.Value; got != c.want {
			t.Errorf("%s: confirmed = %s, want %s (%s)", c.name, got, c.want, r.Signals.Confirmed.Detail)
		}
		if r.Signals.Confirmed.Honesty != Proxy || r.Tier != 3 {
			t.Errorf("%s: honesty %s tier %d", c.name, r.Signals.Confirmed.Honesty, r.Tier)
		}
	}

	// A verified approval names the platform's login, not the BRD's typed one.
	r := confirmedOf(t, []schema.BRD{brdFor("b", schema.BRDApproved, 2, typed)},
		&ConfirmData{Records: []ConfirmRecord{{ID: "b", Version: 2, Action: "brd.approve", State: "confirmed", ApproverLogin: "platform-ana", PR: 3}}})
	if got := r.Evidence.Approvals[0].ApprovedBy; got != "platform-ana" {
		t.Errorf("approver = %q, want the platform login", got)
	}

	// Two BRDs: the least confirmed decides.
	r = confirmedOf(t, []schema.BRD{
		brdFor("a", schema.BRDDraft, 2, nil), brdFor("b", schema.BRDDraft, 2, nil)},
		&ConfirmData{Records: []ConfirmRecord{{ID: "a", Version: 2, Action: "brd.approve", State: "confirmed", ApproverLogin: "ana", PR: 1}}})
	if got := r.Signals.Confirmed.Value; got != ConfirmedDraft {
		t.Errorf("two BRDs: %s", got)
	}
	if got := confirmedOf(t, nil, nil).Signals.Confirmed.Value; got != ConfirmedNone {
		t.Errorf("no BRD: %s", got)
	}
}

// LoadConfirm reads the file the confirm command writes and nothing else.
// @verifies audit:INV-5
func TestLoadConfirm(t *testing.T) {
	dir := t.TempDir()
	if LoadConfirm(dir) != nil {
		t.Fatal("no file must read as no confirm data")
	}
	write := func(body string) {
		p := filepath.Join(dir, ".cache", "confirm.json")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"contract":"other/9","records":[{"id":"b","version":1,"action":"brd.approve","state":"confirmed"}]}`)
	if LoadConfirm(dir) != nil {
		t.Error("a file of another schema must be set aside")
	}
	write(`{"contract":"lattice.confirm/1","commit":"abc","records":[{"id":"b","version":1,"action":"brd.approve","state":"confirmed","approver_login":"ana","pr":4}]}`)
	cd := LoadConfirm(dir)
	if cd == nil || len(cd.Records) != 1 || cd.Records[0].ApproverLogin != "ana" || cd.Commit != "abc" {
		t.Fatalf("loaded %+v", cd)
	}
	if src := Collect(dir, schema.KnowledgeGraph{}, false); src.Confirm == nil {
		t.Error("Collect must carry confirm.json")
	}
}

// confirm.json is read as the record of a run only while its stamp is intact:
// a hand-written file with no stamp, a stamped file with a verdict edited, or
// a file git tracks reads unknown, never human. A local run's file still
// shows, marked local; under --ci a local or other-commit file reads unknown.
// @verifies confirm:INV-6
func TestConfirmJSONProvenance(t *testing.T) {
	b := brdFor("b", schema.BRDDraft, 2, nil)
	prepared := []ConfirmRecord{{ID: "b", Version: 2, Action: "brd.approve", State: "pending"}}
	approved := []ConfirmRecord{{ID: "b", Version: 2, Action: "brd.approve", State: "confirmed", ApproverLogin: "ana", PR: 7}}
	sealed := func(ciRun string, recs []ConfirmRecord) *stamp.Stamp {
		st, err := stamp.Seal(stamp.Stamp{Commit: "h1", CIRun: ciRun, Platform: "github"}, recs)
		if err != nil {
			t.Fatal(err)
		}
		return &st
	}
	write := func(dir string, st *stamp.Stamp, recs []ConfirmRecord) {
		t.Helper()
		body, err := json.MarshalIndent(map[string]interface{}{"contract": confirmSchema, "commit": "h1", "stamp": st, "records": recs}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, ".cache", "confirm.json")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	state := func(cd *ConfirmData) Signal {
		t.Helper()
		return confirmedOf(t, []schema.BRD{b}, cd).Signals.Confirmed
	}

	t.Run("hand-written, no stamp", func(t *testing.T) {
		dir := t.TempDir()
		write(dir, nil, approved)
		cd := LoadConfirm(dir)
		if cd == nil || cd.SetAside != stamp.CodeUnstamped {
			t.Fatalf("want %s, got %+v", stamp.CodeUnstamped, cd)
		}
		if s := state(cd); s.Value != ConfirmedUnknown || !strings.Contains(s.Detail, stamp.CodeUnstamped) {
			t.Errorf("an unstamped file must read unknown: %+v", s)
		}
	})
	t.Run("stamped, one verdict edited", func(t *testing.T) {
		dir := t.TempDir()
		write(dir, sealed("4812", prepared), approved) // the digest is of the pending verdict
		cd := LoadConfirm(dir)
		if cd == nil || cd.SetAside != stamp.CodeTampered {
			t.Fatalf("want %s, got %+v", stamp.CodeTampered, cd)
		}
		if s := state(cd); s.Value != ConfirmedUnknown {
			t.Errorf("an edited file must read unknown: %+v", s)
		}
	})
	t.Run("stamped, ci_run edited", func(t *testing.T) {
		dir := t.TempDir()
		st := sealed(stamp.LocalRun, approved)
		st.CIRun = "4812"
		write(dir, st, approved)
		if cd := LoadConfirm(dir); cd == nil || cd.SetAside != stamp.CodeTampered {
			t.Fatalf("a local stamp relabelled as CI must read tampered: %+v", cd)
		}
	})
	t.Run("valid CI file", func(t *testing.T) {
		dir := t.TempDir()
		write(dir, sealed("4812", approved), approved)
		cd := LoadConfirm(dir)
		if cd == nil || cd.SetAside != "" || cd.Local() {
			t.Fatalf("an intact CI file is read: %+v", cd)
		}
		if s := state(cd); s.Value != ConfirmedHuman || strings.Contains(s.Detail, "local") {
			t.Errorf("an intact CI file confirms: %+v", s)
		}
		if s := state(cd.ForCI("h1")); s.Value != ConfirmedHuman {
			t.Errorf("under --ci, a CI file at head confirms: %+v", s)
		}
		if s := state(cd.ForCI("h2")); s.Value != ConfirmedUnknown || !strings.Contains(s.Detail, stamp.CodeNotCI) {
			t.Errorf("under --ci, a file of another commit reads unknown: %+v", s)
		}
	})
	t.Run("local file", func(t *testing.T) {
		dir := t.TempDir()
		write(dir, sealed(stamp.LocalRun, approved), approved)
		cd := LoadConfirm(dir)
		if s := state(cd); s.Value != ConfirmedHuman || !strings.Contains(s.Detail, "human (local, not CI)") {
			t.Errorf("a local file shows, marked local: %+v", s)
		}
		ci := cd.ForCI("h1")
		if s := state(ci); s.Value != ConfirmedUnknown || ci.SetAside != stamp.CodeNotCI {
			t.Errorf("under --ci, a local file reads unknown: %+v", s)
		}
	})
	t.Run("tracked by git", func(t *testing.T) {
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("git not installed")
		}
		root := t.TempDir()
		dir := filepath.Join(root, "lattice")
		write(dir, sealed("4812", approved), approved)
		for _, args := range [][]string{{"init", "-q"}, {"add", "-f", "lattice/.cache/confirm.json"}} {
			cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
			cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		cd := LoadConfirm(dir)
		if cd == nil || cd.SetAside != stamp.CodeTracked {
			t.Fatalf("want %s, got %+v", stamp.CodeTracked, cd)
		}
		if s := state(cd); s.Value != ConfirmedUnknown {
			t.Errorf("a committed confirm.json must read unknown: %+v", s)
		}
	})
}
