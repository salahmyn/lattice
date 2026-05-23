package views

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// RenderFlows emits a Mermaid flowchart per entry point: trigger →
// handler → reached features (with capability when known) → side
// effects. When epID is set, only that entry point's flow is rendered;
// otherwise every EP with a non-empty flow appears.
func RenderFlows(kg schema.KnowledgeGraph, epID string) string {
	eps := kg.EntryPoints
	if epID != "" {
		var match []schema.EntryPoint
		for _, ep := range eps {
			if ep.ID == epID {
				match = append(match, ep)
			}
		}
		eps = match
	} else {
		// Default: hide orphan EPs (no flow yet) — the user usually
		// wants to see the connected surface, not 200 empty entries.
		var withFlow []schema.EntryPoint
		for _, ep := range eps {
			if len(ep.Flow) > 0 {
				withFlow = append(withFlow, ep)
			}
		}
		eps = withFlow
	}
	if len(eps) == 0 {
		if epID != "" {
			return "# Flow\n\nNo entry point with id `" + epID + "` (try `lattice view entry-points` to list).\n"
		}
		return "# Flows\n\nNo entry point reaches a feature yet — run `lattice extract` after `lattice import inscribe` so the graph has feature implementations, then re-render.\n"
	}
	sort.Slice(eps, func(i, j int) bool { return eps[i].ID < eps[j].ID })

	var b strings.Builder
	b.WriteString("# Flows\n\n")
	for i, ep := range eps {
		if i > 0 {
			b.WriteString("\n---\n\n")
		}
		renderOneFlow(&b, ep)
	}
	return b.String()
}

func renderOneFlow(b *strings.Builder, ep schema.EntryPoint) {
	b.WriteString("## " + ep.ID + "\n\n")
	if ep.Purpose != "" {
		b.WriteString("> " + ep.Purpose + "\n\n")
	}
	b.WriteString("```mermaid\nflowchart LR\n")

	trigNode := nodeID("T", ep.ID)
	handNode := nodeID("H", ep.Handler.Symbol)
	b.WriteString(fmt.Sprintf("    %s[\"%s\"] --> %s[%q]\n",
		trigNode, mermaidEscape(triggerLabel(ep)), handNode, mermaidEscape(shortFQN(ep.Handler.Symbol))))

	// Each reached feature is one node; the via-symbols become tooltip
	// content via |label| edges so the diagram stays readable.
	for i, step := range ep.Flow {
		fnode := nodeID("F", fmt.Sprintf("%s_%d", ep.ID, i))
		featLabel := step.Feature
		if step.Capability != "" {
			featLabel += "\\n(" + step.Capability + ")"
		}
		b.WriteString(fmt.Sprintf("    %s -->|via| %s[\"%s\"]\n",
			handNode, fnode, mermaidEscape(featLabel)))
	}
	for i, se := range ep.SideEffects {
		snode := nodeID("S", fmt.Sprintf("%s_%d", ep.ID, i))
		shape := "[(" // db cylinder
		end := ")]"
		if se.Kind == "queue_publish" {
			shape, end = "{{", "}}" // hex queue
		}
		label := se.Kind
		if se.Target != "" {
			label += ": " + se.Target
		}
		b.WriteString(fmt.Sprintf("    %s --> %s%s%s%s\n",
			handNode, snode, shape, mermaidEscape(label), end))
	}
	b.WriteString("```\n\n")
	if len(ep.Flow) > 0 {
		b.WriteString("**Features reached:** ")
		seen := map[string]bool{}
		var feats []string
		for _, s := range ep.Flow {
			if !seen[s.Feature] {
				seen[s.Feature] = true
				feats = append(feats, s.Feature)
			}
		}
		b.WriteString(strings.Join(feats, ", ") + "\n\n")
	}
}

func triggerLabel(ep schema.EntryPoint) string {
	switch ep.Kind {
	case schema.EntryPointKindHTTP:
		return ep.Trigger.Method + " " + ep.Trigger.Path
	case schema.EntryPointKindCron:
		return "cron " + ep.Trigger.Schedule
	case schema.EntryPointKindCLI:
		return "cli " + ep.Trigger.Command
	case schema.EntryPointKindQueue:
		return "queue " + ep.Trigger.Queue
	case schema.EntryPointKindEventConsumer:
		return "event " + ep.Trigger.Event
	}
	return ep.Kind
}

// nodeID makes a mermaid-safe node id: alphanumerics + underscore only.
func nodeID(prefix, s string) string {
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString("_")
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// mermaidEscape neuters characters that break mermaid label parsing.
func mermaidEscape(s string) string {
	s = strings.ReplaceAll(s, "\"", "'")
	s = strings.ReplaceAll(s, "[", "(")
	s = strings.ReplaceAll(s, "]", ")")
	s = strings.ReplaceAll(s, "{", "(")
	s = strings.ReplaceAll(s, "}", ")")
	return s
}
