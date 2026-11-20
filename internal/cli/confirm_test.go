package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/check"
	"github.com/salahmyn/lattice/pkg/lattice/confirm"
)

// A confirm.json from a local run confirms as human (local, not CI) in a
// local check, and reads unknown under --ci (EVIDENCE_NOT_CI); so does a CI
// file of another commit. A hand-written file never confirms.
// @verifies confirm:INV-6
func TestCheckCIReadsOnlyCIConfirmFile(t *testing.T) {
	clearCIEnv(t)
	f := newCheckFixture(t)
	f.git("checkout", "-q", "-b", "edit")
	f.write("shop/shop.go", strings.Replace(shopGo(true), "n > 0", "n >= 1", 1))
	f.commit("edit")
	head := f.git("rev-parse", "HEAD")
	latticeDir := filepath.Join(f.dir, "lattice")

	save := func(commit, ciRun string) {
		t.Helper()
		cf := confirm.File{Contract: confirm.Schema, Commit: commit, Records: []confirm.Record{
			{ID: "brd.shop", Version: 1, Action: confirm.ActionApprove, State: confirm.StateConfirmed, ApproverLogin: "ana", PR: 3},
		}}
		if err := confirm.SaveCache(latticeDir, confirm.Seal(cf, confirm.Stamp{CIRun: ciRun, Platform: "github"})); err != nil {
			t.Fatal(err)
		}
	}
	confirmed := func(args ...string) check.Assurance {
		t.Helper()
		rep, _ := f.check(append([]string{"--base", "main"}, args...)...)
		for _, r := range rep.Promises {
			if r.Promise == "shop:INV-1" {
				return r.Signals.Confirmed
			}
		}
		t.Fatalf("shop:INV-1 is not on the page: %+v", rep.Promises)
		return check.Assurance{}
	}

	save(head, "local")
	if c := confirmed(); c.State != check.Pass || !strings.Contains(c.Text, "human (local, not CI)") {
		t.Errorf("a local confirm file is shown, marked local: %+v", c)
	}
	if c := confirmed("--ci"); c.State != check.Unknown || !strings.Contains(c.Text, "EVIDENCE_NOT_CI") {
		t.Errorf("under --ci a local confirm file reads unknown: %+v", c)
	}

	save(head, "4812")
	if c := confirmed("--ci"); c.State != check.Pass {
		t.Errorf("under --ci a CI confirm file at head confirms: %+v", c)
	}
	save("0123456789abcdef", "4812")
	if c := confirmed("--ci"); c.State != check.Unknown || !strings.Contains(c.Text, "EVIDENCE_NOT_CI") {
		t.Errorf("under --ci a confirm file of another commit reads unknown: %+v", c)
	}

	f.write("lattice/.cache/confirm.json", `{"contract":"lattice.confirm/1","commit":"`+head+`","records":[{"id":"brd.shop","version":1,"action":"brd.approve","state":"confirmed","approver_login":"ana","pr":3}]}`)
	if c := confirmed(); c.State == check.Pass || !strings.Contains(c.Text, "CONFIRM_UNSTAMPED") {
		t.Errorf("a hand-written confirm file never confirms: %+v", c)
	}
}

// confirm --unsigned prints, for a version approved by a local command, the
// prepare, switch and sign commands with the stem prepare then writes, and
// for a decision confirmed by a review, the sign command for its file. It
// writes nothing: no signature, no branch, no change to the tree.
// @verifies confirm:INV-19
func TestConfirmUnsignedCommands(t *testing.T) {
	clearCIEnv(t)
	f := newCheckFixture(t)
	f.write("lattice/brds/brd.shop.yaml", "id: brd.shop\nversion: 1\nstatus: approved\ntitle: Shop\nbusiness_problem: Sell books.\n"+
		"success_criteria:\n  - id: SC-1\n    statement: A price is positive.\n    maps_to_invariant: shop:INV-1\nimplements_via: [shop]\n"+
		"approval:\n  approved_by: ana@example.com\n  approved_at: \"2026-01-01T00:00:00Z\"\n  approved_version: 1\n  local: true\n")
	reviewed := confirm.Decision{Action: confirm.ActionPromiseConfirm, ID: "brd.shop", Version: 1, PacketHash: "sha256:77aa88bb99",
		Rows: []confirm.Row{{Promise: "shop:INV-1"}}, PreparedBy: "Bo <bo@example.com>", PreparedAt: "2026-01-01T00:00:00Z"}
	body, err := reviewed.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	reviewedPath := "lattice/" + reviewed.RelPath()
	f.write(reviewedPath, string(body))
	f.commit("a local approval, and a promise confirmed by review")
	added := f.git("rev-parse", "HEAD")
	latticeDir := filepath.Join(f.dir, "lattice")
	cf := confirm.File{Contract: confirm.Schema, Commit: added, Branch: "main", Records: []confirm.Record{{
		ID: "brd.shop", Version: 1, Action: confirm.ActionPromiseConfirm, State: confirm.StateConfirmed, Via: confirm.ViaReview,
		ApproverLogin: "bo", PR: 4, Rows: []string{"shop:INV-1"}, DecisionSHA: added,
	}}}
	if err := confirm.SaveCache(latticeDir, confirm.Seal(cf, confirm.Stamp{CIRun: "4812", Platform: "github"})); err != nil {
		t.Fatal(err)
	}
	branches := f.git("branch", "--list")

	out, err := run(t, f.dir, newConfirmCommand, "--unsigned")
	if err != nil {
		t.Fatalf("confirm --unsigned: %v\n%s", err, out)
	}
	var l confirm.UnsignedList
	if err := json.Unmarshal([]byte(out), &l); err != nil || l.Contract != confirm.UnsignedSchema {
		t.Fatalf("not %s: %v\n%s", confirm.UnsignedSchema, err, out)
	}
	if len(l.Decisions) != 2 {
		t.Fatalf("want the local approval and the review: %+v", l.Decisions)
	}
	var local, review confirm.Unsigned
	for _, u := range l.Decisions {
		if u.Rests == confirm.RestsLocal {
			local = u
		} else {
			review = u
		}
	}
	if review.Action != confirm.ActionPromiseConfirm || len(review.Commands) != 1 || review.Commands[0] != "lattice sign "+reviewedPath {
		t.Errorf("the review: %+v", review)
	}
	if st := f.git("status", "--porcelain"); st != "" || f.git("branch", "--list") != branches {
		t.Errorf("--unsigned must write nothing:\n%s", st)
	}

	prep, err := run(t, f.dir, newPrepareCommand, "brd", "approve", "brd.shop", "--version", "1")
	if err != nil {
		t.Fatalf("prepare: %v\n%s", err, prep)
	}
	var res struct {
		Branch string `json:"branch"`
	}
	if err := json.Unmarshal([]byte(prep), &res); err != nil {
		t.Fatal(err)
	}
	stem := strings.TrimPrefix(res.Branch, "decision/")
	wantCmds := []string{"lattice prepare brd approve brd.shop --version 1", "git switch " + res.Branch, "lattice sign " + stem}
	if local.Action != confirm.ActionApprove || strings.Join(local.Commands, "\n") != strings.Join(wantCmds, "\n") {
		t.Errorf("the local approval: %q, want %q", local.Commands, wantCmds)
	}
}
