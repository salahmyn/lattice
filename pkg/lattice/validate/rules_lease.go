package validate

import (
	"fmt"

	"github.com/salahmyn/lattice/pkg/lattice/lease"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// checkLeases runs the v0.8 §5/§6 fleet-coordination rules over the active
// work-claim leases passed in via Options.Leases:
//
//	LEASE_SCOPE_OVERLAP  (info)    — two leases by different actors claim
//	                                 overlapping scopes; concurrent edits
//	                                 may collide (most dangerously on a
//	                                 shared Core/Contracts surface).
//	UNATTRIBUTED_CHANGE  (warning) — a lease with no actor while the
//	                                 workspace requires attribution.
//
// Leases are advisory; these rules surface the collision risk before a
// merge, they never block. With no leases (single-agent run) nothing fires.
func (c *corpus) checkLeases() []schema.Violation {
	if len(c.opts.Leases) == 0 {
		return nil
	}
	var v []schema.Violation

	if c.cfg.Autonomy.RequireActor {
		for _, l := range c.opts.Leases {
			if l.Actor == "" {
				v = append(v, schema.Violation{
					Code: schema.CodeUnattributedChange, Severity: schema.SeverityWarning,
					Message: fmt.Sprintf("lease on %q has no actor, but autonomy.require_actor is set", l.Unit),
					NextAction: &schema.NextAction{
						Kind:    "run_command",
						Command: []string{"lattice", "lease", "acquire", l.Unit, "--actor", "<agent-id>"},
						Detail:  "re-acquire the lease with --actor (or set LATTICE_ACTOR) so the transition is attributable",
					},
				})
			}
		}
	}

	for _, ov := range lease.Overlaps(c.opts.Leases) {
		v = append(v, schema.Violation{
			Code: schema.CodeLeaseScopeOverlap, Severity: schema.SeverityInfo,
			Message: fmt.Sprintf("leases on %q (%s) and %q (%s) both claim %q — concurrent edits may collide",
				ov.A.Unit, ov.A.Actor, ov.B.Unit, ov.B.Actor, ov.PathPrefix),
			NextAction: &schema.NextAction{
				Kind:   "coordinate",
				Detail: "split the shared surface, or sequence the two slices so one releases before the other edits " + ov.PathPrefix,
			},
		})
	}
	return v
}
