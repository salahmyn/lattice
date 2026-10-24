package ledger

import "testing"

func TestRecordLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	entries := []Entry{
		{At: "2026-06-07T12:00:00Z", Actor: "agent:A", Unit: "brd.x:US-1", Transition: "unverified→demonstrated", Evidence: "test.journey.us1", Mode: "gated"},
		{At: "2026-06-07T12:05:00Z", Actor: "agent:B", Unit: "brd.x:SC-2", Transition: "verified→regressed", Evidence: "CRITERION_INVARIANT_NARROWER", Mode: "gated"},
	}
	if err := RecordAll(dir, entries); err != nil {
		t.Fatalf("record: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].To() != "demonstrated" || got[0].From() != "unverified" {
		t.Errorf("transition parse: from=%q to=%q", got[0].From(), got[0].To())
	}
	if u := ByUnit(got, "brd.x:SC-2"); len(u) != 1 || u[0].Actor != "agent:B" {
		t.Errorf("ByUnit = %+v", u)
	}
	if a := ByActor(got, "agent:A"); len(a) != 1 {
		t.Errorf("ByActor = %+v", a)
	}
}

func TestLoadMissingIsEmpty(t *testing.T) {
	got, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %+v", got)
	}
}

func TestTruncate(t *testing.T) {
	dir := t.TempDir()
	_ = Record(dir, Entry{Unit: "a", Transition: "x→y"})
	if err := Truncate(dir); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	got, _ := Load(dir)
	if len(got) != 0 {
		t.Errorf("expected empty after truncate, got %+v", got)
	}
	// Truncate on already-absent ledger is a no-op.
	if err := Truncate(dir); err != nil {
		t.Errorf("truncate absent: %v", err)
	}
}

// Re-running a check with the same outcome adds nothing; a changed outcome,
// another actor or another unit is recorded.
func TestRecordChange(t *testing.T) {
	dir := t.TempDir()
	run := func(actor, unit, evidence string) bool {
		t.Helper()
		ok, err := RecordChange(dir, Entry{Event: EventCheckRun, Actor: actor, Unit: unit, Evidence: evidence})
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	steps := []struct {
		actor, unit, evidence string
		want                  bool
	}{
		{"ci", "workspace", "validate: 0 error(s)", true},
		{"ci", "workspace", "validate: 0 error(s)", false}, // same outcome
		{"ci", "workspace", "validate: 2 error(s)", true},  // outcome changed
		{"ci", "workspace", "validate: 0 error(s)", true},  // and back
		{"dev", "workspace", "validate: 0 error(s)", true}, // another actor
		{"dev", "brd.x:SC-1", "validate: 0 error(s)", true},
	}
	for i, s := range steps {
		if got := run(s.actor, s.unit, s.evidence); got != s.want {
			t.Errorf("step %d: appended=%v, want %v", i, got, s.want)
		}
	}
	entries, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Errorf("ledger has %d entries, want 5", len(entries))
	}
}
