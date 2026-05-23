package validate

import (
	"fmt"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// checkEntryPoints applies the v0.3.0 integrity rules to the entry-point
// axis. Errors block validation; warnings surface for triage:
//
//   UNCLASSIFIED_ENTRY_POINT (warning) — handler reaches no feature
//   DUPLICATE_TRIGGER         (warning) — two EPs claim the same trigger
//   PHANTOM_FLOW              (error)   — flow step names a non-existent feature
//   HANDLER_MISSING           (warning) — handler FQN absent from the IR
func checkEntryPoints(kg schema.KnowledgeGraph) []schema.Violation {
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
		if len(symbolFQNs) > 0 && ep.Handler.Symbol != "" && !symbolFQNs[ep.Handler.Symbol] {
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
		// PHANTOM_FLOW — every named feature must exist.
		for _, step := range ep.Flow {
			if !featureIDs[step.Feature] {
				out = append(out, schema.Violation{
					Code:      schema.CodePhantomFlow,
					Severity:  schema.SeverityError,
					Message:   fmt.Sprintf("entry point %q flow step names non-existent feature %q", ep.ID, step.Feature),
					FeatureID: step.Feature,
				})
			}
		}
		// DUPLICATE_TRIGGER — two EPs that share (kind, method, path/etc).
		key := ep.Kind + "|" + ep.Trigger.Method + " " + ep.Trigger.Path + ep.Trigger.Schedule + ep.Trigger.Queue + ep.Trigger.Event + ep.Trigger.Command
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
