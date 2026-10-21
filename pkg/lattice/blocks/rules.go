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

var (
	clauseSplit = regexp.MustCompile(`\s+(?:and|or)\s+`)
	operatorCut = regexp.MustCompile(`\s*(?:!=|<=|>=|=|<|>|\s)\s*`)
)

// configRefs returns the config ids a block's `when` clauses read.
func configRefs(when []string) []string {
	var out []string
	for _, clause := range when {
		for _, part := range clauseSplit.Split(clause, -1) {
			if cid := operatorCut.Split(strings.TrimSpace(part), 2)[0]; cid != "" {
				out = append(out, cid)
			}
		}
	}
	return out
}

// applyConfig adds a config rule's edges: from the producer to every catalogue
// block that reads a config of ConfigKind, or whose config_source has type
// ConfigSource and an allowed scope.
func (g *graph) applyConfig(j Judgment, e JudgmentEntry) {
	from := g.resolve(e.From)
	if g.blocks[from] == nil {
		g.warnf("judgment %s: edge end %q is not a block", j.ID, e.From)
		return
	}
	only, prov := compileID(e.Blocks), judgmentProvenance(j)
	for _, im := range g.sources {
		for _, id := range sortedKeys(im.nodes) {
			n, key := im.nodes[id], im.keys[id]
			if only != nil && !only.MatchString(key) {
				continue
			}
			if e.ConfigKind != "" {
				for _, cid := range configRefs(n.When) {
					if im.configKinds[cid] == e.ConfigKind {
						g.edge(from, key, e.Kind, cid, nil, prov)
					}
				}
			}
			if cs := n.ConfigSource; e.ConfigSource != "" && cs != nil && cs.Type == e.ConfigSource &&
				(len(e.Scopes) == 0 || contains(e.Scopes, cs.Scope)) {
				g.edge(from, key, e.Kind, e.Label, nil, prov)
			}
		}
	}
}
