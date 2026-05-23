package agentic

import (
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/config"
)

// ToneContract renders a config.Tone into the prose that prepends every
// LLM system prompt. Returns an empty string when no tone is configured —
// the historical engineering-leaning voice is the zero value.
//
// Threading: the labeler, the narrator, and the annotation suggester all
// call ToneContract(cfg) and prepend the result to their base system
// prompt. One config knob, one place to render it, all agentic capabilities
// shift voice together.
func ToneContract(t config.Tone) string {
	var lines []string
	switch strings.ToLower(strings.TrimSpace(t.Audience)) {
	case "business":
		lines = append(lines,
			"Audience: business stakeholders and non-technical readers.",
			"Lead with what the user can do, not how the system is built.",
			"Prefer plain English over software jargon. Where a technical term is unavoidable, define it inline.")
	case "product":
		lines = append(lines,
			"Audience: product managers and designers.",
			"Frame each feature as a capability of the product, written for a roadmap or release-notes reader.",
			"Use functional language; light technical vocabulary is fine but never code-level detail.")
	case "engineering", "engineer":
		lines = append(lines,
			"Audience: senior engineers reviewing a codebase.",
			"Precision over plain language. Use accurate technical vocabulary; cite framework idioms when they explain the behaviour.")
	case "mixed", "":
		// No-op: default voice.
	default:
		lines = append(lines, "Audience: "+t.Audience+".")
	}
	switch strings.ToLower(strings.TrimSpace(t.ReadingLevel)) {
	case "simple":
		lines = append(lines, "Reading level: keep sentences short. Aim for a 9th-grade reader.")
	case "intermediate":
		lines = append(lines, "Reading level: educated adult. Compound sentences are fine.")
	case "expert":
		lines = append(lines, "Reading level: domain expert. You may use precise technical terms without defining them.")
	}
	if t.AvoidJargon {
		lines = append(lines,
			"Avoid software jargon (factory, polymorphism, hydration, middleware, repository, observer, transformer). "+
				"When the underlying code uses one of these patterns, describe what it accomplishes — not what it is.")
	}
	if extra := strings.TrimSpace(t.ExtraInstructions); extra != "" {
		lines = append(lines, "Additional voice instructions:", extra)
	}
	if len(lines) == 0 {
		return ""
	}
	return "Voice and audience contract — apply to every prose field you produce:\n  - " +
		strings.Join(lines, "\n  - ") + "\n\n"
}
