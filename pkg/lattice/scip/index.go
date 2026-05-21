package scip

import (
	"os"
	"sort"
	"strings"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

// definitionRole is the SCIP symbol-role bit marking an occurrence as a
// definition.
const definitionRole = int32(scippb.SymbolRole_Definition)

// Reference is one code location touching a symbol.
type Reference struct {
	File         string `json:"file"`
	Line         int    `json:"line"`
	IsDefinition bool   `json:"is_definition"`
}

// BlastRadius is the code-level impact set for a queried symbol.
type BlastRadius struct {
	Query       string      `json:"query"`
	Resolved    bool        `json:"resolved"`
	Definitions []Reference `json:"definitions,omitempty"`
	References  []Reference `json:"references,omitempty"`
	Files       []string    `json:"files,omitempty"`
}

// Corpus is one or more parsed SCIP indexes queried as a unit.
type Corpus struct {
	indexes []*scippb.Index
}

// Load parses every readable SCIP index from the given paths into a corpus.
// Missing files are skipped — a repo may not have indexed every language.
func Load(paths ...string) (*Corpus, error) {
	c := &Corpus{}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return c, err
		}
		idx := &scippb.Index{}
		if err := proto.Unmarshal(data, idx); err != nil {
			return c, err
		}
		c.indexes = append(c.indexes, idx)
	}
	return c, nil
}

// Empty reports whether the corpus parsed no indexes.
func (c *Corpus) Empty() bool { return len(c.indexes) == 0 }

// BlastRadius returns the definitions and references of the symbol whose SCIP
// descriptor matches the query. Matching is by descriptor substring: adapter
// FQNs and SCIP symbols use different schemes, so the simple name is the
// reliable join key.
func (c *Corpus) BlastRadius(query string) BlastRadius {
	br := BlastRadius{Query: query}
	needle := simpleName(query)
	if needle == "" {
		return br
	}

	fileSet := map[string]bool{}
	for _, idx := range c.indexes {
		for _, doc := range idx.Documents {
			for _, occ := range doc.Occurrences {
				if !symbolMatches(occ.Symbol, needle) {
					continue
				}
				br.Resolved = true
				ref := Reference{
					File:         doc.RelativePath,
					Line:         occurrenceLine(occ),
					IsDefinition: occ.SymbolRoles&definitionRole != 0,
				}
				fileSet[doc.RelativePath] = true
				if ref.IsDefinition {
					br.Definitions = append(br.Definitions, ref)
				} else {
					br.References = append(br.References, ref)
				}
			}
		}
	}
	for f := range fileSet {
		br.Files = append(br.Files, f)
	}
	sort.Strings(br.Files)
	sortRefs(br.Definitions)
	sortRefs(br.References)
	return br
}

// occurrenceLine returns the 1-based start line of an occurrence.
func occurrenceLine(occ *scippb.Occurrence) int {
	if len(occ.Range) == 0 {
		return 0
	}
	return int(occ.Range[0]) + 1
}

// symbolMatches reports whether a SCIP symbol string carries the given simple
// name as a descriptor component.
func symbolMatches(scipSymbol, simpleName string) bool {
	if scipSymbol == "" || strings.HasPrefix(scipSymbol, "local ") {
		return false
	}
	for _, sep := range []string{"/", ".", "#", ":", "(", ")", "`"} {
		scipSymbol = strings.ReplaceAll(scipSymbol, sep, " ")
	}
	for _, part := range strings.Fields(scipSymbol) {
		if part == simpleName {
			return true
		}
	}
	return false
}

// simpleName returns the last component of a dotted/scoped FQN.
func simpleName(fqn string) string {
	for _, sep := range []string{"::", ".", "\\", "/", "#"} {
		if i := strings.LastIndex(fqn, sep); i >= 0 {
			fqn = fqn[i+len(sep):]
		}
	}
	return strings.TrimSpace(fqn)
}

func sortRefs(refs []Reference) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].File != refs[j].File {
			return refs[i].File < refs[j].File
		}
		return refs[i].Line < refs[j].Line
	})
}
