package impact

// @module-feature impact

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// NoteStatementUnchanged is the code of the note on a promise whose enforcer
// changed while its statement and tier did not.
const NoteStatementUnchanged = "STATEMENT_UNCHANGED"

// Note is an informational reading of one touched promise. It is advice for
// the reviewer: it never changes a verdict, a gap or a merge rule.
type Note struct {
	Code      string `json:"code"`
	Promise   string `json:"promise"`
	Statement string `json:"statement,omitempty"`
	// Enforcers are the changed enforcer symbols the note is about.
	Enforcers []string `json:"enforcers,omitempty"`
	Text      string   `json:"text"`
	Next      string   `json:"next,omitempty"`
}

var criterionField = regexp.MustCompile(`^success_criteria\[([^\]]+)\](?:\.(.*))?$`)

// statementNotes lists the promises whose enforcer's own lines changed while
// the promise's statement and tier did not: the written promise may no longer
// describe the code. A promise the change adds (absent at base), one whose
// statement changed, and one whose tier or mapping a BRD edit touched get no
// note; other rules read those edits. A removed enforcer, or a symbol that
// stopped enforcing the promise, is not a changed enforcer here: removed
// enforcement reports it.
// @enforces impact:INV-5
func statementNotes(head, base []schema.Manifest, brds []schema.BRD, md ManifestDiff, cc codeResult) []Note {
	lost := map[string]bool{}
	for _, r := range cc.removed {
		lost[r.Symbol+"\x00"+r.Invariant] = true
	}
	enforcers := map[string][]string{}
	for _, cs := range cc.symbols {
		if cs.Test || (cs.Change != ChangeModified && cs.Change != ChangeAdded) {
			continue
		}
		for _, ref := range cs.Enforces {
			if lost[cs.Symbol+"\x00"+ref] || contains(enforcers[ref], cs.Symbol) {
				continue
			}
			enforcers[ref] = append(enforcers[ref], cs.Symbol)
		}
	}
	if len(enforcers) == 0 {
		return nil
	}
	headInv, baseInv := statements(head), statements(base)
	tierEdited := tierEdits(brds, md)

	var out []Note
	for _, ref := range sortedKeys(enforcers) {
		hs, inHead := headInv[ref]
		bs, inBase := baseInv[ref]
		if !inHead || !inBase || hs != bs || tierEdited[ref] {
			continue
		}
		syms := enforcers[ref]
		sort.Strings(syms)
		out = append(out, Note{
			Code: NoteStatementUnchanged, Promise: ref, Statement: schema.InlineText(hs), Enforcers: syms,
			Text: fmt.Sprintf("the enforcer of %s changed and its statement did not: re-read the statement and confirm it still holds", ref),
			Next: fmt.Sprintf("read the statement of %s against %s; if it no longer describes the code, edit the statement in the feature file",
				ref, strings.Join(syms, ", ")),
		})
	}
	return out
}

// statements maps each invariant ref of the features to its statement.
func statements(features []schema.Manifest) map[string]string {
	out := map[string]string{}
	for _, f := range features {
		for _, inv := range f.Invariants {
			out[f.ID+":"+inv.ID] = inv.Statement
		}
	}
	return out
}

// tierEdits lists the invariant refs whose tier this change may have moved:
// a BRD criterion mapping to one was added, or its tier, its mapping or the
// whole criterion changed.
func tierEdits(brds []schema.BRD, md ManifestDiff) map[string]bool {
	out := map[string]bool{}
	headMaps := map[string][]string{}
	byID := map[string]schema.BRD{}
	for _, b := range brds {
		byID[b.ID] = b
		for _, sc := range b.SuccessCriteria {
			headMaps[b.ID+"\x00"+sc.ID] = normRefs(sc.MapsToInvariant, "")
		}
	}
	for _, a := range md.BRDs.Added {
		for _, sc := range byID[a.ID].SuccessCriteria {
			for _, r := range normRefs(sc.MapsToInvariant, "") {
				out[r] = true
			}
		}
	}
	for _, mc := range append(append([]ManifestChange{}, md.BRDs.Changed...), md.BRDs.Renamed...) {
		for _, f := range mc.Fields {
			m := criterionField.FindStringSubmatch(f.Field)
			if m == nil {
				continue
			}
			switch m[2] {
			case "tier", "maps_to_invariant", "":
			default:
				continue
			}
			for _, r := range headMaps[mc.ID+"\x00"+m[1]] {
				out[r] = true
			}
			for _, v := range []interface{}{f.Before, f.After} {
				for _, r := range refsIn(v) {
					out[r] = true
				}
			}
		}
	}
	return out
}

// refsIn reads invariant refs out of a manifest diff value: one ref, a list,
// or a whole criterion.
func refsIn(v interface{}) []string {
	switch x := v.(type) {
	case string:
		return normRefs([]string{x}, "")
	case []interface{}:
		var out []string
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return normRefs(out, "")
	case map[string]interface{}:
		return refsIn(x["maps_to_invariant"])
	}
	return nil
}
