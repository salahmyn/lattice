package lease

import (
	"testing"
	"time"
)

func TestAcquireReleaseRoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)

	l, err := Acquire(dir, "todo.add", "agent:A", "abc123", time.Hour, []string{"src/Features/AddTodo/"}, now)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if l.Actor != "agent:A" || !l.IsActive(now) {
		t.Fatalf("unexpected lease: %+v", l)
	}

	list, _ := List(dir)
	if len(list) != 1 || list[0].Unit != "todo.add" {
		t.Fatalf("List = %+v", list)
	}

	if err := Release(dir, "todo.add", "agent:A", false); err != nil {
		t.Fatalf("release: %v", err)
	}
	list, _ = List(dir)
	if len(list) != 0 {
		t.Fatalf("expected empty after release, got %+v", list)
	}
}

func TestAcquireConflict(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	if _, err := Acquire(dir, "todo.add", "agent:A", "", time.Hour, nil, now); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(dir, "todo.add", "agent:B", "", time.Hour, nil, now); err == nil {
		t.Error("expected conflict acquiring B over active A lease")
	}
	// Same actor refreshes without error.
	if _, err := Acquire(dir, "todo.add", "agent:A", "", time.Hour, nil, now); err != nil {
		t.Errorf("same-actor refresh should succeed: %v", err)
	}
}

func TestExpiry(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	if _, err := Acquire(dir, "todo.add", "agent:A", "", time.Minute, nil, now); err != nil {
		t.Fatal(err)
	}
	later := now.Add(2 * time.Minute)
	active, _ := Active(dir, later)
	if len(active) != 0 {
		t.Errorf("expected lease expired by %v, got %+v", later, active)
	}
	// A different actor may claim an expired lease.
	if _, err := Acquire(dir, "todo.add", "agent:B", "", time.Hour, nil, later); err != nil {
		t.Errorf("expected B to claim expired lease: %v", err)
	}
}

func TestOverlapsCrossActor(t *testing.T) {
	a := Lease{Unit: "todo.add", Actor: "A", Scope: []string{"src/Core/Contracts/"}}
	b := Lease{Unit: "todo.list", Actor: "B", Scope: []string{"src/Core/Contracts/TodoStore.ts"}}
	d := Lease{Unit: "todo.y", Actor: "C", Scope: []string{"src/Features/Other/"}}

	ov := Overlaps([]Lease{a, b, d})
	if len(ov) != 1 {
		t.Fatalf("expected exactly one overlap (A↔B on Contracts), got %d: %+v", len(ov), ov)
	}
	if ov[0].PathPrefix != "src/Core/Contracts" {
		t.Errorf("PathPrefix = %q", ov[0].PathPrefix)
	}
}

func TestOverlapsSameActorExcluded(t *testing.T) {
	// Two leases by the same actor on the same scope never conflict.
	a := Lease{Unit: "todo.add", Actor: "A", Scope: []string{"src/Core/Contracts/"}}
	c := Lease{Unit: "todo.x", Actor: "A", Scope: []string{"src/Core/Contracts/"}}
	d := Lease{Unit: "todo.y", Actor: "C", Scope: []string{"src/Features/Other/"}}

	if ov := Overlaps([]Lease{a, c, d}); len(ov) != 0 {
		t.Fatalf("expected no overlap (same actor, disjoint other), got %+v", ov)
	}
}
