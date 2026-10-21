package blocks

import (
	"regexp"
	"strings"
)

// compileID compiles a block id pattern: "*" matches within one dot-separated
// segment, "**" across segments, "?" one character other than a dot. An empty
// pattern compiles to nil (no filter).
func compileID(p string) *regexp.Regexp {
	if p == "" {
		return nil
	}
	var b strings.Builder
	b.WriteString("^")
	rs := []rune(p)
	for i := 0; i < len(rs); i++ {
		switch {
		case rs[i] == '*' && i+1 < len(rs) && rs[i+1] == '*':
			b.WriteString(".*")
			i++
		case rs[i] == '*':
			b.WriteString(`[^.]*`)
		case rs[i] == '?':
			b.WriteString(`[^.]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(rs[i])))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

func isIDPattern(s string) bool { return strings.ContainsAny(s, "*?") }

// expand turns an edge end into block ids: a pattern matches every block not
// excluded; a plain id resolves through alias judgments.
func (g *graph) expand(end string, exclude []string) []string {
	if !isIDPattern(end) {
		return []string{g.resolve(end)}
	}
	re := compileID(end)
	var out []string
	for _, id := range sortedKeys(g.blocks) {
		if re.MatchString(id) && !excluded(id, exclude) {
			out = append(out, id)
		}
	}
	return out
}

func excluded(id string, exclude []string) bool {
	for _, x := range exclude {
		if x == id || (isIDPattern(x) && compileID(x).MatchString(id)) {
			return true
		}
	}
	return false
}

// stub replaced in Task 2
func (g *graph) applyConfig(Judgment, JudgmentEntry) {}
