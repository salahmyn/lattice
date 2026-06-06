// Package featurespec renders a feature manifest as the AMA-shaped
// `.ai-spec.md` — a ≤500-word markdown summary that an AI agent can
// load alongside the feature's folder to act without re-reading
// the manifest YAML.
//
// The output is fully deterministic: every section is derived from
// the manifest fields (purpose, surface[], errors[], invariants[],
// capabilities[]). An LLM polish pass is supported via a separate
// helper, but the deterministic form always works — the validator
// guarantees the schema fields exist.
package featurespec

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// WordCap is the AMA spec §3 ceiling for `.ai-spec.md`. The
// FEATURE_SPEC_TOO_LARGE rule fires when a generated spec exceeds it.
const WordCap = 500

// Render returns the AMA-shaped markdown for the given feature.
// Idempotent — same manifest always produces byte-identical output.
//
// Sections that have no content are omitted entirely (no "Invariants:
// none" filler), so the spec stays compact for small features and
// only grows as the manifest does.
func Render(m schema.Manifest) string {
	var b strings.Builder

	// Title + tagline.
	fmt.Fprintf(&b, "# %s\n\n", m.ID)
	if purpose := strings.TrimSpace(m.Purpose); purpose != "" {
		fmt.Fprintf(&b, "> %s\n\n", purpose)
	}

	// Inputs — surface[] http/webhook/scheduled triggers + CLI
	// commands. The journey/EP axis is separate but the manifest's
	// surface list is the canonical "what calls this feature" view.
	if inputs := renderInputs(m); inputs != "" {
		b.WriteString("## Inputs\n")
		b.WriteString(inputs)
		b.WriteString("\n")
	}

	// Outputs — http response schemas + emitted events.
	if outputs := renderOutputs(m); outputs != "" {
		b.WriteString("## Outputs\n")
		b.WriteString(outputs)
		b.WriteString("\n")
	}

	// System side effects — events consumed/emitted that change state,
	// scheduled jobs. AMA's "mutates / emits / reads" framing.
	if effects := renderSideEffects(m); effects != "" {
		b.WriteString("## System Side Effects\n")
		b.WriteString(effects)
		b.WriteString("\n")
	}

	// Invariants — the contract a refactor must preserve.
	if len(m.Invariants) > 0 {
		b.WriteString("## Invariants\n")
		for _, inv := range m.Invariants {
			fmt.Fprintf(&b, "- %s: %s\n", inv.ID, oneLine(inv.Statement))
		}
		b.WriteString("\n")
	}

	// Errors — the response contract a caller can observe.
	if len(m.Errors) > 0 {
		b.WriteString("## Errors\n")
		for _, e := range m.Errors {
			line := "- " + e.Code
			if e.Status > 0 {
				line += fmt.Sprintf(" (%d)", e.Status)
			}
			if e.Description != "" {
				line += " — " + oneLine(e.Description)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	// Capabilities — the named behaviours, abbreviated. Just the
	// summary, not the rules (rules can blow the word budget on
	// medium features).
	if len(m.Capabilities) > 0 {
		b.WriteString("## Capabilities\n")
		for _, c := range m.Capabilities {
			kind := ""
			if k := c.EffectiveKind(); k != schema.CapabilityMixed {
				kind = fmt.Sprintf(" [%s]", k)
			}
			fmt.Fprintf(&b, "- %s%s — %s\n", c.ID, kind, oneLine(c.Summary))
		}
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n") + "\n"
}

// WordCount returns the spec's word count, which the
// FEATURE_SPEC_TOO_LARGE rule compares against WordCap.
// Words are whitespace-separated runs; markdown punctuation
// is preserved so this matches what an LLM token-counts.
func WordCount(spec string) int {
	return len(strings.Fields(spec))
}

// renderInputs flattens the surface[] entries that represent inputs
// (http, webhook_receive, scheduled). One bullet per entry, with
// the most identifying field first.
func renderInputs(m schema.Manifest) string {
	var lines []string
	for _, s := range m.Surface {
		switch s.Type {
		case schema.SurfaceHTTP, schema.SurfaceWebhookReceive:
			method := s.Method
			if method == "" {
				method = "?"
			}
			line := fmt.Sprintf("- %s %s", method, s.Path)
			if s.RequestSchema != "" {
				line += " → " + s.RequestSchema
			}
			lines = append(lines, line)
		case schema.SurfaceScheduled:
			lines = append(lines, fmt.Sprintf("- cron %s — %s", s.Schedule, s.Job))
		case schema.SurfaceEventConsume:
			lines = append(lines, fmt.Sprintf("- event %s (consume)", s.Name))
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + nl(lines)
}

// renderOutputs covers http response schemas + emitted events.
func renderOutputs(m schema.Manifest) string {
	var lines []string
	for _, s := range m.Surface {
		switch s.Type {
		case schema.SurfaceHTTP:
			if s.ResponseSchema != "" {
				lines = append(lines, fmt.Sprintf("- %s %s → %s", s.Method, s.Path, s.ResponseSchema))
			}
		case schema.SurfaceEventEmit:
			line := fmt.Sprintf("- event %s (emit)", s.Name)
			if s.Semantics != "" {
				line += " — " + s.Semantics
			}
			lines = append(lines, line)
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + nl(lines)
}

// renderSideEffects flattens the surface entries that change state.
// We separate "emits" (event publishers), "consumes" (event listeners),
// and "scheduled" (cron-driven background work) so the AMA reader
// gets a clear ledger.
func renderSideEffects(m schema.Manifest) string {
	var emits, consumes, scheduled []string
	for _, s := range m.Surface {
		switch s.Type {
		case schema.SurfaceEventEmit:
			emits = append(emits, s.Name)
		case schema.SurfaceEventConsume:
			consumes = append(consumes, s.Name)
		case schema.SurfaceScheduled:
			scheduled = append(scheduled, s.Schedule+" "+s.Job)
		}
	}
	sort.Strings(emits)
	sort.Strings(consumes)
	sort.Strings(scheduled)
	var b strings.Builder
	if len(emits) > 0 {
		fmt.Fprintf(&b, "- emits: %s\n", strings.Join(emits, ", "))
	}
	if len(consumes) > 0 {
		fmt.Fprintf(&b, "- consumes: %s\n", strings.Join(consumes, ", "))
	}
	if len(scheduled) > 0 {
		fmt.Fprintf(&b, "- scheduled: %s\n", strings.Join(scheduled, ", "))
	}
	return b.String()
}

// oneLine collapses any newlines/extra whitespace in s so a
// markdown bullet stays on one line. The full multi-line text is
// available in the source manifest if a reader wants it.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// nl returns "\n" when there are items, "" when there are none —
// keeps the trailing-newline shape consistent without empty
// "## Inputs\n\n" sections.
func nl(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return "\n"
}
