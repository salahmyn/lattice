package breaktest

// @module-feature breaktest

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A second acquire fails fast with an error that names the holder, and the
// lock is free again once the first run releases it.
// @verifies breaktest:INV-14
func TestLockExcludesSecondRun(t *testing.T) {
	dir := t.TempDir()
	release, err := acquireLock(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = acquireLock(dir, nil)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("second acquire: %v, want ErrLocked", err)
	}
	want := "another lattice break is running (pid " + itoa(os.Getpid()) + ", started "
	if !strings.HasPrefix(err.Error(), want) ||
		!strings.HasSuffix(err.Error(), "); wait for it or remove lattice/.cache/results/break.lock if it is gone") {
		t.Errorf("message = %q", err)
	}
	release()
	release2, err := acquireLock(dir, nil)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	release2()
}

// Release removes the file, and removes only its own lock.
// @verifies breaktest:INV-14
func TestLockReleaseRemovesFile(t *testing.T) {
	dir := t.TempDir()
	release, err := acquireLock(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(LockPath(dir)); err != nil {
		t.Fatalf("lock file missing while held: %v", err)
	}
	release()
	if _, err := os.Stat(LockPath(dir)); !os.IsNotExist(err) {
		t.Errorf("lock file still there after release: %v", err)
	}

	// A lock that another run took over is not removed by a late release.
	release, err = acquireLock(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := json.Marshal(lockInfo{PID: os.Getpid() + 1, Started: "2026-01-01T00:00:00Z"})
	if err := os.WriteFile(LockPath(dir), other, 0o644); err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(LockPath(dir)); err != nil {
		t.Errorf("release removed a lock it does not own: %v", err)
	}
}

// A lock whose process is gone is reclaimed, with a note.
// @verifies breaktest:INV-14
func TestLockReclaimsStale(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no liveness check on Windows: a lock is always held")
	}
	dir := t.TempDir()
	stale, _ := json.Marshal(lockInfo{PID: 2147483646, Started: "2026-01-01T00:00:00Z"})
	if err := os.MkdirAll(filepath.Dir(LockPath(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LockPath(dir), stale, 0o644); err != nil {
		t.Fatal(err)
	}
	var note bytes.Buffer
	release, err := acquireLock(dir, &note)
	if err != nil {
		t.Fatalf("stale lock not reclaimed: %v", err)
	}
	if !strings.Contains(note.String(), "stale lock (pid 2147483646") {
		t.Errorf("note = %q", note.String())
	}
	var held lockInfo
	raw, _ := os.ReadFile(LockPath(dir))
	if json.Unmarshal(raw, &held) != nil || held.PID != os.Getpid() {
		t.Errorf("lock now holds %q, want this process", raw)
	}
	release()
}

// A lock file that cannot be read is held, not reclaimed.
// @verifies breaktest:INV-14
func TestLockUnreadableIsHeld(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(LockPath(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LockPath(dir), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireLock(dir, nil); !errors.Is(err, ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
