package importer

import (
	"regexp"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// MatchCapabilities assigns each FQN to the capability whose tokens best
// overlap with the symbol's name. It is the v0.2.1 fix for the dogfood
// result where sidecar mode attached symbols to features but never to
// capabilities — leaving 172 UNIMPLEMENTED_CAPABILITY warnings even
// though method names like `toMail` and `toDatabase` obviously implement
// the `mail_notification` and `database_notification` capabilities the
// LLM had just drafted.
//
// Returns a map cap_id -> sorted FQNs; symbols that don't pass the
// threshold for any capability are left out (they remain feature-only
// links). Token weighting uses a poor-man's IDF — tokens that appear in
// many capabilities (like "notification" across every cap of the same
// feature) carry less signal than tokens unique to one cap.
func MatchCapabilities(symbols []string, capabilities []schema.Capability) map[string][]string {
	if len(capabilities) == 0 || len(symbols) == 0 {
		return nil
	}
	capTokens := make([]map[string]bool, len(capabilities))
	docFreq := map[string]int{}
	for i, c := range capabilities {
		capTokens[i] = capabilityTokens(c)
		for t := range capTokens[i] {
			docFreq[t]++
		}
	}
	weight := func(t string) float64 {
		// 1.0 when unique; ~0.3 when in every cap.
		return 1.0 / float64(docFreq[t])
	}
	out := map[string][]string{}
	for _, fqn := range symbols {
		sym := symbolTokens(fqn)
		if len(sym) == 0 {
			continue
		}
		bestIdx, bestScore := -1, 0.0
		for i, ct := range capTokens {
			score := weightedOverlap(sym, ct, weight)
			if score > bestScore {
				bestScore, bestIdx = score, i
			}
		}
		// Threshold: at least half the symbol's distinguishing tokens
		// must match (after weighting). Too low and unrelated methods
		// get linked to the wrong cap; too high and obvious matches
		// like toMail/mail miss.
		if bestIdx < 0 || bestScore < 0.5 {
			continue
		}
		capID := capabilities[bestIdx].ID
		out[capID] = append(out[capID], fqn)
	}
	for id := range out {
		sort.Strings(out[id])
	}
	return out
}

// weightedOverlap is sum(weight[t] for t in intersect) / |sym tokens|.
// The numerator uses IDF so tokens shared across every cap (e.g.
// "notification" across mail/database/array/channel capabilities of
// webhook.failed_notification) don't dominate distinguishing tokens.
func weightedOverlap(sym, cap map[string]bool, w func(string) float64) float64 {
	if len(sym) == 0 {
		return 0
	}
	score := 0.0
	for t := range sym {
		if cap[t] {
			score += w(t)
		}
	}
	return score / float64(len(sym))
}

// symbolTokens extracts distinguishing tokens from a symbol FQN. For
// method-like symbols (FQN contains ::) only the method name is
// tokenised — the class name appears in every method on the class and
// would inflate every score uniformly, defeating the matcher. For
// non-method symbols all FQN segments are used.
func symbolTokens(fqn string) map[string]bool {
	last := fqn
	if i := strings.LastIndex(fqn, "::"); i >= 0 {
		last = fqn[i+2:]
	} else {
		// Take the last path component (handles Class, Namespace\Class).
		for _, sep := range []string{"\\", "/", "."} {
			if i := strings.LastIndex(last, sep); i >= 0 {
				last = last[i+len(sep):]
			}
		}
	}
	return distinctTokens(splitIdentifier(last))
}

// capabilityTokens are drawn from the capability's id, summary, and rules
// — the LLM puts the discriminating vocabulary in those fields.
func capabilityTokens(c schema.Capability) map[string]bool {
	parts := []string{c.ID, string(c.Summary)}
	for _, r := range c.Rules {
		parts = append(parts, r)
	}
	return distinctTokens(tokenize(strings.Join(parts, " ")))
}

// splitIdentifier handles camelCase, snake_case, and dot-/colon-separated
// names: "toMailNotification" -> [to mail notification]. The two passes
// catch both lower→Upper and acronym→Word boundaries (HTTPClient).
var camelLowerUpper = regexp.MustCompile(`([a-z0-9])([A-Z])`)
var camelAcronym = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
var nonWord = regexp.MustCompile(`[^a-zA-Z0-9]+`)

func splitIdentifier(s string) []string {
	s = camelLowerUpper.ReplaceAllString(s, "${1}_${2}")
	s = camelAcronym.ReplaceAllString(s, "${1}_${2}")
	parts := nonWord.Split(s, -1)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, strings.ToLower(p))
		}
	}
	return out
}

// tokenize lowercases and splits arbitrary prose on non-word characters.
// Used for capability summaries and rules.
func tokenize(s string) []string {
	parts := nonWord.Split(s, -1)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, strings.ToLower(p))
		}
	}
	return out
}

// stopwords are very common English/idiomatic-method words that carry no
// signal for capability matching. `via` is intentionally kept — it's a
// meaningful Laravel notification channel method name and reviewers
// expect it to bind to channel_selection / channels caps.
var stopwords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "of": true,
	"on": true, "in": true, "to": true, "for": true, "with": true, "by": true,
	"is": true, "be": true, "as": true, "at": true, "it": true, "this": true,
	"that": true, "from": true, "into": true, "must": true, "may": true,
	"can": true, "will": true, "should": true, "get": true, "set": true,
}

func distinctTokens(parts []string) map[string]bool {
	out := map[string]bool{}
	for _, p := range parts {
		if len(p) < 2 || stopwords[p] {
			continue
		}
		out[p] = true
	}
	return out
}
