package validate

// @module-feature validation

import (
	"fmt"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// checkEntryPoints applies the v0.3.0 integrity rules to the entry-point
// axis. Errors block validation; warnings surface for triage:
//
//	UNCLASSIFIED_ENTRY_POINT (warning) — handler reaches no feature
//	DUPLICATE_TRIGGER         (warning) — two EPs claim the same trigger
//	PHANTOM_FLOW              (error)   — flow step names a non-existent feature
//	HANDLER_MISSING           (warning) — handler FQN absent from the IR
func checkEntryPoints(kg schema.KnowledgeGraph, r *reach.Result) []schema.Violation {
	if len(kg.EntryPoints) == 0 {
		return nil
	}
	featureIDs := map[string]bool{}
	for _, f := range kg.Features {
		featureIDs[f.ID] = true
	}
	symbolFQNs := map[string]bool{}
	for _, s := range kg.Symbols {
		symbolFQNs[s.FQN] = true
	}
	triggerSeen := map[string]string{} // key -> first EP id that claimed it

	var out []schema.Violation
	for _, ep := range kg.EntryPoints {
		// HANDLER_MISSING — only fires when the IR actually has symbols
		// (otherwise this would always fire in review mode).
		// A runtime root's handler is a node of the call graph (a serve
		// handler, a cron job) that holds no symbol of its own.
		if len(symbolFQNs) > 0 && ep.Handler.Symbol != "" && !symbolFQNs[ep.Handler.Symbol] &&
			!(r != nil && r.Graph != nil && r.Graph.HasNode(ep.Handler.Symbol)) {
			out = append(out, schema.Violation{
				Code:     schema.CodeHandlerMissing,
				Severity: schema.SeverityWarning,
				Message:  fmt.Sprintf("entry point %q has handler %q that does not appear in the symbol index", ep.ID, ep.Handler.Symbol),
			})
		}
		// UNCLASSIFIED_ENTRY_POINT — handler exists but flow is empty.
		if len(ep.Flow) == 0 {
			out = append(out, schema.Violation{
				Code:     schema.CodeUnclassifiedEntryPoint,
				Severity: schema.SeverityWarning,
				Message:  fmt.Sprintf("entry point %q reaches no feature — annotate the handler chain or accept it as a trampoline", ep.ID),
			})
		}
		// PHANTOM_FLOW — every named feature must exist. lattice refresh
		// warns about the same steps, over the same features.
		for _, step := range ep.Flow {
			if !featureIDs[step.Feature] {
				out = append(out, schema.Violation{
					Code:      schema.CodePhantomFlow,
					Severity:  schema.SeverityError,
					Message:   fmt.Sprintf("entry point %q flow step names non-existent feature %q", ep.ID, step.Feature),
					FeatureID: step.Feature,
					NextAction: &schema.NextAction{Kind: "edit_entry_point", Ref: ep.ID, Field: "flow",
						Detail: "name a declared feature in the step, or create the feature it names"},
				})
			}
		}
		// DUPLICATE_TRIGGER — two EPs that share (kind, method, path/etc).
		trigger := ep.Trigger.Method + " " + ep.Trigger.Path + ep.Trigger.Schedule + ep.Trigger.Job + ep.Trigger.Queue + ep.Trigger.Event + ep.Trigger.Command
		if strings.TrimSpace(trigger) == "" {
			trigger += ep.Handler.Symbol // no trigger to compare: the handler tells entry points apart
		}
		key := ep.Kind + "|" + trigger
		if prev, ok := triggerSeen[key]; ok {
			out = append(out, schema.Violation{
				Code:     schema.CodeDuplicateTrigger,
				Severity: schema.SeverityWarning,
				Message:  fmt.Sprintf("entry point %q shares its trigger with %q — routing collision", ep.ID, prev),
			})
		} else {
			triggerSeen[key] = ep.ID
		}
	}
	return out
}
