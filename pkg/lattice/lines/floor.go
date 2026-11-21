package lines

// @module-feature audit

import (
	"fmt"
	"sort"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Findings is VERIFIED_LINES_BELOW_FLOOR for each feature whose verified
// lines (checked plus run, over the known lines of its blocks with an
// intent) are below the floor, a warning. A feature whose lines are all
// unknown has no percentage to compare, so it reads "no line data" as info:
// unknown lines never count as verified, and never as a failure either. With
// no floor (0) or no report nothing fires. loc maps a feature to where it is
// declared.
// @enforces audit:INV-10
func Findings(rep *Report, floor float64, loc map[string]*schema.Location) []schema.Violation {
	if rep == nil || floor <= 0 {
		return nil
	}
	var out []schema.Violation
	for _, f := range rep.Features {
		if f.Feature == "" || f.WithIntent.Known()+f.WithIntent.Unknown == 0 {
			continue
		}
		v := schema.Violation{Code: schema.CodeVerifiedLinesBelowFloor, Subject: f.Feature, FeatureID: f.Feature,
			Location: loc[f.Feature]}
		known, unknown := f.WithIntent.Known(), f.WithIntent.Unknown
		switch {
		case known == 0:
			v.Severity = schema.SeverityInfo
			v.Message = fmt.Sprintf("feature %s: no line data for its %d lines with an intent, so its verified lines cannot be compared with the floor of %s%% (run lattice break --lines)",
				f.Feature, unknown, trim(floor))
			v.NextAction = &schema.NextAction{Kind: "run_break_lines", Command: []string{"lattice", "break", "--lines"},
				Detail: "record which lines each promise's verifiers run"}
		case f.Verified != nil && *f.Verified < floor:
			v.Severity = schema.SeverityWarning
			v.Message = fmt.Sprintf("feature %s: %.1f%% of its lines with an intent were run by their own verifiers (%d of %d known lines, %d unknown), below the verified-lines floor of %s%%",
				f.Feature, *f.Verified, f.WithIntent.Verified(), known, unknown, trim(floor))
			v.NextAction = &schema.NextAction{Kind: "add_verification",
				Detail: "add verifiers that run the lines listed by lattice audit --lines, or lower the floor in config.yaml coverage.floors.verified_lines"}
		default:
			continue
		}
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Subject < out[j].Subject })
	return out
}

func trim(f float64) string {
	s := fmt.Sprintf("%.1f", f)
	if len(s) > 2 && s[len(s)-2:] == ".0" {
		s = s[:len(s)-2]
	}
	return s
}
