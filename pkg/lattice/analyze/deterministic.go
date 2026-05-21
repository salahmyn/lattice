package analyze

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// deterministicChecks runs the graph-based conflict checks (design section 21).
func deterministicChecks(proposal schema.Manifest, corpus []schema.Manifest) []Finding {
	var findings []Finding
	findings = append(findings, surfaceCollisions(proposal, corpus)...)
	findings = append(findings, invariantReuse(proposal, corpus)...)
	findings = append(findings, breakingSurfaceChanges(proposal, corpus)...)
	findings = append(findings, cycleIntroduction(proposal, corpus)...)
	findings = append(findings, migrationPhaseOrder(proposal)...)
	findings = append(findings, ruleContradictions(proposal)...)

	if len(findings) == 0 {
		findings = append(findings, Finding{Level: LevelOK, Code: "NO_DETERMINISTIC_CONFLICTS",
			Message: "no deterministic conflicts found"})
	}
	return findings
}

// surfaceKey is a stable identity for a surface entry.
func surfaceKey(s schema.Surface) string {
	switch s.Type {
	case schema.SurfaceHTTP, schema.SurfaceWebhookReceive:
		return string(s.Type) + " " + s.Method + " " + s.Path
	case schema.SurfaceEventEmit, schema.SurfaceEventConsume:
		return string(s.Type) + " " + s.Name
	case schema.SurfaceScheduled:
		return "scheduled " + s.Schedule + " " + s.Job
	case schema.SurfaceModule:
		return "module " + s.Path
	default:
		return string(s.Type)
	}
}

// httpKey ignores the surface type so http and webhook collisions on the same
// method+path are caught.
func httpKey(s schema.Surface) (string, bool) {
	if s.Type == schema.SurfaceHTTP || s.Type == schema.SurfaceWebhookReceive {
		return s.Method + " " + s.Path, true
	}
	if s.Type == schema.SurfaceEventEmit {
		return "emit " + s.Name, true
	}
	return "", false
}

func surfaceCollisions(proposal schema.Manifest, corpus []schema.Manifest) []Finding {
	owner := map[string]string{}
	for _, m := range corpus {
		if m.ID == proposal.ID {
			continue
		}
		for _, s := range m.Surface {
			if k, ok := httpKey(s); ok {
				owner[k] = m.ID
			}
		}
	}
	var findings []Finding
	for _, s := range proposal.Surface {
		k, ok := httpKey(s)
		if !ok {
			continue
		}
		if other, clash := owner[k]; clash {
			findings = append(findings, Finding{
				Level: LevelError, Code: "SURFACE_COLLISION",
				Message: fmt.Sprintf("surface %q already claimed by feature %q", surfaceKey(s), other),
				Detail:  map[string]interface{}{"surface": surfaceKey(s), "owner": other},
			})
		}
	}
	return findings
}

func invariantReuse(proposal schema.Manifest, corpus []schema.Manifest) []Finding {
	var findings []Finding
	for _, m := range corpus {
		if m.ID != proposal.ID {
			continue
		}
		existing := map[string]bool{}
		for _, inv := range m.Invariants {
			existing[inv.ID] = true
		}
		for _, inv := range proposal.Invariants {
			if existing[inv.ID] {
				findings = append(findings, Finding{
					Level: LevelWarning, Code: "INVARIANT_ID_REUSE",
					Message: fmt.Sprintf("proposed invariant %s already exists on %s; reusing the id redefines it",
						inv.ID, m.ID),
					Detail: map[string]interface{}{"invariant": inv.ID},
				})
			}
		}
	}
	return findings
}

func breakingSurfaceChanges(proposal schema.Manifest, corpus []schema.Manifest) []Finding {
	var findings []Finding
	for _, s := range proposal.Surface {
		if s.BreakingChangeFrom == "" {
			continue
		}
		consumers := consumersOf(proposal.ID, corpus)
		findings = append(findings, Finding{
			Level: LevelWarning, Code: "BREAKING_SURFACE_CHANGE",
			Message: fmt.Sprintf("breaking surface change in %q (from %s); a migration plan is required",
				surfaceKey(s), s.BreakingChangeFrom),
			Detail: map[string]interface{}{"surface": surfaceKey(s), "known_consumers": consumers},
		})
	}
	return findings
}

// consumersOf returns features that depend on the given feature.
func consumersOf(feature string, corpus []schema.Manifest) []string {
	var out []string
	for _, m := range corpus {
		for _, dep := range m.DependsOn {
			if dep == feature {
				out = append(out, m.ID)
			}
		}
	}
	sort.Strings(out)
	return out
}

func cycleIntroduction(proposal schema.Manifest, corpus []schema.Manifest) []Finding {
	deps := map[string][]string{}
	for _, m := range corpus {
		if m.ID == proposal.ID {
			continue
		}
		deps[m.ID] = m.DependsOn
	}
	deps[proposal.ID] = proposal.DependsOn

	var findings []Finding
	for _, target := range proposal.DependsOn {
		if reaches(deps, target, proposal.ID, map[string]bool{}) {
			findings = append(findings, Finding{
				Level: LevelError, Code: "DEPENDENCY_CYCLE",
				Message: fmt.Sprintf("adding depends_on %q would close a cycle back to %s", target, proposal.ID),
				Detail:  map[string]interface{}{"via": target},
			})
		}
	}
	return findings
}

// reaches reports whether `from` can reach `to` through the dependency graph.
func reaches(deps map[string][]string, from, to string, seen map[string]bool) bool {
	if from == to {
		return true
	}
	if seen[from] {
		return false
	}
	seen[from] = true
	for _, n := range deps[from] {
		if reaches(deps, n, to, seen) {
			return true
		}
	}
	return false
}

func migrationPhaseOrder(proposal schema.Manifest) []Finding {
	if proposal.Migration == nil {
		return nil
	}
	var findings []Finding
	sawPending := false
	for _, ph := range proposal.Migration.Phases {
		switch ph.Status {
		case schema.PhasePending, schema.PhaseInProgress:
			sawPending = true
		case schema.PhaseComplete:
			if sawPending {
				findings = append(findings, Finding{
					Level: LevelWarning, Code: "MIGRATION_PHASE_ORDER",
					Message: fmt.Sprintf("migration phase %q is complete but an earlier phase is not", ph.ID),
					Detail:  map[string]interface{}{"phase": ph.ID},
				})
			}
		}
	}
	return findings
}

// ruleContradictions is a light heuristic: it flags capabilities whose rules
// use conditional logic ("iff", "and not") for human review.
func ruleContradictions(proposal schema.Manifest) []Finding {
	var findings []Finding
	for _, cap := range proposal.Capabilities {
		for _, rule := range cap.Rules {
			low := strings.ToLower(rule)
			if strings.Contains(low, " iff ") || strings.Contains(low, " and not ") {
				findings = append(findings, Finding{
					Level: LevelWarning, Code: "CONDITIONAL_RULE",
					Message: fmt.Sprintf("capability %q has a conditional rule; review for contradiction", cap.ID),
					Detail:  map[string]interface{}{"capability": cap.ID, "rule": rule},
				})
				break
			}
		}
	}
	return findings
}
