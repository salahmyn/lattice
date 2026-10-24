package validate

// @module-feature validation

import (
	"fmt"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/rtm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// requiredLevels are the truth levels validate.require_level accepts.
var requiredLevels = map[string]rtm.Status{
	"verified":     rtm.StatusVerified,
	"demonstrated": rtm.StatusDemonstrated,
}

// checkRequiredLevel gates every BRD success criterion and user scenario on
// validate.require_level. The RTM already computes each one's status, but a
// verified criterion raises no finding and an unmapped one only an info, so
// "every criterion demonstrated" could not fail a run. BRD_BELOW_LEVEL
// (error) names each unit below the level; known gaps go in the allowlist
// under their subject (brd.<id>:SC-N, brd.<id>:US-N).
// @enforces validation:INV-8
func (c *corpus) checkRequiredLevel() []schema.Violation {
	name := strings.ToLower(strings.TrimSpace(c.cfg.Validate.RequireLevel))
	if name == "" {
		return nil
	}
	level, ok := requiredLevels[name]
	if !ok {
		return []schema.Violation{{
			Code: schema.CodeValidatePolicy, Severity: schema.SeverityError,
			Subject:  "require_level",
			Message:  fmt.Sprintf("config.yaml validate.require_level: %q is not one of verified, demonstrated", c.cfg.Validate.RequireLevel),
			Location: &schema.Location{File: "config.yaml"},
		}}
	}

	matrix := rtm.Build(c.kg, rtm.Options{
		MutationThreshold: c.cfg.MutationTesting.Thresholds.Default,
		ResultOf:          c.opts.ResultOf,
	})
	brdFile := map[string]string{}
	for i := range c.kg.BRDs {
		brdFile[c.kg.BRDs[i].ID] = c.kg.BRDs[i].SourcePath
	}
	below := func(brdID, unit string, status rtm.Status, reason string) schema.Violation {
		msg := fmt.Sprintf("BRD %q %s is %s, below the required %s", brdID, unit, status, name)
		if reason != "" {
			msg += ": " + reason
		}
		return schema.Violation{
			Code: schema.CodeBRDBelowLevel, Severity: schema.SeverityError,
			Subject:  brdID + ":" + unit,
			Message:  msg,
			Location: &schema.Location{File: brdFile[brdID]},
			NextAction: &schema.NextAction{
				Kind:   "add_verification",
				Detail: "reach " + name + " (map, enforce and verify it, then ingest a passing result), or allowlist it with a reason",
			},
		}
	}

	var v []schema.Violation
	for _, r := range matrix.Rows {
		if r.Status.Severity() < level.Severity() {
			v = append(v, below(r.BRDID, r.CriterionID, r.Status, r.StatusReason))
		}
	}
	for _, s := range matrix.Scenarios {
		if s.Status.Severity() < level.Severity() {
			v = append(v, below(s.BRDID, s.ScenarioID, s.Status, s.StatusReason))
		}
	}
	return v
}
