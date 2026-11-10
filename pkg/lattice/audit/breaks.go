package audit

// @module-feature audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	// RanVerifiers, NotRunVerifiers and FailedTests say what the run actually
	// executed. A file written before they existed has none of them, and its
	// runs read as if every verifier ran.
	RanVerifiers    []string `json:"ran_verifiers,omitempty"`
	NotRunVerifiers []string `json:"not_run_verifiers,omitempty"`
	FailedTests     []string `json:"failed_tests,omitempty"`
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

// breakSignal folds one promise's runs, one outcome per enforcer. A run that
// survived with every verifier run, on any enforcer, makes it survived (the
// verifiers cannot tell that enforcer is broken); else a killed run makes it
// killed (a fact about that one break); else a run that survived while some
// verifier did not run makes it partial_run (the break says nothing about the
// verifiers that could not run); else unknown, with the reasons. A run on a
// dirty tree is noted, not discarded.
// @enforces audit:INV-3
func breakSignal(runs []BreakRun) (Signal, []BreakRun) {
	s := Signal{Value: "unknown", Honesty: Fact}
	if len(runs) == 0 {
		s.Detail = "no break run for this promise (lattice break)"
		return s, nil
	}
	runs = append([]BreakRun(nil), runs...)
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].Enforcer < runs[j].Enforcer })

	var full, killed, partial []BreakRun
	for _, r := range runs {
		switch {
		case r.Outcome == "survived" && len(r.NotRunVerifiers) == 0:
			full = append(full, r)
		case r.Outcome == "survived":
			partial = append(partial, r)
		case r.Outcome == "killed":
			killed = append(killed, r)
		}
	}
	enforcers := func(rs []BreakRun) (items []string) {
		for _, r := range rs {
			items = append(items, r.Enforcer)
		}
		return
	}
	if len(full) > 0 {
		s.Value, s.Items, s.Method = "survived", enforcers(full), full[0].Method
		return s, runs
	}
	if len(killed) > 0 {
		s.Value, s.Items, s.Method = "killed", enforcers(killed), killed[0].Method
		if len(killed) < len(runs) {
			s.Detail = fmt.Sprintf("%d of %d enforcers broken; the rest: %s", len(killed), len(runs), perEnforcer(runs, "killed"))
		}
		return s, runs
	}
	if len(partial) > 0 {
		s.Value, s.Items, s.Method = "partial_run", enforcers(partial), partial[0].Method
		s.Detail = "survived, but verifiers did not run: " + strings.Join(notRun(partial), ", ")
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

// notRun lists the verifiers the runs left unrun, sorted, once each.
func notRun(runs []BreakRun) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range runs {
		for _, v := range r.NotRunVerifiers {
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	sort.Strings(out)
	return out
}

// perEnforcer is the outcome of each enforcer whose outcome is not skip, in
// one line.
func perEnforcer(runs []BreakRun, skip string) string {
	var parts []string
	for _, r := range runs {
		if r.Outcome == skip {
			continue
		}
		p := r.Enforcer + ": " + r.Outcome
		if r.Outcome == "survived" && len(r.NotRunVerifiers) > 0 {
			p += " (verifiers not run: " + strings.Join(r.NotRunVerifiers, ", ") + ")"
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, "; ")
}

// Per-enforcer outcomes: the audit's reading of the break runs of one
// enforcer. survived needs every verifier run; a break that survived while a
// verifier could not run reads unknown, as the row does.
const (
	OutcomeKilled   = "killed"
	OutcomeSurvived = "survived"
	OutcomeUnknown  = "unknown"
	OutcomeNotRun   = "not_run" // no break run of this enforcer
)

// EnforcerOutcome is one enforcer of a promise and what breaking it did.
type EnforcerOutcome struct {
	Symbol  string `json:"symbol"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Outcome string `json:"outcome"` // killed | survived | unknown | not_run
	Reason  string `json:"reason,omitempty"`
}

// enforcerOutcomes reads one outcome per enforcer from the promise's break
// runs, in the order breakSignal folds them: a full survival wins over a
// kill, a kill over a partial survival, and that over unknown. An enforcer
// with no run reads not_run; a run of a symbol the graph does not list as an
// enforcer is kept, after the graph's, so no run goes unshown.
// @enforces audit:INV-6
func enforcerOutcomes(enf []EnforcerEvidence, runs []BreakRun) []EnforcerOutcome {
	byEnf := map[string][]BreakRun{}
	for _, r := range runs {
		byEnf[r.Enforcer] = append(byEnf[r.Enforcer], r)
	}
	out := make([]EnforcerOutcome, 0, len(enf))
	listed := map[string]bool{}
	for _, e := range enf {
		listed[e.FQN] = true
		o := readEnforcer(byEnf[e.FQN])
		o.Symbol, o.File, o.Line = e.FQN, e.File, e.Line
		out = append(out, o)
	}
	var extra []string
	for name := range byEnf {
		if !listed[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	for _, name := range extra {
		o := readEnforcer(byEnf[name])
		o.Symbol, o.File = name, byEnf[name][0].EnforcerFile
		out = append(out, o)
	}
	return out
}

// readEnforcer folds the runs of one enforcer into its outcome and reason.
func readEnforcer(runs []BreakRun) EnforcerOutcome {
	if len(runs) == 0 {
		return EnforcerOutcome{Outcome: OutcomeNotRun, Reason: "no break run of this enforcer (lattice break)"}
	}
	pick := func(match func(BreakRun) bool) (BreakRun, bool) {
		for _, r := range runs {
			if match(r) {
				return r, true
			}
		}
		return BreakRun{}, false
	}
	if r, ok := pick(func(r BreakRun) bool { return r.Outcome == "survived" && len(r.NotRunVerifiers) == 0 }); ok {
		return EnforcerOutcome{Outcome: OutcomeSurvived, Reason: "the verifiers passed with it broken" + methodNote(r)}
	}
	if r, ok := pick(func(r BreakRun) bool { return r.Outcome == "killed" }); ok {
		return EnforcerOutcome{Outcome: OutcomeKilled, Reason: "breaking it" + methodNote(r) + " made the verifiers fail"}
	}
	if r, ok := pick(func(r BreakRun) bool { return r.Outcome == "survived" }); ok {
		return EnforcerOutcome{Outcome: OutcomeUnknown,
			Reason: "survived" + methodNote(r) + ", but verifiers did not run: " + strings.Join(r.NotRunVerifiers, ", ")}
	}
	r := runs[0]
	why := r.Reason
	if why == "" {
		why = "outcome " + r.Outcome
	}
	return EnforcerOutcome{Outcome: OutcomeUnknown, Reason: why}
}

func methodNote(r BreakRun) string {
	if r.Method == "" {
		return ""
	}
	return " (" + r.Method + ")"
}

// tally is the row's per-enforcer count, led by the outcome that decides the
// class and naming the symbols, as "; 1 of 3 enforcers survived: a; 2
// killed: b, c". It is empty for a promise with fewer than two enforcers,
// whose reason already names the one.
// @enforces audit:INV-6
func tally(outs []EnforcerOutcome, lead string) string {
	if len(outs) < 2 {
		return ""
	}
	by := map[string][]string{}
	for _, o := range outs {
		by[o.Outcome] = append(by[o.Outcome], o.Symbol)
	}
	label := map[string]string{
		OutcomeSurvived: "survived", OutcomeKilled: "killed",
		OutcomeUnknown: "could not be judged", OutcomeNotRun: "never broken (no break run)",
	}
	s := fmt.Sprintf("; %d of %d enforcers %s: %s", len(by[lead]), len(outs), label[lead], strings.Join(by[lead], ", "))
	for _, k := range []string{OutcomeSurvived, OutcomeKilled, OutcomeUnknown, OutcomeNotRun} {
		if k == lead || len(by[k]) == 0 {
			continue
		}
		s += fmt.Sprintf("; %d %s: %s", len(by[k]), label[k], strings.Join(by[k], ", "))
	}
	return s
}

// ShowsEnforcers reports whether the per-enforcer breakdown belongs on the
// row when it is printed: a fake, a partial run, or enforcers whose breaks
// disagree, and at least one break run behind them.
func (r Row) ShowsEnforcers() bool {
	ran, mixed := false, false
	for i, o := range r.Enforcers {
		if o.Outcome != OutcomeNotRun {
			ran = true
		}
		if i > 0 && o.Outcome != r.Enforcers[0].Outcome {
			mixed = true
		}
	}
	return ran && (r.Class == ClassFake || r.Kind == KindPartialRun || mixed)
}
