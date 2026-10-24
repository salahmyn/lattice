package validate

// @module-feature validation

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// requiredLevels are the truth levels validate.require_level accepts.
var requiredLevels = map[string]rtm.Status{
	"verified":     rtm.StatusVerified,
	"demonstrated": rtm.StatusDemonstrated,
	"exercised":    rtm.StatusExercised,
}

// levelPolicyError is the VALIDATE_POLICY finding for a bad require_level.
func levelPolicyError(msg string) schema.Violation {
	return schema.Violation{
		Code: schema.CodeValidatePolicy, Severity: schema.SeverityError,
		Subject:  "require_level",
		Message:  "config.yaml validate.require_level: " + msg,
		Location: &schema.Location{File: "config.yaml"},
	}
}

// checkRequiredLevel gates every BRD success criterion and user scenario on
// validate.require_level. The RTM already computes each one's status, but a
// verified criterion raises no finding and an unmapped one only an info, so
// "every criterion demonstrated" could not fail a run. BRD_BELOW_LEVEL
// (error) names each unit below the level; known gaps go in the allowlist
// under their subject (brd.<id>:SC-N, brd.<id>:US-N).
//
// The level may differ per criterion tier ({default: demonstrated, tier:
// {3: exercised}}): a criterion uses its tier's level, else the default;
// scenarios have no tier and use the default.
// @enforces validation:INV-8, validation:INV-12
func (c *corpus) checkRequiredLevel() []schema.Violation {
	policy := c.cfg.Validate.RequireLevel
	if policy.IsZero() {
		return nil
	}
	parse := func(name string) (rtm.Status, string, bool) {
		n := strings.ToLower(strings.TrimSpace(name))
		if n == "" {
			return "", "", true
		}
		l, ok := requiredLevels[n]
		return l, n, ok
	}
	if _, _, ok := parse(policy.Default); !ok {
		return []schema.Violation{levelPolicyError(fmt.Sprintf("%q is not one of verified, demonstrated, exercised", policy.Default))}
	}
	tiers := make([]int, 0, len(policy.Tier))
	for t := range policy.Tier {
		tiers = append(tiers, t)
	}
	sort.Ints(tiers)
	for _, t := range tiers {
		if t < 1 || t > 3 {
			return []schema.Violation{levelPolicyError(fmt.Sprintf("tier %d: a criterion tier is 1, 2 or 3", t))}
		}
		if _, _, ok := parse(policy.Tier[t]); !ok {
			return []schema.Violation{levelPolicyError(fmt.Sprintf("tier %d: %q is not one of verified, demonstrated, exercised", t, policy.Tier[t]))}
		}
	}

	matrix := rtm.Build(c.kg, c.rtmOptions())
	brdFile := map[string]string{}
	for i := range c.kg.BRDs {
		brdFile[c.kg.BRDs[i].ID] = c.kg.BRDs[i].SourcePath
	}
	below := func(brdID, unit string, status rtm.Status, name, scope, reason string) schema.Violation {
		msg := fmt.Sprintf("BRD %q %s is %s, below the required %s%s", brdID, unit, status, name, scope)
		if reason != "" {
			msg += ": " + reason
		}
		detail := "reach " + name + " (map, enforce and verify it, then ingest a passing result), or allowlist it with a reason"
		if name == "exercised" {
			detail = "ingest per-test coverage of its verifiers (`lattice results tests --verifiers`, then `lattice results ingest --per-test <dir>`) and make a verifier run an enforcer, or allowlist it with a reason"
		}
		return schema.Violation{
			Code: schema.CodeBRDBelowLevel, Severity: schema.SeverityError,
			Subject:    brdID + ":" + unit,
			Message:    msg,
			Location:   &schema.Location{File: brdFile[brdID]},
			NextAction: &schema.NextAction{Kind: "add_verification", Detail: detail},
		}
	}
	// reason says why a unit is below the level. A demonstrated unit below
	// exercised says what the coverage lacks.
	reason := func(status rtm.Status, statusReason, coverageReason string, level rtm.Status) string {
		if level == rtm.StatusExercised && status == rtm.StatusDemonstrated {
			if c.opts.Coverage == nil {
				return "no per-test coverage ingested"
			}
			if coverageReason != "" {
				return coverageReason
			}
		}
		return statusReason
	}

	var v []schema.Violation
	for _, r := range matrix.Rows {
		level, name, _ := parse(policy.For(r.Tier))
		if name == "" {
			continue
		}
		scope := ""
		if _, perTier := policy.Tier[r.Tier]; perTier {
			scope = fmt.Sprintf(" for tier %d", r.Tier)
		}
		if r.Status.Severity() < level.Severity() {
			v = append(v, below(r.BRDID, r.CriterionID, r.Status, name, scope,
				reason(r.Status, r.StatusReason, r.CoverageReason, level)))
		}
	}
	level, name, _ := parse(policy.Default)
	if name == "" {
		return v
	}
	for _, s := range matrix.Scenarios {
		if s.Status.Severity() < level.Severity() {
			v = append(v, below(s.BRDID, s.ScenarioID, s.Status, name, "",
				reason(s.Status, s.StatusReason, s.CoverageReason, level)))
		}
	}
	return v
}
