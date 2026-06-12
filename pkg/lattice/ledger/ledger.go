// Package ledger implements the v0.8 §6 attribution ledger: an
// append-only, in-graph record of every truth-level transition, attributed
// to an agent identity and stamped with the autonomy mode it was made
// under.
//
// The ledger is the soundability spine of an autonomous fleet — it answers
// "which agent moved this claim, on what evidence, and under which mode?"
// Credit is earned by *movement* (a unit advancing up the ladder), not by
// activity, so narrowing a BRD to silence a rule leaves a visible non-entry
// or a →regressed entry rather than a green checkmark.
//
// It is derived state: `lattice ledger rebuild` regenerates a snapshot by
// replaying the current graph, so the ledger can never silently disagree
// with the code.
package ledger

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// file is the ledger location relative to the lattice/ directory.
const file = ".ledger/ledger.jsonl"

// Event kinds (v0.8.1). The ledger is the SINGLE event stream — every
// kind of lifecycle event lands here rather than in parallel logs; any
// per-feature or per-gate view is derived from it.
const (
	EventTransition = "transition" // a unit moved on the truth-level ladder
	EventCheckRun   = "check-run"  // a check suite executed (validate, runs-clean, demonstrate)
	EventGate       = "gate"       // a human gate decision (approve, clear)
	EventCR         = "cr"         // a change-request lifecycle event
	EventFlag       = "flag"       // a meaning flag raised or cleared
	EventSignOff    = "sign-off"   // a demonstration sign-off
)

// Entry is one recorded event. For EventTransition, Transition carries
// the "<from>→<to>" ladder move; other kinds use Evidence for their
// payload and may leave Transition empty.
type Entry struct {
	// At is an RFC3339 timestamp; Commit pins the commit the event
	// was observed on (the graph's generated_from_commit).
	At     string `json:"at,omitempty"`
	Commit string `json:"commit,omitempty"`
	// Event is the kind of entry (see Event* constants). Empty means
	// EventTransition — the pre-v0.8.1 wire format remains valid.
	Event string `json:"event,omitempty"`
	// Actor is the identity responsible for the event.
	Actor string `json:"actor,omitempty"`
	// Unit is the truth-bearing unit: "brd.x:SC-1", "brd.x:US-1", a
	// "feature:INV-N" invariant ref, or "workspace" for suite-level events.
	Unit string `json:"unit"`
	// Transition is "<from>→<to>" across the truth-level ladder, e.g.
	// "unverified→demonstrated" or "verified→regressed".
	Transition string `json:"transition,omitempty"`
	// Evidence references what backs the event: a test FQN, a
	// validation code, an entry-point id, or a one-line summary.
	Evidence string `json:"evidence,omitempty"`
	// Mode is the autonomy mode the event was made under.
	Mode string `json:"mode,omitempty"`
}

// Kind returns the event kind, defaulting historical entries to
// EventTransition.
func (e Entry) Kind() string {
	if e.Event == "" {
		return EventTransition
	}
	return e.Event
}

// From returns the pre-transition level (the part before "→"). Empty when
// the transition string has no arrow.
func (e Entry) From() string {
	if i := strings.Index(e.Transition, "→"); i >= 0 {
		return e.Transition[:i]
	}
	return ""
}

// To returns the post-transition level (the part after "→").
func (e Entry) To() string {
	if i := strings.Index(e.Transition, "→"); i >= 0 {
		return e.Transition[i+len("→"):]
	}
	return e.Transition
}

// Record appends one entry to the ledger, creating the directory on first
// write. The ledger is append-only — entries are never rewritten.
func Record(latticeDir string, e Entry) error {
	path := filepath.Join(latticeDir, file)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

// RecordAll appends many entries in one open. Used by rebuild.
func RecordAll(latticeDir string, entries []Entry) error {
	for _, e := range entries {
		if err := Record(latticeDir, e); err != nil {
			return err
		}
	}
	return nil
}

// Load reads every ledger entry in append order. A missing ledger is not
// an error — it returns an empty slice.
func Load(latticeDir string) ([]Entry, error) {
	path := filepath.Join(latticeDir, file)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Entry
		if json.Unmarshal([]byte(line), &e) == nil && e.Unit != "" {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// Truncate clears the ledger — used by `rebuild` before writing a fresh
// snapshot. Absent ledger is a no-op.
func Truncate(latticeDir string) error {
	path := filepath.Join(latticeDir, file)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ByUnit returns entries for one unit, in append order.
func ByUnit(entries []Entry, unit string) []Entry {
	var out []Entry
	for _, e := range entries {
		if e.Unit == unit {
			out = append(out, e)
		}
	}
	return out
}

// ByActor returns entries attributed to one actor.
func ByActor(entries []Entry, actor string) []Entry {
	var out []Entry
	for _, e := range entries {
		if e.Actor == actor {
			out = append(out, e)
		}
	}
	return out
}
