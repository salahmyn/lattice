package validate

// @module-feature validation

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// breaksPath is where lattice break writes its result.
const breaksPath = "lattice/.cache/results/breaks.json"

// maxReasonRunes clips a reason text in a finding's message.
const maxReasonRunes = 160

// checkBreakFacts reports what break testing showed, as facts. Every finding
// is info and always on, and none changes a status, a level or a gate.
//
//	BREAK_SURVIVED       (info) — per feature: promises whose break no verifier noticed
//	BREAK_UNKNOWN_CAUSE  (info) — per feature: unknown runs grouped by cause;
//	                              once, when there is no break evidence at all
//
// The file is read by the audit's rule (audit.ReadBreakFacts): one that is
// missing, of another schema or of another commit is no evidence, and no
// evidence never reads as a pass. The rule runs only when the caller says it
// read the cache (Options.BreaksRead).
// @enforces validation:INV-23, validation:INV-24
func (c *corpus) checkBreakFacts() []schema.Violation {
	if !c.opts.BreaksRead {
		return nil
	}
	facts := audit.ReadBreakFacts(c.opts.Breaks, c.kg.GeneratedFromCommit)
	if !facts.Evidence {
		return c.noBreakEvidence(facts.Why)
	}
	loc := featureLocations(c.kg)
	dirty := ""
	if facts.Dirty {
		dirty = ", run on a dirty tree"
	}
	var v []schema.Violation

	for _, feature := range sortedFeatureKeys(facts.Survived) {
		runs := facts.Survived[feature]
		var items, refs []string
		seen := map[string]bool{}
		for _, r := range runs {
			how := r.Method
			if how == "" {
				how = "method not recorded"
			}
			items = append(items, fmt.Sprintf("%s (enforcer %s, %s)", r.Promise, r.Enforcer, how))
			if !seen[r.Promise] {
				seen[r.Promise] = true
				refs = append(refs, r.Promise)
			}
		}
		noun := "promises"
		if len(refs) == 1 {
			noun = "promise"
		}
		v = append(v, schema.Violation{
			Code: schema.CodeBreakSurvived, Severity: schema.SeverityInfo,
			FeatureID: feature, Subject: feature, Refs: refs, Location: loc[feature],
			Message: fmt.Sprintf("feature %q has %d break-tested %s whose break survived: every verifier passed with the enforcer broken: %s (evidence: breaks.json at this commit%s)",
				feature, len(refs), noun, strings.Join(items, "; "), dirty),
			NextAction: &schema.NextAction{Kind: "add_verification", Ref: refs[0],
				Detail: "see `lattice audit` for each promise; a survived break means the verifiers do not notice that enforcer failing"},
		})
	}

	for _, feature := range sortedFeatureKeys(facts.Unknown) {
		groups := facts.Unknown[feature]
		total := 0
		var parts []string
		seen := map[string]bool{}
		var refs []string
		for _, g := range groups {
			total += g.Count
			parts = append(parts, fmt.Sprintf("%s x%d (%s)", causeLabel(g.Cause), g.Count, clipReason(g.Reason)))
			for _, p := range g.Refs {
				if !seen[p] {
					seen[p] = true
					refs = append(refs, p)
				}
			}
		}
		sort.Strings(refs)
		noun := "runs"
		if total == 1 {
			noun = "run"
		}
		v = append(v, schema.Violation{
			Code: schema.CodeBreakUnknownCause, Severity: schema.SeverityInfo,
			FeatureID: feature, Subject: feature, Refs: refs, Location: loc[feature],
			Message: fmt.Sprintf("feature %q has %d unknown break %s, which say nothing about its verifiers; by cause: %s (evidence: breaks.json at this commit%s)",
				feature, total, noun, strings.Join(parts, "; "), dirty),
			NextAction: &schema.NextAction{Kind: "run_command",
				Command: []string{"lattice", "break", "--feature", feature},
				Detail:  "fix the cause named, then break the feature again"},
		})
	}
	return v
}

// noBreakEvidence is the one BREAK_UNKNOWN_CAUSE finding for a graph with
// invariants and no usable breaks.json. Nothing is read from it: no promise
// counts as break-tested, and none as a pass.
func (c *corpus) noBreakEvidence(why string) []schema.Violation {
	has := false
	for i := range c.kg.Features {
		if len(c.kg.Features[i].Invariants) > 0 {
			has = true
			break
		}
	}
	if !has {
		return nil
	}
	return []schema.Violation{{
		Code: schema.CodeBreakUnknownCause, Severity: schema.SeverityInfo,
		Subject:  "breaks",
		Message:  "no break evidence: " + why + "; no promise reads as break-tested, and none reads as a pass from this",
		Location: &schema.Location{File: breaksPath},
		NextAction: &schema.NextAction{Kind: "run_command",
			Command: []string{"lattice", "break"},
			Detail:  "break the enforcers at this commit to see whether their verifiers notice"},
	}}
}

func causeLabel(cause string) string {
	switch cause {
	case audit.CauseSetupFailed:
		return "setup failed"
	case audit.CauseNoRunner:
		return "no runner"
	case audit.CausePartial:
		return "partial run"
	}
	return "other"
}

func clipReason(s string) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) > maxReasonRunes {
		return string(r[:maxReasonRunes]) + "..."
	}
	return string(r)
}

func sortedFeatureKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
