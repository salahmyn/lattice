package breaktest

// @module-feature breaktest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// ErrLocked is returned when another `lattice break` holds the run lock.
var ErrLocked = errors.New("another lattice break is running")

// lockInfo is the content of break.lock: who holds it and since when.
type lockInfo struct {
	PID     int    `json:"pid"`
	Started string `json:"started"`
}

// lockedError says which process holds the lock. It matches ErrLocked.
type lockedError struct{ holder lockInfo }

func (e *lockedError) Error() string {
	who := "pid unknown"
	if e.holder.PID > 0 {
		who = fmt.Sprintf("pid %d, started %s", e.holder.PID, e.holder.Started)
	}
	return fmt.Sprintf("%v (%s); wait for it or remove lattice/.cache/results/break.lock if it is gone", ErrLocked, who)
}

func (e *lockedError) Is(target error) bool { return target == ErrLocked }

// LockPath is the file that keeps two break runs of one repository apart.
func LockPath(latticeDir string) string {
	return filepath.Join(latticeDir, ".cache", "results", "break.lock")
}

// acquireLock takes the exclusive break lock of a repository. The lock is a
// file created with O_EXCL that holds the pid and start time of its owner; a
// second run fails fast with an error naming the holder, because runners that
// share a resource (a database) corrupt each other's results. A lock whose pid
// no longer exists on this machine is reclaimed, with a note on warn. The
// returned function releases the lock and is safe to call more than once.
// @enforces breaktest:INV-14
func acquireLock(latticeDir string, warn io.Writer) (release func(), err error) {
	path := LockPath(latticeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	mine := lockInfo{PID: os.Getpid(), Started: time.Now().UTC().Format(time.RFC3339)}
	for attempt := 0; attempt < 3; attempt++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			werr := json.NewEncoder(f).Encode(mine)
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
			if werr != nil {
				os.Remove(path)
				return nil, werr
			}
			return func() { releaseLock(path, mine) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		raw, rerr := os.ReadFile(path)
		if errors.Is(rerr, os.ErrNotExist) {
			continue // released between our create and read
		}
		if rerr != nil {
			return nil, rerr
		}
		var held lockInfo
		_ = json.Unmarshal(raw, &held) // an unreadable lock reads as pid unknown, and held
		if held.PID <= 0 || processAlive(held.PID) {
			return nil, &lockedError{holder: held}
		}
		// Reclaim a stale lock, unless it changed since it was read.
		if now, _ := os.ReadFile(path); string(now) == string(raw) {
			if warn != nil {
				fmt.Fprintf(warn, "lattice break: removing a stale lock (pid %d, started %s; the process is gone)\n", held.PID, held.Started)
			}
			if rerr := os.Remove(path); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
				return nil, rerr
			}
		}
	}
	return nil, fmt.Errorf("could not take %s: it keeps changing under us", path)
}

// releaseLock removes the lock file if it is still this run's.
func releaseLock(path string, mine lockInfo) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var held lockInfo
	if json.Unmarshal(raw, &held) == nil && held == mine {
		os.Remove(path)
	}
}
