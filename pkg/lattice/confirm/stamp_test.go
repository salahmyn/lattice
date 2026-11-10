package confirm

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/confirm/stamp"
)

// The file `lattice confirm` writes carries its stamp and loads intact; a
// hand-written file with no stamp, or a stamped file with one verdict edited,
// loads set aside with every verdict unknown, so validate's relief and the
// web API never read it as confirmed.
// @verifies confirm:INV-6
func TestCacheStampIsChecked(t *testing.T) {
	d := decision(ActionApprove)
	plat := &fake{pr: merged("dev", "h2"), reviews: []Review{approval("ana", "h2")}}
	f := Compute(context.Background(), Input{Commit: "h", Found: foundOne(d), Platform: plat, Now: fixedNow,
		CIRun: "4812", PlatformName: "github", Repository: "o/r"})
	if f.Stamp == nil || f.Stamp.CIRun != "4812" || f.Stamp.Commit != "h" || f.Stamp.Platform != "github" || !strings.HasPrefix(f.Stamp.Digest, "sha256:") {
		t.Fatalf("stamp = %+v", f.Stamp)
	}

	dir := t.TempDir()
	if err := SaveCache(dir, f); err != nil {
		t.Fatal(err)
	}
	got := LoadCache(dir)
	if got == nil || got.SetAside() != "" || got.Records[0].State != StateConfirmed {
		t.Fatalf("an intact file loads as written: %+v", got)
	}

	raw, err := os.ReadFile(CachePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw), `"approver_login": "ana"`, `"approver_login": "mallory"`, 1)
	if edited == string(raw) {
		t.Fatal("fixture: nothing to edit")
	}
	if err := os.WriteFile(CachePath(dir), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	got = LoadCache(dir)
	if got == nil || got.SetAside() != stamp.CodeTampered || got.Records[0].State != StateUnknown || got.Assurance.State != StateUnknown {
		t.Errorf("an edited verdict must read unknown (%s): %+v", stamp.CodeTampered, got)
	}
	if off := Offline(got, "h"); off.Assurance.Code != stamp.CodeTampered {
		t.Errorf("offline keeps the reason the cache was set aside: %+v", off.Assurance)
	}

	hand := `{"contract":"lattice.confirm/1","commit":"h","records":[{"id":"brd.x","version":2,"action":"brd.approve","state":"confirmed","approver_login":"ana","pr":12}]}`
	if err := os.WriteFile(CachePath(dir), []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	got = LoadCache(dir)
	if got == nil || got.SetAside() != stamp.CodeUnstamped || got.Records[0].State != StateUnknown {
		t.Errorf("a hand-written file must read unknown (%s): %+v", stamp.CodeUnstamped, got)
	}
}

// A previous cache stands in for the platform only while its stamp is
// intact, and a CI run never takes a verdict from a local file: a
// hand-written cache cannot come back out of `lattice confirm` stamped.
// @verifies confirm:INV-6
func TestCacheIsNotLaundered(t *testing.T) {
	d := decision(ActionApprove)
	pending := &fake{pr: merged("dev", "h2")} // the platform shows no approval
	forged := File{Contract: Schema, Commit: "h", Records: []Record{{ID: d.ID, Version: d.Version, Action: d.Action,
		State: StateConfirmed, ApproverLogin: "ana", PR: 12, DecisionSHA: "c0ffee"}}}

	for name, cache := range map[string]File{
		"unstamped": SetAside(forged, stamp.CodeUnstamped, "no stamp"),
		"local":     Seal(forged, Stamp{CIRun: stamp.LocalRun}),
	} {
		c := cache
		f := Compute(context.Background(), Input{Commit: "h", Found: foundOne(d), Platform: pending, Cache: &c, CIRun: "4812", Now: fixedNow})
		if f.Records[0].State == StateConfirmed {
			t.Errorf("%s cache was reused by a CI run: %+v", name, f.Records[0])
		}
	}
	ci := Seal(forged, Stamp{CIRun: "4811"})
	f := Compute(context.Background(), Input{Commit: "h", Found: foundOne(d), Platform: pending, Cache: &ci, CIRun: "4812", Now: fixedNow})
	if f.Records[0].State != StateConfirmed {
		t.Errorf("an intact CI cache is reused: %+v", f.Records[0])
	}
}
