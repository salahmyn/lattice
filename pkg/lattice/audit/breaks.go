package audit

// @module-feature audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// BreaksSchema is the version tag of lattice/.cache/results/breaks.json, the
// file `lattice break` writes and the audit reads. The audit reads it as a
// file and never imports the package that writes it.
const BreaksSchema = "lattice.breaks/1"

// breaksFile is the cache path, relative to the lattice/ directory.
const breaksFile = ".cache/results/breaks.json"

// BreaksFile is breaks.json.
type BreaksFile struct {
	Schema      string     `json:"schema"`
	Commit      string     `json:"commit,omitempty"`
	Dirty       bool       `json:"dirty"`
	GeneratedAt string     `json:"generated_at,omitempty"`
	Runs        []BreakRun `json:"runs"`
}

// BreakRun is one break of one enforcer against its promise's verifiers.
type BreakRun struct {
	Promise      string   `json:"promise"`
	Enforcer     string   `json:"enforcer"`
	EnforcerFile string   `json:"enforcer_file,omitempty"`
	Verifiers    []string `json:"verifiers,omitempty"`
	Method       string   `json:"method,omitempty"` // early_return | scripted
	Script       string   `json:"script,omitempty"`
	Baseline     string   `json:"baseline,omitempty"` // pass | fail | error
	// Outcome is killed, survived or unknown; anything else reads unknown.
	Outcome    string `json:"outcome"`
	Reason     string `json:"reason,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

// LoadBreaks reads breaks.json; ok is false when there is none. A file that
// cannot be parsed is reported as an error, not silently dropped.
func LoadBreaks(latticeDir string) (*BreaksFile, bool, error) {
	data, err := os.ReadFile(filepath.Join(latticeDir, breaksFile))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var bf BreaksFile
	if err := json.Unmarshal(data, &bf); err != nil {
		return nil, true, fmt.Errorf("breaks.json: %w", err)
	}
	return &bf, true, nil
}

// usableBreaks groups the runs by promise, or returns none with the reason
// the file was set aside: a schema it does not know, or a commit that is not
// the graph's head (a break result of another commit says nothing about this
// one, and a stale "killed" must not read as a pass).
// @enforces audit:INV-4
func usableBreaks(bf *BreaksFile, head string) (map[string][]BreakRun, BreaksInput) {
	if bf == nil {
		return nil, BreaksInput{}
	}
	in := BreaksInput{Present: true, Commit: bf.Commit, Dirty: bf.Dirty, GeneratedAt: bf.GeneratedAt, Runs: len(bf.Runs)}
	switch {
	case bf.Schema != BreaksSchema:
		in.Ignored = fmt.Sprintf("breaks.json schema %q is not %s", bf.Schema, BreaksSchema)
		return nil, in
	case !stampMatches(bf.Commit, head):
		in.Ignored = fmt.Sprintf("breaks.json is for commit %s, the graph is at %s", short(bf.Commit), short(head))
		return nil, in
	}
	out := map[string][]BreakRun{}
	for _, r := range bf.Runs {
		out[r.Promise] = append(out[r.Promise], r)
	}
	return out, in
}

func mergeBreaksInput(stamped, used BreaksInput) BreaksInput {
	if used.Present {
		return used
	}
	return stamped
}

func short(sha string) string {
	if len(sha) > 10 {
		return sha[:10]
	}
	if sha == "" {
		return "(none)"
	}
	return sha
}

// breakSignal folds one promise's runs. A survived run on any enforcer makes
// it survived (the verifiers cannot tell that enforcer is broken); else a
// killed run makes it killed (a fact about that one break); else unknown,
// with the reasons. A run on a dirty tree is noted, not discarded.
// @enforces audit:INV-3
func breakSignal(runs []BreakRun) (Signal, []BreakRun) {
	s := Signal{Value: "unknown", Honesty: Fact}
	if len(runs) == 0 {
		s.Detail = "no break run for this promise (lattice break)"
		return s, nil
	}
	runs = append([]BreakRun(nil), runs...)
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].Enforcer < runs[j].Enforcer })

	pick := func(outcome string) (items []string, method string) {
		for _, r := range runs {
			if r.Outcome == outcome {
				items = append(items, r.Enforcer)
				if method == "" {
					method = r.Method
				}
			}
		}
		return
	}
	if items, m := pick("survived"); len(items) > 0 {
		s.Value, s.Items, s.Method = "survived", items, m
		return s, runs
	}
	if items, m := pick("killed"); len(items) > 0 {
		s.Value, s.Items, s.Method = "killed", items, m
		if len(items) < len(runs) {
			s.Detail = fmt.Sprintf("%d of %d enforcers broken; the rest read unknown", len(items), len(runs))
		}
		return s, runs
	}
	// Unknown: say why, from the runs' own reasons.
	seen := map[string]bool{}
	for _, r := range runs {
		why := r.Reason
		if why == "" {
			why = "outcome " + r.Outcome
		}
		if !seen[why] {
			seen[why] = true
			if s.Detail != "" {
				s.Detail += "; "
			}
			s.Detail += why
		}
	}
	s.Method = runs[0].Method
	return s, runs
}
