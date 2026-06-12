// Package flags is the open-meaning-flag store (v0.8.1). A flag marks
// a unit (criterion, scenario, invariant) whose meaning is in question:
// a suspected criterion↔invariant mismatch, or a demotion from an
// approved change request. Flags ride ALONGSIDE the computed RTM
// status — a demonstrated-but-flagged criterion reports both; the flag
// is never hidden behind the green.
//
// Anyone (human or agent) may raise a flag; only a human clears one.
// Storage is lattice/.flags/flags.json — git-tracked and regenerable
// from the ledger, like leases and results.
package flags

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Flag is one open (or cleared) meaning flag on a unit.
type Flag struct {
	Unit   string `json:"unit"`   // e.g. brd.checkout.refund/SC-1
	Reason string `json:"reason"` // one line: why the meaning is in question
	By     string `json:"by"`     // who raised it (actor or human)
	At     string `json:"at"`     // ISO datetime raised
	// Source ties the flag to its origin: "manual", "cr:CR-3" (a
	// demotion), or a rule code like CRITERION_INVARIANT_NARROWER.
	Source string `json:"source,omitempty"`

	// ClearedBy/ClearedAt close the flag. Clearing is a human act — the
	// CLI refuses agent actors here.
	ClearedBy string `json:"cleared_by,omitempty"`
	ClearedAt string `json:"cleared_at,omitempty"`
}

// Open reports whether the flag is still open.
func (f Flag) Open() bool { return f.ClearedBy == "" }

const (
	dir  = ".flags"
	file = "flags.json"
)

func path(latticeDir string) string { return filepath.Join(latticeDir, dir, file) }

// Load reads every flag (open and cleared). A missing store is empty,
// not an error.
func Load(latticeDir string) []Flag {
	data, err := os.ReadFile(path(latticeDir))
	if err != nil {
		return nil
	}
	var all []Flag
	if json.Unmarshal(data, &all) != nil {
		return nil
	}
	return all
}

// OpenByUnit returns the open flags keyed by unit.
func OpenByUnit(latticeDir string) map[string][]Flag {
	out := map[string][]Flag{}
	for _, f := range Load(latticeDir) {
		if f.Open() {
			out[f.Unit] = append(out[f.Unit], f)
		}
	}
	return out
}

// Raise opens a flag on unit. Raising the same (unit, reason) twice is
// a no-op so rule-driven raises stay idempotent.
func Raise(latticeDir, unit, reason, by, source string, now time.Time) (Flag, error) {
	if strings.TrimSpace(unit) == "" || strings.TrimSpace(reason) == "" {
		return Flag{}, fmt.Errorf("a flag needs both a unit and a one-line reason")
	}
	all := Load(latticeDir)
	for _, f := range all {
		if f.Open() && f.Unit == unit && f.Reason == reason {
			return f, nil
		}
	}
	nf := Flag{
		Unit: unit, Reason: reason, By: by,
		At: now.UTC().Format(time.RFC3339), Source: source,
	}
	all = append(all, nf)
	return nf, save(latticeDir, all)
}

// Clear closes every open flag on unit. Clearing is the human meaning
// gate — callers must pass the human identity in by; the CLI enforces
// that it is non-empty and not an agent actor.
func Clear(latticeDir, unit, by string, now time.Time) (int, error) {
	if strings.TrimSpace(by) == "" {
		return 0, fmt.Errorf("clearing a flag requires --by <human> — only a human clears meaning flags")
	}
	all := Load(latticeDir)
	n := 0
	for i := range all {
		if all[i].Open() && all[i].Unit == unit {
			all[i].ClearedBy = by
			all[i].ClearedAt = now.UTC().Format(time.RFC3339)
			n++
		}
	}
	if n == 0 {
		return 0, fmt.Errorf("no open flags on %s", unit)
	}
	return n, save(latticeDir, all)
}

func save(latticeDir string, all []Flag) error {
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Unit != all[j].Unit {
			return all[i].Unit < all[j].Unit
		}
		return all[i].At < all[j].At
	})
	p := path(latticeDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}
