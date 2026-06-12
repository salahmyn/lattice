package validate

// v0.8.1 — lifecycle governance rules.
//
// These keep the graph honest about WHO established a claim and WHAT
// criticality it carries:
//
//   AUTHOR_NOT_SEPARATED   (info)    — V8: a unit's entire attributed ledger
//                                      history is one actor, including the
//                                      move to demonstrated. Self-verified
//                                      work needs an independent pass.
//   MUTATION_REQUIRED_TIER (warning) — a tier-2+ criterion chain carries no
//                                      mutation evidence. At tier 2+ a
//                                      verifier that exists is not enough;
//                                      it must be shown to constrain the
//                                      enforcer (V10 inversion).
//   CRITERION_FLAGGED      (info)    — an open meaning flag on a unit. A
//                                      validate run never reads clean while
//                                      a meaning question is open.
//
// All are info/warning: they report honesty problems, they never block.

import (
	"fmt"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func (c *corpus) checkGovernance() []schema.Violation {
	var v []schema.Violation
	v = append(v, c.checkAuthorSeparation()...)
	if !c.cfg.IsLite() {
		v = append(v, c.checkMutationTierGate()...)
	}
	v = append(v, c.checkOpenFlags()...)
	return v
}

// checkAuthorSeparation is V8, approximated at the ledger's unit
// granularity: when every attributed transition for a unit names the
// same actor and that history reaches demonstrated, nobody independent
// has touched the claim. Unattributed histories are skipped — they
// can't prove separation, but UNATTRIBUTED_CHANGE already covers them.
func (c *corpus) checkAuthorSeparation() []schema.Violation {
	if len(c.opts.LedgerEntries) == 0 {
		return nil
	}
	type hist struct {
		actors       map[string]bool
		demonstrated bool
	}
	byUnit := map[string]*hist{}
	for _, e := range c.opts.LedgerEntries {
		if e.Kind() != "transition" && e.Kind() != "" {
			continue
		}
		actor := strings.TrimSpace(e.Actor)
		if actor == "" || actor == "unattributed" {
			continue
		}
		h := byUnit[e.Unit]
		if h == nil {
			h = &hist{actors: map[string]bool{}}
			byUnit[e.Unit] = h
		}
		h.actors[actor] = true
		if e.To() == "demonstrated" {
			h.demonstrated = true
		}
	}

	var v []schema.Violation
	for unit, h := range byUnit {
		if !h.demonstrated || len(h.actors) != 1 {
			continue
		}
		var actor string
		for a := range h.actors {
			actor = a
		}
		v = append(v, schema.Violation{
			Code:     schema.CodeAuthorNotSeparated,
			Severity: schema.SeverityInfo,
			Message: fmt.Sprintf(
				"%s is self-verified: %s wired and demonstrated it with no independent pass (V8)",
				unit, actor),
			NextAction: &schema.NextAction{
				Kind:   "request_review",
				Detail: "have a different actor re-run the checks or extend the verifiers",
			},
		})
	}
	return v
}

// checkMutationTierGate enforces V10's gate at tier 2+: a criterion of
// pinned tier 2 or 3 whose backing invariant carries no mutation
// evidence gets a warning. Direct-wired criteria are skipped — mutation
// scores key off invariants; wire one to make the gate enforceable.
func (c *corpus) checkMutationTierGate() []schema.Violation {
	var v []schema.Violation
	for _, b := range c.kg.BRDs {
		for _, sc := range b.SuccessCriteria {
			if sc.EffectiveTier() < 2 || sc.DirectWire {
				continue
			}
			feature, inv := resolveRef(sc.MapsToInvariant, "")
			if feature == "" || inv == "" {
				continue // unmapped/phantom — other rules own that
			}
			f := c.features[feature]
			if f == nil {
				continue
			}
			if _, has := f.MutationScores[inv]; has {
				continue
			}
			v = append(v, schema.Violation{
				Code:        schema.CodeMutationRequiredTier,
				Severity:    schema.SeverityWarning,
				FeatureID:   feature,
				InvariantID: inv,
				Message: fmt.Sprintf(
					"%s/%s is tier %d but %s has no mutation evidence — at tier 2+ the verifier must be shown to constrain the enforcer",
					b.ID, sc.ID, sc.EffectiveTier(), inv),
				NextAction: &schema.NextAction{
					Kind:    "run_command",
					Command: []string{"lattice", "mutation", "run", "--feature", feature},
				},
			})
		}
	}
	return v
}

// checkOpenFlags surfaces every open meaning flag so validate output
// carries the flag next to whatever else is green.
func (c *corpus) checkOpenFlags() []schema.Violation {
	if c.opts.FlagsOf == nil {
		return nil
	}
	var v []schema.Violation
	for _, b := range c.kg.BRDs {
		units := make([]string, 0, len(b.SuccessCriteria)+len(b.UserScenarios))
		for _, sc := range b.SuccessCriteria {
			units = append(units, b.ID+"/"+sc.ID)
		}
		for _, us := range b.UserScenarios {
			units = append(units, b.ID+"/"+us.ID)
		}
		for _, unit := range units {
			for _, reason := range c.opts.FlagsOf(unit) {
				v = append(v, schema.Violation{
					Code:     schema.CodeCriterionFlagged,
					Severity: schema.SeverityInfo,
					Message:  fmt.Sprintf("%s has an open meaning flag: %s", unit, reason),
					NextAction: &schema.NextAction{
						Kind:   "human_gate",
						Detail: "a human confirms or bounces the flag: lattice flag clear " + unit + " --by <human>",
					},
				})
			}
		}
	}
	return v
}
