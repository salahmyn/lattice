package audit

// @module-feature audit

import (
	"sort"
	"strings"
)

// Causes of an unknown break run. The cause is read from the run's own
// reason text, so a breaks.json written before this reading has them too.
const (
	CauseSetupFailed = "setup_failed" // break.setup (or its re-run) failed: nothing ran
	CauseNoRunner    = "no_runner"    // no runner is configured for the verifiers' language
	CausePartial     = "partial"      // the break left the verifiers that ran green, but some did not run
	CauseOther       = "other"        // anything else: the reason text says what
)

// setupFailedPrefix starts the reason lattice break gives every run after a
// failed break.setup, a failed re-run after a restore, or a failed baseline
// heal. The writer of breaks.json owns the wording; this package reads the
// file and does not import it.
const setupFailedPrefix = "setup failed"

// SetupFailedReason reports whether a run's reason is a failed setup, and the
// reason without the "setup failed: " lead.
func SetupFailedReason(reason string) (string, bool) {
	rest, ok := strings.CutPrefix(reason, setupFailedPrefix)
	if !ok {
		return "", false
	}
	rest = strings.TrimPrefix(rest, ":")
	return strings.TrimSpace(rest), true
}

// UnknownCause is a group of unknown runs of one feature that share a cause,
// with the reason text of the group's first run.
type UnknownCause struct {
	Cause  string   `json:"cause"`
	Reason string   `json:"reason"`
	Count  int      `json:"count"`
	Refs   []string `json:"promises,omitempty"`
}

// SurvivedRun is a break that no verifier of the promise noticed, with every
// verifier run.
type SurvivedRun struct {
	Promise  string `json:"promise"`
	Enforcer string `json:"enforcer"`
	Method   string `json:"method,omitempty"`
}

// BreakFacts is breaks.json read as facts per feature. It is read by the
// audit's own rule (usableBreaks): a file of another schema or commit is set
// aside, and then there is no evidence, which never reads as a pass.
type BreakFacts struct {
	// Evidence is true when the file belongs to the graph's commit.
	Evidence bool
	// Why says why there is no evidence: no file, or the reason it was set
	// aside. Empty when Evidence is true.
	Why   string
	Dirty bool
	// Survived and Unknown are keyed by feature id.
	Survived map[string][]SurvivedRun
	Unknown  map[string][]UnknownCause
}

// ReadBreakFacts reads the survived and unknown runs of breaks.json by
// feature. bf is nil when there is no file; head is the graph's commit.
// A run is survived when its verifiers all ran and passed with the enforcer
// broken; every other run that did not kill a break is unknown. A killed run
// is a fact about that one break and is not listed. Edge runs are not read
// (usableBreaks leaves them out).
// @enforces audit:INV-12
func ReadBreakFacts(bf *BreaksFile, head string) BreakFacts {
	runs, in := usableBreaks(bf, head)
	f := BreakFacts{Survived: map[string][]SurvivedRun{}, Unknown: map[string][]UnknownCause{}}
	switch {
	case !in.Present:
		f.Why = "no breaks.json: lattice break has not been run at this commit"
		return f
	case in.Ignored != "":
		f.Why = in.Ignored
		return f
	}
	f.Evidence, f.Dirty = true, in.Dirty

	promises := make([]string, 0, len(runs))
	for p := range runs {
		promises = append(promises, p)
	}
	sort.Strings(promises)
	idx := map[string]map[string]int{} // feature -> group key -> index in Unknown
	for _, p := range promises {
		feature, _, _ := strings.Cut(p, ":")
		rs := append([]BreakRun(nil), runs[p]...)
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].Enforcer < rs[j].Enforcer })
		for _, r := range rs {
			switch {
			case r.Outcome == "killed":
			case r.Outcome == "survived" && len(r.NotRunVerifiers) == 0:
				f.Survived[feature] = append(f.Survived[feature],
					SurvivedRun{Promise: p, Enforcer: r.Enforcer, Method: r.Method})
			default:
				cause, reason := unknownCause(r)
				key := cause + "\x00" + reason
				if cause == CauseSetupFailed || cause == CausePartial {
					key = cause // one group per cause: the reason text is the first run's
				}
				if idx[feature] == nil {
					idx[feature] = map[string]int{}
				}
				i, ok := idx[feature][key]
				if !ok {
					i = len(f.Unknown[feature])
					idx[feature][key] = i
					f.Unknown[feature] = append(f.Unknown[feature], UnknownCause{Cause: cause, Reason: reason})
				}
				g := &f.Unknown[feature][i]
				g.Count++
				if len(g.Refs) == 0 || g.Refs[len(g.Refs)-1] != p {
					g.Refs = append(g.Refs, p)
				}
			}
		}
	}
	for feature := range f.Unknown {
		g := f.Unknown[feature]
		sort.SliceStable(g, func(i, j int) bool {
			if g[i].Count != g[j].Count {
				return g[i].Count > g[j].Count
			}
			if g[i].Cause != g[j].Cause {
				return g[i].Cause < g[j].Cause
			}
			return g[i].Reason < g[j].Reason
		})
	}
	return f
}

// unknownCause reads the cause and the reason text of an unknown run.
func unknownCause(r BreakRun) (cause, reason string) {
	reason = r.Reason
	if reason == "" {
		reason = "outcome " + r.Outcome
		if r.Outcome == "" {
			reason = "outcome not recorded"
		}
	}
	if why, ok := SetupFailedReason(reason); ok {
		return CauseSetupFailed, why
	}
	switch {
	case strings.HasPrefix(reason, "no runner configured"):
		return CauseNoRunner, reason
	case partialRun(r):
		return CausePartial, reason
	}
	return CauseOther, reason
}
