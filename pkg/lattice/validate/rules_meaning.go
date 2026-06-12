package validate

import (
	"fmt"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// checkMeaning runs the v0.8 δ meaning-fidelity rules. α/β/γ make a green
// check mean *demonstrated*; δ makes it mean *correctly meant* — it
// attacks the case where a wired link means LESS than the layer above
// asked (the SC ⊃ INV narrowing).
//
//	ENFORCER_NOT_GUARD           (info)    — @enforces symbol is a tag, not a guard
//	INVARIANT_MUTANT_SURVIVED    (warning) — a mutant of the enforcer survived
//	INVARIANT_UNFALSIFIABLE      (info)    — enforcer has no reachable violating path
//	CRITERION_INVARIANT_NARROWER (info)    — invariant does not entail its criterion (assisted)
//
// The narrowing check is *assisted*: it is a deterministic no-op unless
// agentic.llm.enabled is true, and it is never run for a BRD that carries
// a regulatory / legal / financial constraint — those stay human-judged.
// It never gates and never auto-edits.
func (c *corpus) checkMeaning() []schema.Violation {
	var v []schema.Violation

	// Symbol kind index — a guard is a function/method with a violating
	// path; a tag is a class/module/file that merely carries the tag.
	kindOf := map[string]string{}
	for _, s := range c.kg.Symbols {
		kindOf[s.FQN] = strings.ToLower(s.Kind)
	}

	matrix := rtm.Build(c.kg, rtm.Options{
		MutationThreshold: c.cfg.MutationTesting.Thresholds.Default,
		ResultOf:          c.opts.ResultOf,
	})

	brdByID := map[string]*schema.BRD{}
	for i := range c.kg.BRDs {
		brdByID[c.kg.BRDs[i].ID] = &c.kg.BRDs[i]
	}

	for _, row := range matrix.Rows {
		loc := &schema.Location{}
		if b := brdByID[row.BRDID]; b != nil {
			loc.File = b.SourcePath
		}

		// ENFORCER_NOT_GUARD / INVARIANT_UNFALSIFIABLE — only meaningful
		// when there ARE enforcers to inspect.
		if len(row.Enforcers) > 0 {
			guards := 0
			for _, fqn := range row.Enforcers {
				if isGuardKind(kindOf[fqn]) {
					guards++
				}
			}
			if guards == 0 {
				v = append(v, schema.Violation{
					Code: schema.CodeEnforcerNotGuard, Severity: schema.SeverityInfo,
					InvariantID: row.InvariantID, FeatureID: row.FeatureID,
					Message: fmt.Sprintf("invariant %s on %s: %d enforcer(s) carry the @enforces tag but none is a guard (function/method with a reject path)",
						row.InvariantID, row.FeatureID, len(row.Enforcers)),
					Location: loc,
					NextAction: &schema.NextAction{
						Kind:   "add_annotation",
						Detail: "move @enforces onto the function that actually rejects the violating case, not the enclosing class/module",
					},
				})
				// With no guard AND no mutation evidence, the invariant has
				// no demonstrated violating path — it may pass by absence.
				if !row.HasMutation {
					v = append(v, schema.Violation{
						Code: schema.CodeInvariantUnfalsifiable, Severity: schema.SeverityInfo,
						InvariantID: row.InvariantID, FeatureID: row.FeatureID,
						Message: fmt.Sprintf("invariant %s on %s may be unfalsifiable — no guard symbol and no mutation evidence of a violating path",
							row.InvariantID, row.FeatureID),
						Location: loc,
						NextAction: &schema.NextAction{
							Kind:    "run_command",
							Command: []string{"lattice", "mutation", "run"},
							Detail:  "run mutation to prove the invariant can fail, or add a guard the verifier's negative case exercises",
						},
					})
				}
			}

			// INVARIANT_MUTANT_SURVIVED — mechanical proof the invariant is
			// unfalsified: a mutant of the enforcer survived the suite.
			// Manifest mutation scores are on the 0–100 scale (the same scale
			// MutationThreshold compares against); any score below 100 means
			// at least one mutant survived.
			if row.HasMutation && row.MutationScore < 100.0 {
				v = append(v, schema.Violation{
					Code: schema.CodeInvariantMutantSurvived, Severity: schema.SeverityWarning,
					InvariantID: row.InvariantID, FeatureID: row.FeatureID,
					Message: fmt.Sprintf("invariant %s on %s: a mutant of the enforcer survived (score %.2f) — the suite does not pin the invariant",
						row.InvariantID, row.FeatureID, row.MutationScore),
					Location: loc,
					NextAction: &schema.NextAction{
						Kind:   "add_verification",
						Detail: "add a negative-case test that kills the surviving mutant",
					},
				})
			}
		}

		// CRITERION_INVARIANT_NARROWER — assisted entailment, gated hard.
		if c.cfg.Agentic.LLM.Enabled && row.Invariant != "" && row.Statement != "" {
			b := brdByID[row.BRDID]
			if b != nil && !hasHumanJudgedConstraint(b) {
				if missing := narrowingTerms(row.Statement, row.Invariant); len(missing) >= 2 {
					v = append(v, schema.Violation{
						Code: schema.CodeCriterionInvariantNarrower, Severity: schema.SeverityInfo,
						InvariantID: row.InvariantID, FeatureID: row.FeatureID,
						Message: fmt.Sprintf("criterion %s of BRD %q may be narrowed by %s: the invariant does not mention %q",
							row.CriterionID, row.BRDID, row.MapsTo, strings.Join(missing, ", ")),
						Location: loc,
						NextAction: &schema.NextAction{
							Kind:   "edit_manifest",
							Detail: "widen the invariant (or add another) until it entails the criterion; do not remap to a narrower convenient invariant",
						},
					})
				}
			}
		}
	}
	return v
}

// isGuardKind reports whether a symbol kind can host a violating path.
func isGuardKind(kind string) bool {
	switch kind {
	case "function", "method", "func":
		return true
	}
	return false
}

// hasHumanJudgedConstraint reports whether the BRD carries a regulatory,
// legal, or financial constraint — δ never machine-judges the meaning of
// those, so the narrowing check skips the whole BRD.
func hasHumanJudgedConstraint(b *schema.BRD) bool {
	for _, k := range b.Constraints {
		switch k.Kind {
		case schema.ConstraintRegulatory, schema.ConstraintLegal, schema.ConstraintFinancial:
			return true
		}
	}
	return false
}

// narrowingTerms is the deterministic lexical fallback for the assisted
// entailment check: salient content words in the criterion that are absent
// from the invariant statement. It only ever runs when the LLM is enabled
// (the real entailment path would replace it); it is conservative —
// callers require >=2 missing terms before flagging — so it informs rather
// than nags. A true LLM entailment provider slots in here later.
func narrowingTerms(criterion, invariant string) []string {
	inv := wordSet(invariant)
	var missing []string
	seen := map[string]bool{}
	for _, w := range contentWords(criterion) {
		if !inv[w] && !seen[w] {
			seen[w] = true
			missing = append(missing, w)
		}
	}
	return missing
}

func wordSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range contentWords(s) {
		out[w] = true
	}
	return out
}

// contentWords lowercases, strips punctuation, and drops short/stopword
// tokens so the comparison keys off salient terms.
func contentWords(s string) []string {
	var out []string
	for _, raw := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if len(raw) < 4 || meaningStopwords[raw] {
			continue
		}
		out = append(out, raw)
	}
	return out
}

// meaningStopwords are common words that carry no entailment signal.
var meaningStopwords = map[string]bool{
	"that": true, "this": true, "with": true, "from": true, "they": true,
	"there": true, "their": true, "which": true, "when": true, "where": true,
	"will": true, "shall": true, "must": true, "always": true, "never": true,
	"each": true, "every": true, "some": true, "into": true, "over": true,
	"then": true, "than": true, "have": true, "been": true, "were": true,
	"call": true, "calls": true, "returns": true, "return": true, "value": true,
}
