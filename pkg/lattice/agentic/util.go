package agentic

import "strings"

// extractJSON pulls the first JSON object or array out of an LLM reply,
// tolerating markdown code fences and surrounding prose.
func extractJSON(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.Index(text, "```"); i >= 0 {
		rest := text[i+3:]
		rest = strings.TrimPrefix(rest, "json")
		rest = strings.TrimPrefix(rest, "yaml")
		if j := strings.Index(rest, "```"); j >= 0 {
			return strings.TrimSpace(rest[:j])
		}
	}
	start := strings.IndexAny(text, "{[")
	if start < 0 {
		return text
	}
	open := text[start]
	close := byte('}')
	if open == '[' {
		close = ']'
	}
	depth := 0
	for i := start; i < len(text); i++ {
		switch text[i] {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return text[start : i+1]
			}
		}
	}
	return text[start:]
}

// extractYAML pulls YAML content out of an LLM reply, stripping code fences.
func extractYAML(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.Index(text, "```"); i >= 0 {
		rest := text[i+3:]
		rest = strings.TrimPrefix(rest, "yaml")
		rest = strings.TrimPrefix(rest, "yml")
		if j := strings.Index(rest, "```"); j >= 0 {
			return strings.TrimSpace(rest[:j])
		}
	}
	return text
}
