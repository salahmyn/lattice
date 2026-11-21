package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/confirm/stamp"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// The audit reads confirm.routes as `lattice confirm` does: with routes:
// [signature] a promise whose BRD a review alone confirmed reads pending,
// with the reason, never human, even from a file written before the setting
// changed; by default it reads human; an unreadable config.yaml reads
// unknown.
// @verifies confirm:INV-22
func TestConfirmedHonoursRoutes(t *testing.T) {
	dir := t.TempDir()
	recs := []ConfirmRecord{{ID: "b", Version: 2, Action: "brd.approve", State: "confirmed", Via: ViaReview, ApproverLogin: "ana", PR: 7}}
	st, err := stamp.Seal(stamp.Stamp{Commit: "h1", CIRun: "4812", Platform: "github"}, recs)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]interface{}{"contract": confirmSchema, "commit": "h1", "stamp": st, "records": recs})
	if err != nil {
		t.Fatal(err)
	}
	write := func(rel, data string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".cache/confirm.json", string(body))
	b := brdFor("b", schema.BRDApproved, 2, &schema.BRDApproval{ApprovedBy: "ana@example.com", ApprovedVersion: 2})
	signal := func() Signal {
		t.Helper()
		return confirmedOf(t, []schema.BRD{b}, LoadConfirm(dir)).Signals.Confirmed
	}

	if s := signal(); s.Value != ConfirmedHuman {
		t.Fatalf("default routes: %+v", s)
	}
	write("config.yaml", "confirm:\n  routes: [signature]\n")
	s := signal()
	if s.Value != ConfirmedPending || !strings.Contains(s.Detail, "accepts only signature") || !strings.Contains(s.Detail, "approved by ana on PR #7") {
		t.Errorf("routes: [signature]: %+v", s)
	}
	if st, detail, who := BRDStanding(b, LoadConfirm(dir)); st != ConfirmedPending || who != "" || !strings.Contains(detail, "confirm.routes") {
		t.Errorf("the BRD's standing: %s %q %q", st, detail, who)
	}
	write("config.yaml", "confirm:\n  routes: [review]\n")
	if s := signal(); s.Value != ConfirmedHuman {
		t.Errorf("routes: [review]: %+v", s)
	}
	write("config.yaml", "confirm: [\n")
	if s := signal(); s.Value != ConfirmedUnknown || !strings.Contains(s.Detail, "config.yaml could not be read") {
		t.Errorf("an unreadable config.yaml: %+v", s)
	}
}
