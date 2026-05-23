package entrypoints

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/agentic"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// LabelOptions tunes the v0.3.1 LLM labelling of entry points. Tone
// is the same agentic.ToneContract used by the v0.2.1 importer, so a
// single tone setting steers feature drafts AND entry-point purposes.
type LabelOptions struct {
	Provider     agentic.Provider
	SystemPrompt string // typically: agentic.ToneContract(...) + baseLabelPrompt
	MaxTokens    int
	// Skip is a set of EP ids the labeler should leave untouched —
	// typically the ones already on disk with a human-authored purpose.
	Skip map[string]bool
}

const entryPointLabelPrompt = "You name one entry point (HTTP route / CLI command / cron / queue) " +
	"in a software system. Given the trigger metadata and handler symbol, " +
	"reply with JSON {\"purpose\": \"<one sentence, no jargon unless the audience contract permits>\"}. " +
	"Reply with JSON only."

// LabelEntryPoints fills in EntryPoint.Purpose for every EP whose
// purpose is empty and whose id isn't in opts.Skip. Failure on one
// EP doesn't block the others — labels are independent.
func LabelEntryPoints(ctx context.Context, eps []schema.EntryPoint, opts LabelOptions) []schema.EntryPoint {
	if opts.Provider == nil || !agentic.Enabled(opts.Provider) {
		return eps
	}
	sysPrompt := opts.SystemPrompt
	if sysPrompt == "" {
		sysPrompt = entryPointLabelPrompt
	} else if !strings.Contains(sysPrompt, "JSON") {
		// Caller passed a tone contract without the JSON instruction;
		// append the labeler's contract.
		sysPrompt = sysPrompt + "\n\n" + entryPointLabelPrompt
	}
	for i := range eps {
		ep := &eps[i]
		if ep.Purpose != "" {
			continue
		}
		if opts.Skip[ep.ID] {
			continue
		}
		purpose := labelOne(ctx, *ep, opts.Provider, sysPrompt, opts.MaxTokens)
		if purpose != "" {
			ep.Purpose = purpose
		}
	}
	return eps
}

func labelOne(ctx context.Context, ep schema.EntryPoint, provider agentic.Provider, sys string, maxTok int) string {
	prompt := buildLabelPrompt(ep)
	resp, err := provider.Complete(ctx, agentic.CompletionRequest{
		SystemPrompt: sys,
		UserMessage:  prompt,
		MaxTokens:    maxTok,
	})
	if err != nil {
		return ""
	}
	var parsed struct {
		Purpose string `json:"purpose"`
	}
	text := extractJSON(resp.Text)
	if json.Unmarshal([]byte(text), &parsed) != nil {
		return ""
	}
	return strings.TrimSpace(parsed.Purpose)
}

func buildLabelPrompt(ep schema.EntryPoint) string {
	var b strings.Builder
	b.WriteString("Entry point kind: ")
	b.WriteString(ep.Kind)
	b.WriteString("\nTrigger: ")
	b.WriteString(triggerDescription(ep))
	b.WriteString("\nHandler symbol: ")
	b.WriteString(ep.Handler.Symbol)
	if len(ep.Flow) > 0 {
		b.WriteString("\nFeatures reached:")
		for _, s := range ep.Flow {
			b.WriteString("\n  - ")
			b.WriteString(s.Feature)
		}
	}
	b.WriteString("\n\nDescribe what fires when this entry point runs, in one sentence.")
	return b.String()
}

func triggerDescription(ep schema.EntryPoint) string {
	switch ep.Kind {
	case schema.EntryPointKindHTTP:
		return ep.Trigger.Method + " " + ep.Trigger.Path
	case schema.EntryPointKindCLI:
		return "command " + ep.Trigger.Command
	case schema.EntryPointKindCron:
		return "cron " + ep.Trigger.Schedule
	case schema.EntryPointKindQueue:
		return "queue " + ep.Trigger.Queue
	case schema.EntryPointKindEventConsumer:
		return "event " + ep.Trigger.Event
	}
	return ep.ID
}

// extractJSON strips ```json fences and other common prose around the
// JSON body — the importer's existing extractJSON helper has the same
// job; duplicated here so this package stays self-contained.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			return s[i : j+1]
		}
	}
	return s
}
