package ui

// @module-feature ui

import (
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/blocks"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/store"
)

// The block map's assurance lens (rnd/0010, A4). Every colour it draws is a
// kernel field: the audit classifies each promise, this file joins those
// classes to the blocks that hold the promises, takes the weakest by the
// audit's own order, and names the style each class is drawn in. The page
// maps a style name to a colour and computes nothing.

// Lens styles. Only StylePass (break_tested) and StylePassWeak (exercised)
// are drawn in the positive colour.
const (
	StylePass             = "pass"
	StylePassWeak         = "pass-weak"
	StyleFake             = "fake"
	StyleUnenforced       = "unenforced"
	StyleUnverified       = "unverified"
	StyleNotDemonstrated  = "not-demonstrated"
	StyleDemonstratedOnly = "demonstrated-only"
	// StyleUnknown is the hatched grey of unknown, of a promise the audit
	// has no row for, and of any class the page does not know.
	StyleUnknown = "unknown"
	// StyleNone is a block that holds no promise: nothing to assure.
	StyleNone = "none"
)

// LensKeyNone is the legend key of the blocks that hold no promise.
const LensKeyNone = "none"

// MapData is what the block map reads: the stored graph and, added beside
// it, the assurance lens.
type MapData struct {
	store.Graph
	Assurance AssuranceLens `json:"assurance"`
	// Refresh says whether the graph was rebuilt from the workspace's inputs
	// for this page; nil when nothing tried.
	Refresh *MapRefresh `json:"refresh,omitempty"`
}

// MapRefresh is the rebuild of the block graph that preceded a page, as
// lattice refresh would have run it.
type MapRefresh struct {
	// Rebuilt is true when the stored graph differed from the one its inputs
	// give, and the page draws the rebuilt graph; Summary says what changed.
	Rebuilt bool   `json:"rebuilt"`
	Summary string `json:"summary,omitempty"`
	// Saved is true when the rebuilt graph was written to the store.
	Saved bool `json:"saved"`
	// Note says, when set, why the page does not draw a graph rebuilt from
	// the current inputs, or why it was not saved, and how to fix it.
	Note     string   `json:"note,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// AssuranceLens is the audit as the map draws it.
type AssuranceLens struct {
	// Contract is the audit contract the classes come from.
	Contract string `json:"contract"`
	Commit   string `json:"commit,omitempty"`
	Dirty    bool   `json:"dirty,omitempty"`
	// Available is false when the audit could not be computed; Note says
	// why, and every promise on the map then reads unknown.
	Available bool     `json:"available"`
	Note      string   `json:"note,omitempty"`
	Inputs    []string `json:"inputs,omitempty"` // the evidence the audit read, one line each
	// Order is the audit's classes, weakest first. A block holding several
	// promises takes the first of their classes in this order.
	Order []string `json:"order"`
	// Styles names the style of each class.
	Styles map[string]string `json:"styles"`
	// Fallback is the style of anything the page cannot place.
	Fallback string    `json:"fallback"`
	Legend   []LensKey `json:"legend"`
	// Promises are keyed <feature>:INV-n.
	Promises map[string]LensPromise `json:"promises"`
	// Nodes has one entry per block of the graph.
	Nodes map[string]LensNode `json:"nodes"`
	// Unplaced are promises the audit classified that no block on the map
	// holds (a stored graph older than the features).
	Unplaced []string `json:"unplaced,omitempty"`
}

// LensKey is one legend entry: a class, or LensKeyNone.
type LensKey struct {
	Key     string `json:"key"`
	Style   string `json:"style"`
	Label   string `json:"label"`
	Meaning string `json:"meaning"`
	// Count is the promises of the class; for LensKeyNone, the blocks that
	// hold no promise. Unit says which.
	Count int    `json:"count"`
	Unit  string `json:"unit"`
}

// LensPromise is one promise as the lens shows it, read from its audit row.
type LensPromise struct {
	Class     string `json:"class"`
	Kind      string `json:"kind,omitempty"`
	Reason    string `json:"reason"`
	Statement string `json:"statement,omitempty"`
	Block     string `json:"block,omitempty"` // the block that stands for it
	// Missing is true when the audit has no row for a promise block on the
	// map: it reads unknown.
	Missing bool `json:"missing,omitempty"`
	// Cases counts the promise's acceptance cases by the coverage the audit
	// computed.
	Cases map[string]int `json:"cases,omitempty"`
}

// LensNode is one block: the weakest class of the promises it holds (its
// own and those of the blocks inside it) and the count per class.
type LensNode struct {
	Class    string         `json:"class,omitempty"` // empty when the block holds no promise
	Style    string         `json:"style"`
	Counts   map[string]int `json:"counts,omitempty"`
	Promises []string       `json:"promises,omitempty"` // weakest first
}

// lensText is the label and plain meaning of each legend key.
var lensText = map[string][2]string{
	audit.ClassFake:             {"Fake", "a verifier passes with the code broken, or never runs it"},
	audit.ClassUnenforced:       {"Unenforced", "no code is anchored as the enforcer"},
	audit.ClassUnverified:       {"Unverified", "an enforcer, but no test is anchored as its verifier"},
	audit.ClassNotDemonstrated:  {"Not demonstrated", "no passing result is ingested, or one is red"},
	audit.ClassUnknown:          {"Unknown", "nothing decides it: evidence missing, a partial run, or no audit row"},
	audit.ClassDemonstratedOnly: {"Demonstrated only", "a test passed; nothing shows it runs the enforcer"},
	audit.ClassExercised:        {"Exercised", "a passing test ran the enforcer; no break shows it would notice"},
	audit.ClassBreakTested:      {"Break-tested", "breaking the enforcer made its tests fail"},
	LensKeyNone:                 {"No promise", "the block holds no invariant"},
}

// LensStyle names the style a class is drawn in. Only the audit's positive
// classes get a pass style, and only break_tested the full one; any other
// value, one the audit does not send included, is drawn unknown.
// @enforces ui:INV-2
func LensStyle(class string) string {
	if audit.Positive(class) {
		if class == audit.ClassBreakTested {
			return StylePass
		}
		return StylePassWeak
	}
	switch class {
	case audit.ClassFake:
		return StyleFake
	case audit.ClassUnenforced:
		return StyleUnenforced
	case audit.ClassUnverified:
		return StyleUnverified
	case audit.ClassNotDemonstrated:
		return StyleNotDemonstrated
	case audit.ClassDemonstratedOnly:
		return StyleDemonstratedOnly
	}
	return StyleUnknown
}

// lensRank is each class's place in the audit's order, weakest first.
func lensRank() map[string]int {
	r := make(map[string]int, len(audit.Classes))
	for i, c := range audit.Classes {
		r[c] = i
	}
	return r
}

// Assurance computes the lens over g from rep, the audit of the workspace.
// rep is nil when the audit could not be computed (note says why): every
// promise block on the map then reads unknown. A promise block the audit has
// no row for reads unknown too; it is never dropped and never a pass.
// @enforces ui:INV-2
func Assurance(g store.Graph, rep *audit.Report, note string) AssuranceLens {
	rank := lensRank()
	l := AssuranceLens{Contract: audit.Schema, Available: rep != nil, Note: note,
		Order: append([]string(nil), audit.Classes...), Styles: map[string]string{}, Fallback: StyleUnknown,
		Promises: map[string]LensPromise{}, Nodes: make(map[string]LensNode, len(g.Blocks))}
	for _, c := range audit.Classes {
		l.Styles[c] = LensStyle(c)
	}

	byID := make(map[string]schema.Block, len(g.Blocks))
	for _, b := range g.Blocks {
		byID[b.ID] = b
	}
	held := map[string]map[string]bool{} // block id -> promises it holds
	hold := func(id, ref string) {
		if held[id] == nil {
			held[id] = map[string]bool{}
		}
		held[id][ref] = true
	}
	// a promise counts on its block and on every block that contains it
	holdUp := func(id, ref string) {
		seen := map[string]bool{}
		for cur := id; cur != "" && !seen[cur]; cur = byID[cur].Parent {
			if _, ok := byID[cur]; !ok {
				break
			}
			seen[cur] = true
			hold(cur, ref)
		}
	}

	placed := map[string]bool{}
	if rep != nil {
		l.Commit, l.Dirty, l.Inputs = rep.Commit, rep.Dirty, rep.InputLines()
		for _, row := range rep.Rows {
			p := LensPromise{Class: row.Class, Kind: row.Kind, Reason: row.Reason, Statement: row.Statement}
			if _, known := rank[p.Class]; !known {
				p.Class = audit.ClassUnknown
			}
			for _, c := range row.Cases {
				if p.Cases == nil {
					p.Cases = map[string]int{}
				}
				p.Cases[c.Coverage]++
			}
			if id := blocks.InvariantBlockID(row.Feature, row.Invariant); byID[id].ID != "" {
				p.Block = id
				holdUp(id, row.Promise)
				placed[row.Promise] = true
			}
			if fid := blocks.FeatureBlockID(row.Feature); byID[fid].ID != "" {
				holdUp(fid, row.Promise)
				placed[row.Promise] = true
			}
			l.Promises[row.Promise] = p
			if !placed[row.Promise] {
				l.Unplaced = append(l.Unplaced, row.Promise)
			}
		}
	}

	// promise blocks the audit has no row for
	for _, b := range g.Blocks {
		if b.Kind != "invariant" || !strings.HasPrefix(b.ID, "inv:") || len(held[b.ID]) > 0 {
			continue
		}
		ref := b.ID
		if f, ok := strings.CutPrefix(b.Parent, "feat:"); ok && b.Title != "" {
			ref = f + ":" + b.Title
		}
		why := "the audit has no row for this promise; the stored graph may be older than the features (lattice refresh)"
		if rep == nil {
			why = "the audit was not computed"
			if note != "" {
				why += ": " + note
			}
		}
		l.Promises[ref] = LensPromise{Class: audit.ClassUnknown, Reason: why, Block: b.ID, Missing: true}
		holdUp(b.ID, ref)
	}

	counts := map[string]int{}
	for _, p := range l.Promises {
		counts[p.Class]++
	}
	for _, b := range g.Blocks {
		refs := held[b.ID]
		if len(refs) == 0 {
			l.Nodes[b.ID] = LensNode{Style: StyleNone}
			counts[LensKeyNone]++
			continue
		}
		n := LensNode{Counts: map[string]int{}}
		for ref := range refs {
			c := l.Promises[ref].Class
			n.Counts[c]++
			n.Promises = append(n.Promises, ref)
		}
		sort.Slice(n.Promises, func(i, j int) bool {
			a, b := l.Promises[n.Promises[i]].Class, l.Promises[n.Promises[j]].Class
			if rank[a] != rank[b] {
				return rank[a] < rank[b]
			}
			return n.Promises[i] < n.Promises[j]
		})
		n.Class = l.Promises[n.Promises[0]].Class // the weakest
		n.Style = LensStyle(n.Class)
		l.Nodes[b.ID] = n
	}

	for _, k := range append(append([]string(nil), audit.Classes...), LensKeyNone) {
		style := StyleNone
		if k != LensKeyNone {
			style = LensStyle(k)
		}
		t, unit := lensText[k], "promises"
		if k == LensKeyNone {
			unit = "blocks"
		}
		l.Legend = append(l.Legend, LensKey{Key: k, Style: style, Label: t[0], Meaning: t[1], Count: counts[k], Unit: unit})
	}
	sort.Strings(l.Unplaced)
	return l
}
