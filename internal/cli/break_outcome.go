package cli

// @module-feature breaktest

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/breaktest"
)

// failOnCode is the code of the failure --fail-on reports.
const failOnCode = "BREAK_OUTCOME"

// failOnOutcomes are the outcomes --fail-on takes, in the order messages
// list them.
var failOnOutcomes = []string{breaktest.Survived, breaktest.Unknown}

// parseFailOn reads --fail-on (repeated flags and comma lists both arrive
// here as one list). With --fails-on-base only unknown applies: that run has
// no survived outcome, and passes_on_base is not one.
func parseFailOn(values []string, failsOnBase bool) (map[string]bool, error) {
	want := map[string]bool{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		switch v {
		case "":
		case breaktest.Survived, breaktest.Unknown:
			want[v] = true
		default:
			return nil, fmt.Errorf("--fail-on: unknown outcome %q (want survived or unknown, as a comma list)", v)
		}
	}
	if failsOnBase && want[breaktest.Survived] {
		return nil, fmt.Errorf("--fail-on survived does not apply to --fails-on-base, whose outcomes are fails_on_base, passes_on_base and unknown; use --fail-on unknown")
	}
	return want, nil
}

// reasonCount is one reason of the unknown runs and how many runs give it.
type reasonCount struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// breakSummary counts the runs an invocation shows (the --promise or
// --feature selection, else every run of the commit).
type breakSummary struct {
	Runs     int  `json:"runs"`
	Killed   int  `json:"killed"`
	Survived int  `json:"survived"`
	Unknown  int  `json:"unknown"`
	Ran      bool `json:"ran"` // some run was killed or survived
	// UnknownReasons are the three most common reasons of the unknown runs.
	// Runs with no runner count under one reason, "no runner configured",
	// whatever languages each lacks.
	UnknownReasons []reasonCount `json:"unknown_reasons,omitempty"`
	// MissingRunners counts, per language, the unknown runs that lack its
	// runner, most first: a run whose verifiers need two runners counts
	// under both.
	MissingRunners []reasonCount `json:"missing_runners,omitempty"`
}

// noRunnerReason is the reason a summary groups runs with no runner under.
const noRunnerReason = "no runner configured"

// breakOutput is what `lattice break --json` prints: the report as written
// to breaks.json, the summary of the shown runs, and, when --fail-on
// matched, the error and code of the failure envelope.
type breakOutput struct {
	breaktest.Report
	Summary breakSummary `json:"summary"`
	Error   string       `json:"error,omitempty"`
	Code    string       `json:"code,omitempty"`
}

// failsOnBaseOutput is `lattice break --fails-on-base --json`: the report,
// and the failure's error and code when --fail-on matched.
type failsOnBaseOutput struct {
	breaktest.FailsOnBaseReport
	Error string `json:"error,omitempty"`
	Code  string `json:"code,omitempty"`
}

func summarize(runs []breaktest.Run) breakSummary {
	s := breakSummary{Runs: len(runs)}
	reasons, missing := map[string]int{}, map[string]int{}
	for _, r := range runs {
		switch r.Outcome {
		case breaktest.Killed:
			s.Killed++
		case breaktest.Survived:
			s.Survived++
		default:
			s.Unknown++
			if langs, ok := breaktest.NoRunnerLanguages(r.Reason); ok {
				reasons[noRunnerReason]++
				for _, l := range langs {
					missing[l]++
				}
				continue
			}
			reasons[r.Reason]++
		}
	}
	s.Ran = s.Killed+s.Survived > 0
	s.UnknownReasons = rankCounts(reasons, 3)
	s.MissingRunners = rankCounts(missing, 0)
	return s
}

// rankCounts lists counts most first, then by name; limit > 0 keeps that many.
func rankCounts(m map[string]int, limit int) []reasonCount {
	var out []reasonCount
	for k, n := range m {
		out = append(out, reasonCount{Reason: k, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Reason < out[j].Reason
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// failOnMessage is the failure --fail-on reports for the shown runs' counts,
// or "" when no shown run has a listed outcome. Without --fail-on it is
// always "", so the exit code does not depend on the outcomes.
// @enforces breaktest:INV-12
func failOnMessage(want map[string]bool, counts map[string]int) string {
	var listed, parts []string
	hit := false
	for _, o := range failOnOutcomes {
		if !want[o] {
			continue
		}
		listed = append(listed, o)
		parts = append(parts, fmt.Sprintf("%d %s", counts[o], o))
		hit = hit || counts[o] > 0
	}
	if !hit {
		return ""
	}
	return strings.Join(parts, ", ") + "; --fail-on " + strings.Join(listed, ",")
}

// nothingRanLine is the closing line when every shown run is unknown: no
// break ran, so nothing was learned about the verifiers.
// @enforces breaktest:INV-12
func nothingRanLine(s breakSummary) string {
	if s.Runs == 0 || s.Ran {
		return ""
	}
	var top []string
	for _, rc := range s.UnknownReasons {
		top = append(top, fmt.Sprintf("%s ×%d", rc.Reason, rc.Count))
	}
	more := ""
	if rest := s.Unknown - sumCounts(s.UnknownReasons); rest > 0 {
		more = ", …"
	}
	runners := ""
	if len(s.MissingRunners) > 0 {
		var parts []string
		for _, rc := range s.MissingRunners {
			parts = append(parts, fmt.Sprintf("%s ×%d", rc.Reason, rc.Count))
		}
		runners = "; missing runners: " + strings.Join(parts, ", ") + " (set break.runners or break.commands)"
	}
	return fmt.Sprintf("no break ran: %d of %s unknown (%s%s)%s; break_tested stays unknown",
		s.Unknown, plural(s.Runs, "run"), strings.Join(top, ", "), more, runners)
}

func sumCounts(rcs []reasonCount) int {
	n := 0
	for _, rc := range rcs {
		n += rc.Count
	}
	return n
}
