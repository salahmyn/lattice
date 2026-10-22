package blocks

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

var brdStatuses = map[schema.BRDStatus]bool{schema.BRDDraft: true, schema.BRDProposed: true, schema.BRDApproved: true, schema.BRDSuperseded: true}

// ParseBRD reads one BRD record strictly.
func ParseBRD(data []byte, label string) (schema.BRD, error) {
	var b schema.BRD
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&b); err != nil {
		return schema.BRD{}, fmt.Errorf("%s: %w", label, err)
	}
	switch {
	case b.ID == "" || b.Title == "":
		return schema.BRD{}, fmt.Errorf("%s: a BRD needs an id and a title", label)
	case !brdStatuses[b.Status]:
		return schema.BRD{}, fmt.Errorf("%s: BRD %s has status %q (draft, proposed, approved, superseded)", label, b.ID, b.Status)
	}
	for i, s := range b.UserScenarios {
		if s.Narrative == "" || len(s.Then) == 0 {
			return schema.BRD{}, fmt.Errorf("%s: BRD %s scenario %d needs a narrative (its name) and at least one then", label, b.ID, i)
		}
	}
	return b, nil
}

// BusinessView is what the views page shows: each BRD with the facts the
// graph gives it, the blocks BRDs share, and the actors and systems.
type BusinessView struct {
	Features  []FeatureView `json:"features"`
	Shared    []SharedBlock `json:"shared"`
	Conflicts []BRDConflict `json:"conflicts"`
	Actors    []ActorView   `json:"actors"`
	Systems   []SystemView  `json:"systems"`
	Seams     []Seam        `json:"seams"`
}

// FeatureView is one BRD with its computed facts.
type FeatureView struct {
	BRD     schema.BRD `json:"brd"`
	Built   string     `json:"built"` // built | planned | mixed | unknown, from the blocks' status attrs
	Tests   Coverage   `json:"tests"`
	Areas   []string   `json:"areas"`   // the top-level areas its blocks sit in
	Missing []string   `json:"missing"` // block ids it names that the graph does not have
}

// Coverage counts the tests listed on the blocks (attrs tests_<name>, " | " separated).
type Coverage map[string]int

// SharedBlock is a block two or more BRDs touch.
type SharedBlock struct {
	Block    string   `json:"block"`
	Features []string `json:"features"`
}

// BRDConflict is a pair of BRDs that touch the same blocks: which one wins
// when both apply is for a person to decide.
type BRDConflict struct {
	A      string   `json:"a"`
	B      string   `json:"b"`
	Blocks []string `json:"blocks"`
}

// ActorView is an actor block and what it drives.
type ActorView struct {
	ID      string         `json:"id"`
	Title   string         `json:"title"`
	Desc    string         `json:"description,omitempty"`
	Edges   map[string]int `json:"edges"`   // outgoing edges by kind
	Reaches int            `json:"reaches"` // distinct blocks its edges reach
}

// SystemView is a top-level block and the blocks inside it.
type SystemView struct {
	ID     string         `json:"id"`
	Title  string         `json:"title"`
	Kind   string         `json:"kind"`
	Blocks int            `json:"blocks"`
	Tiers  map[string]int `json:"tiers,omitempty"`
	Kinds  map[string]int `json:"kinds,omitempty"`
}

// Seam counts the edges of one kind from one top-level block to another.
type Seam struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
	N    int    `json:"n"`
}

// Business computes the views page's facts from the graph and the BRDs.
func Business(bs []schema.Block, es []schema.BlockEdge, brds []schema.BRD) BusinessView {
	by := map[string]*schema.Block{}
	for i := range bs {
		by[bs[i].ID] = &bs[i]
	}
	top := func(id string) string { // the top-level block above id
		seen := map[string]bool{}
		for b := by[id]; b != nil && !seen[b.ID]; b = by[b.Parent] {
			seen[b.ID] = true
			if b.Parent == "" || by[b.Parent] == nil {
				return b.ID
			}
		}
		return ""
	}
	area := func(id string) string { // the block just below the top-level block
		prev := ""
		seen := map[string]bool{}
		for b := by[id]; b != nil && !seen[b.ID]; b = by[b.Parent] {
			seen[b.ID] = true
			if b.Parent == "" || by[b.Parent] == nil {
				if prev == "" {
					return b.ID
				}
				return prev
			}
			prev = b.ID
		}
		return prev
	}

	v := BusinessView{Features: []FeatureView{}, Shared: []SharedBlock{}, Conflicts: []BRDConflict{}}
	touched := map[string][]string{} // block -> BRD ids
	for _, b := range brds {
		f := FeatureView{BRD: b, Tests: Coverage{}, Areas: []string{}, Missing: []string{}}
		areas := map[string]bool{}
		built, planned, known := 0, 0, 0
		for _, id := range b.Blocks {
			blk := by[id]
			if blk == nil {
				f.Missing = append(f.Missing, id)
				continue
			}
			touched[id] = append(touched[id], b.ID)
			if a := area(id); a != "" {
				areas[a] = true
			}
			switch blk.Attrs["status"] {
			case "built":
				built++
				known++
			case "planned":
				planned++
				known++
			case "":
			default:
				known++
			}
			for k, val := range blk.Attrs {
				if name, ok := strings.CutPrefix(k, "tests_"); ok && val != "" {
					f.Tests[name] += len(strings.Split(val, " | "))
				}
			}
		}
		switch {
		case known == 0:
			f.Built = "unknown"
		case built == known:
			f.Built = "built"
		case planned == known:
			f.Built = "planned"
		default:
			f.Built = "mixed"
		}
		f.Areas = sortedKeys(areas)
		v.Features = append(v.Features, f)
	}
	pairs := map[[2]string][]string{}
	for _, id := range sortedKeys(touched) {
		fs := touched[id]
		if len(fs) < 2 {
			continue
		}
		v.Shared = append(v.Shared, SharedBlock{Block: id, Features: fs})
		for i := range fs {
			for j := i + 1; j < len(fs); j++ {
				k := [2]string{fs[i], fs[j]}
				pairs[k] = append(pairs[k], id)
			}
		}
	}
	keys := make([][2]string, 0, len(pairs))
	for k := range pairs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+"\x00"+keys[i][1] < keys[j][0]+"\x00"+keys[j][1] })
	for _, k := range keys {
		v.Conflicts = append(v.Conflicts, BRDConflict{A: k[0], B: k[1], Blocks: pairs[k]})
	}

	out := map[string]map[string]int{}
	reach := map[string]map[string]bool{}
	seams := map[[3]string]int{}
	for _, e := range es {
		if out[e.From] == nil {
			out[e.From], reach[e.From] = map[string]int{}, map[string]bool{}
		}
		out[e.From][e.Kind]++
		reach[e.From][e.To] = true
		if a, b := top(e.From), top(e.To); a != "" && b != "" && a != b {
			seams[[3]string{a, b, e.Kind}]++
		}
	}
	systems := map[string]*SystemView{}
	for _, b := range bs {
		if b.Kind == "actor" || b.Kind == "agent" {
			v.Actors = append(v.Actors, ActorView{ID: b.ID, Title: b.Title, Desc: b.Description, Edges: out[b.ID], Reaches: len(reach[b.ID])})
		}
		t := top(b.ID)
		if t == "" || b.ID == t || b.Initiative != "" {
			continue
		}
		s := systems[t]
		if s == nil {
			s = &SystemView{ID: t, Title: by[t].Title, Kind: by[t].Kind, Tiers: map[string]int{}, Kinds: map[string]int{}}
			systems[t] = s
		}
		s.Blocks++
		if b.Tier != "" {
			s.Tiers[b.Tier]++
		}
		s.Kinds[b.Kind]++
	}
	for _, k := range sortedKeys(systems) {
		v.Systems = append(v.Systems, *systems[k])
	}
	sort.Slice(v.Systems, func(i, j int) bool { return v.Systems[i].Blocks > v.Systems[j].Blocks })
	sort.Slice(v.Actors, func(i, j int) bool { return v.Actors[i].ID < v.Actors[j].ID })
	sk := make([][3]string, 0, len(seams))
	for k := range seams {
		sk = append(sk, k)
	}
	sort.Slice(sk, func(i, j int) bool {
		if seams[sk[i]] != seams[sk[j]] {
			return seams[sk[i]] > seams[sk[j]]
		}
		return sk[i][0]+sk[i][1]+sk[i][2] < sk[j][0]+sk[j][1]+sk[j][2]
	})
	for _, k := range sk {
		v.Seams = append(v.Seams, Seam{From: k[0], To: k[1], Kind: k[2], N: seams[k]})
	}
	return v
}

// Gherkin writes a BRD as a .feature file. Blocks are named by their
// catalogue id when they have one (attrs catalogue_id), else by block id.
func Gherkin(b schema.BRD, bs []schema.Block) string {
	name := map[string]string{}
	for _, blk := range bs {
		if c := blk.Attrs["catalogue_id"]; c != "" {
			name[blk.ID] = c
		}
	}
	nm := func(id string) string {
		if n := name[id]; n != "" {
			return n
		}
		return id
	}
	var L []string
	L = append(L, "Feature: "+b.Title, "")
	for _, ln := range strings.Split(b.BusinessProblem, "\n") {
		L = append(L, "  "+ln)
	}
	L = append(L, "")
	for _, f := range b.Flags {
		if f.Meaning != "" {
			L = append(L, fmt.Sprintf("  # flag: %s  —  %s", f.ID, f.Meaning))
		}
	}
	L = append(L, "")
	step := func(word string, lines []string) {
		for j, s := range lines {
			w := word
			if j > 0 {
				w = "And"
			}
			L = append(L, "    "+w+" "+s)
		}
	}
	for _, s := range b.UserScenarios {
		L = append(L, "  Scenario: "+s.Narrative)
		step("Given", s.Given)
		step("When", s.When)
		step("Then", s.Then)
		names := make([]string, len(s.Blocks))
		for i, id := range s.Blocks {
			names[i] = nm(id)
		}
		L = append(L, "    # realised by: "+strings.Join(names, ", "), "")
	}
	for _, q := range b.OpenQuestions {
		L = append(L, "  # "+q)
	}
	if len(b.OpenQuestions) > 0 {
		L = append(L, "")
	}
	return strings.Join(L, "\n")
}
