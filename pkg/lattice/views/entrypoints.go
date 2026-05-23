package views

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// RenderEntryPoints groups every entry point by kind and renders a
// markdown table per kind — the v0.3.0 invocation-axis answer to
// "what triggers reach this system?".
func RenderEntryPoints(kg schema.KnowledgeGraph) string {
	if len(kg.EntryPoints) == 0 {
		return "# Entry points\n\nNo entry points detected.\n"
	}
	byKind := map[string][]schema.EntryPoint{}
	for _, ep := range kg.EntryPoints {
		byKind[ep.Kind] = append(byKind[ep.Kind], ep)
	}
	var b strings.Builder
	b.WriteString("# Entry points\n\n")
	for _, kind := range orderedKinds(byKind) {
		eps := byKind[kind]
		sort.Slice(eps, func(i, j int) bool { return eps[i].ID < eps[j].ID })
		b.WriteString("## ")
		b.WriteString(prettyKind(kind))
		b.WriteString(fmt.Sprintf(" (%d)\n\n", len(eps)))
		renderTable(&b, kind, eps)
		b.WriteString("\n")
	}
	return b.String()
}

// renderTable formats one kind's entry points as a markdown table whose
// columns are tailored to the trigger shape — HTTP shows method+path,
// cron shows the schedule, queue shows the queue name, and so on.
func renderTable(b *strings.Builder, kind string, eps []schema.EntryPoint) {
	switch kind {
	case schema.EntryPointKindHTTP:
		b.WriteString("| Method | Path | Handler | Features reached |\n")
		b.WriteString("|---|---|---|---|\n")
		for _, ep := range eps {
			b.WriteString(fmt.Sprintf("| %s | `%s` | `%s` | %s |\n",
				ep.Trigger.Method, ep.Trigger.Path, shortFQN(ep.Handler.Symbol), featuresReached(ep)))
		}
	case schema.EntryPointKindCron:
		b.WriteString("| Schedule | Handler | Features reached |\n")
		b.WriteString("|---|---|---|\n")
		for _, ep := range eps {
			b.WriteString(fmt.Sprintf("| `%s` | `%s` | %s |\n",
				ep.Trigger.Schedule, shortFQN(ep.Handler.Symbol), featuresReached(ep)))
		}
	case schema.EntryPointKindCLI:
		b.WriteString("| Command | Handler | Features reached |\n")
		b.WriteString("|---|---|---|\n")
		for _, ep := range eps {
			b.WriteString(fmt.Sprintf("| `%s` | `%s` | %s |\n",
				ep.Trigger.Command, shortFQN(ep.Handler.Symbol), featuresReached(ep)))
		}
	case schema.EntryPointKindQueue, schema.EntryPointKindWebhook, schema.EntryPointKindEventConsumer:
		b.WriteString("| Queue/Event | Handler | Features reached |\n")
		b.WriteString("|---|---|---|\n")
		for _, ep := range eps {
			label := ep.Trigger.Queue
			if label == "" {
				label = ep.Trigger.Event
			}
			b.WriteString(fmt.Sprintf("| `%s` | `%s` | %s |\n",
				label, shortFQN(ep.Handler.Symbol), featuresReached(ep)))
		}
	default:
		b.WriteString("| Trigger | Handler | Features reached |\n")
		b.WriteString("|---|---|---|\n")
		for _, ep := range eps {
			b.WriteString(fmt.Sprintf("| %s | `%s` | %s |\n",
				triggerSummary(ep.Trigger), shortFQN(ep.Handler.Symbol), featuresReached(ep)))
		}
	}
}

// featuresReached lists the features on the entry point's flow, or
// reports "—" when none — the latter is the v0.3.0 signal that the
// handler exists but no feature annotates its descendants
// (UNCLASSIFIED_ENTRY_POINT once validation lands).
func featuresReached(ep schema.EntryPoint) string {
	if len(ep.Flow) == 0 {
		return "_(none — orphan trigger)_"
	}
	seen := map[string]bool{}
	var feats []string
	for _, s := range ep.Flow {
		if !seen[s.Feature] {
			seen[s.Feature] = true
			feats = append(feats, s.Feature)
		}
	}
	sort.Strings(feats)
	return strings.Join(feats, ", ")
}

// shortFQN trims a verbose PHP/Python/TS FQN to the last two segments
// for table readability. App\Http\Controllers\X::store -> Controllers\X::store
func shortFQN(fqn string) string {
	if fqn == "" {
		return "_(unresolved)_"
	}
	// Keep the method part intact; trim class namespace.
	cls, method := fqn, ""
	if i := strings.LastIndex(fqn, "::"); i >= 0 {
		cls, method = fqn[:i], fqn[i:]
	}
	for _, sep := range []string{"\\", "/", "."} {
		segs := strings.Split(cls, sep)
		if len(segs) > 2 {
			cls = strings.Join(segs[len(segs)-2:], sep)
			break
		}
	}
	return cls + method
}

func triggerSummary(t schema.Trigger) string {
	switch {
	case t.Path != "":
		return t.Method + " " + t.Path
	case t.Schedule != "":
		return "cron " + t.Schedule
	case t.Command != "":
		return "cli " + t.Command
	case t.Queue != "":
		return "queue " + t.Queue
	case t.Event != "":
		return "event " + t.Event
	}
	return "?"
}

func prettyKind(kind string) string {
	switch kind {
	case schema.EntryPointKindHTTP:
		return "HTTP routes"
	case schema.EntryPointKindCLI:
		return "CLI commands"
	case schema.EntryPointKindCron:
		return "Scheduled jobs"
	case schema.EntryPointKindQueue:
		return "Queue workers"
	case schema.EntryPointKindWebhook:
		return "Webhook receivers"
	case schema.EntryPointKindEventConsumer:
		return "Event consumers"
	case schema.EntryPointKindGRPC:
		return "gRPC services"
	}
	return kind
}

// orderedKinds returns the kinds present in byKind in a stable, human
// order — HTTP first because it's usually the dominant surface.
func orderedKinds(byKind map[string][]schema.EntryPoint) []string {
	preferred := []string{
		schema.EntryPointKindHTTP,
		schema.EntryPointKindGRPC,
		schema.EntryPointKindCLI,
		schema.EntryPointKindCron,
		schema.EntryPointKindQueue,
		schema.EntryPointKindWebhook,
		schema.EntryPointKindEventConsumer,
	}
	seen := map[string]bool{}
	var out []string
	for _, k := range preferred {
		if _, ok := byKind[k]; ok {
			out = append(out, k)
			seen[k] = true
		}
	}
	for k := range byKind {
		if !seen[k] {
			out = append(out, k)
		}
	}
	return out
}
