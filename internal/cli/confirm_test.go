package cli

import (
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
