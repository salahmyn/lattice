package flags

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

func TestRaiseLoadClear(t *testing.T) {
	dir := t.TempDir()
	if _, err := Raise(dir, "brd.x.y/SC-1", "invariant narrower than criterion", "agent:rev-1", "CRITERION_INVARIANT_NARROWER", t0); err != nil {
		t.Fatal(err)
	}
	// Idempotent re-raise.
	if _, err := Raise(dir, "brd.x.y/SC-1", "invariant narrower than criterion", "agent:rev-2", "manual", t0); err != nil {
		t.Fatal(err)
	}
	open := OpenByUnit(dir)
	if len(open["brd.x.y/SC-1"]) != 1 {
		t.Fatalf("expected 1 open flag, got %d", len(open["brd.x.y/SC-1"]))
	}

	n, err := Clear(dir, "brd.x.y/SC-1", "human:sal", t0.Add(time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("clear: n=%d err=%v", n, err)
	}
	if len(OpenByUnit(dir)) != 0 {
		t.Fatal("expected no open flags after clear")
	}
	// History retained.
	if len(Load(dir)) != 1 {
		t.Fatal("cleared flag must stay in history")
	}
}

func TestClearRequiresIdentityAndOpenFlag(t *testing.T) {
	dir := t.TempDir()
	if _, err := Clear(dir, "u", "", t0); err == nil {
		t.Fatal("expected error clearing without identity")
	}
	if _, err := Clear(dir, "u", "human:sal", t0); err == nil {
		t.Fatal("expected error clearing nonexistent flag")
	}
}
