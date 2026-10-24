package impact

// @module-feature impact

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// ManifestDiff is the field-by-field difference of the feature, BRD and
// entry-point files between the base and the working tree.
type ManifestDiff struct {
	Features    KindDiff `json:"features"`
	BRDs        KindDiff `json:"brds"`
	EntryPoints KindDiff `json:"entry_points"`
}

// Empty reports whether no manifest changed.
func (d ManifestDiff) Empty() bool {
	return d.Features.Empty() && d.BRDs.Empty() && d.EntryPoints.Empty()
}

// KindDiff is the difference for one kind of manifest.
type KindDiff struct {
	Added   []ManifestRef    `json:"added"`
	Removed []ManifestRef    `json:"removed"`
	Renamed []ManifestChange `json:"renamed"`
	Changed []ManifestChange `json:"changed"`
}

// Empty reports whether nothing of this kind changed.
func (k KindDiff) Empty() bool {
	return len(k.Added)+len(k.Removed)+len(k.Renamed)+len(k.Changed) == 0
}

func (k *KindDiff) normalize() {
	if k.Added == nil {
		k.Added = []ManifestRef{}
	}
	if k.Removed == nil {
		k.Removed = []ManifestRef{}
	}
	if k.Renamed == nil {
		k.Renamed = []ManifestChange{}
	}
	if k.Changed == nil {
		k.Changed = []ManifestChange{}
	}
	for _, list := range [][]ManifestChange{k.Renamed, k.Changed} {
		for i := range list {
			if list[i].Fields == nil {
				list[i].Fields = []FieldChange{}
			}
		}
	}
}

// ManifestRef names one added or removed manifest.
type ManifestRef struct {
	ID    string `json:"id"`
	Path  string `json:"path"`
	Error string `json:"error,omitempty"` // the file did not parse
}

// ManifestChange is a manifest present on both sides: renamed (its path or
// id changed) or changed in place, with every field that differs.
type ManifestChange struct {
	ID      string        `json:"id"`
	OldID   string        `json:"old_id,omitempty"`
	Path    string        `json:"path"`
	OldPath string        `json:"old_path,omitempty"`
	Fields  []FieldChange `json:"fields"`
	Error   string        `json:"error,omitempty"`
}

// FieldChange is one field that differs. Field is a path through the
// schema's YAML keys, with list items named by their id where they have
// one: `invariants[INV-1].verifiable_by`, `user_scenarios[US-2].then`.
type FieldChange struct {
	Field  string      `json:"field"`
	Change string      `json:"change"` // changed | added | removed
	Before interface{} `json:"before,omitempty"`
	After  interface{} `json:"after,omitempty"`
}

// Manifest kinds, also the JSON names of their sections.
const (
	kindFeature    = "features"
	kindBRD        = "brds"
	kindEntryPoint = "entry_points"
)

// kindOf says which manifest kind a repo-relative path holds, or "".
// Feature proposals and rejected entry points are outside, as extraction
// treats them.
func (pm pathMap) kindOf(p string) string {
	if p == "" || pm.featureDir == "" && pm.brdDir == "" {
		return ""
	}
	ext := strings.ToLower(filepath.Ext(p))
	if ext != ".yaml" && ext != ".yml" {
		return ""
	}
	switch {
	case strings.HasPrefix(p, pm.featureDir):
		if strings.Contains(strings.TrimPrefix(p, pm.featureDir), "proposals/") {
			return ""
		}
		return kindFeature
	case strings.HasPrefix(p, pm.brdDir):
		return kindBRD
	case strings.HasPrefix(p, pm.epDir):
		if ext != ".yaml" || strings.Contains(strings.TrimPrefix(p, pm.epDir), ".rejected/") {
			return ""
		}
		return kindEntryPoint
	}
	return ""
}

// doc is one side of a manifest: its id and its canonical YAML tree.
type doc struct {
	id   string
	node *yaml.Node
	err  string
	m    *schema.Manifest // features only, for the base-side role set
}

// decode parses a manifest of the given kind into its schema struct and
// re-renders it canonically, so the comparison walks exactly the fields
// the schema has (in struct order) and a single ref and a one-item list,
// or a reordered mapping, compare by meaning.
func decode(kind string, data []byte) doc {
	var v interface{}
	var id func() string
	var m *schema.Manifest
	switch kind {
	case kindFeature:
		m = &schema.Manifest{}
		v, id = m, func() string { return m.ID }
	case kindBRD:
		b := &schema.BRD{}
		v, id = b, func() string { return b.ID }
	case kindEntryPoint:
		e := &schema.EntryPoint{}
		v, id = e, func() string { return e.ID }
	default:
		return doc{err: "unknown manifest kind"}
	}
	if err := yaml.Unmarshal(data, v); err != nil {
		return doc{err: err.Error()}
	}
	out, err := schema.MarshalCanonical(v)
	if err != nil {
		return doc{id: id(), err: err.Error()}
	}
	var n yaml.Node
	if err := yaml.Unmarshal(out, &n); err != nil {
		return doc{id: id(), err: err.Error()}
	}
	root := &n
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		root = root.Content[0]
	}
	return doc{id: id(), node: root, m: m}
}

// diffManifests compares every changed manifest file. It also returns the
// base side's features (the head features with the changed ones swapped
// back), which give the base-side graph its roles.
func diffManifests(g repoGit, base string, pm pathMap, changes []FileChange, headFeatures []schema.Manifest) (ManifestDiff, []schema.Manifest) {
	var md ManifestDiff
	kinds := map[string]*KindDiff{kindFeature: &md.Features, kindBRD: &md.BRDs, kindEntryPoint: &md.EntryPoints}

	type side struct {
		path string
		d    doc
	}
	added := map[string][]side{}   // kind -> added files
	removed := map[string][]side{} // kind -> removed files
	baseFeat := map[string]*schema.Manifest{}
	dropFeat := map[string]bool{} // head feature paths absent on the base side

	readOld := func(kind, p string) doc {
		data, ok := showBlob(g, base, p)
		if !ok {
			return doc{err: "not found at base"}
		}
		return decode(kind, data)
	}
	readNew := func(kind, p string) doc {
		data, err := os.ReadFile(filepath.Join(g.dir, filepath.FromSlash(p)))
		if err != nil {
			return doc{err: err.Error()}
		}
		return decode(kind, data)
	}

	for _, c := range changes {
		oldKind, newKind := pm.kindOf(c.OldPath), pm.kindOf(c.NewPath)
		if oldKind == "" && newKind == "" {
			continue
		}
		var od, nd doc
		if oldKind != "" {
			od = readOld(oldKind, c.OldPath)
			if oldKind == kindFeature && od.m != nil {
				baseFeat[pm.latticeRel(c.OldPath)] = od.m
			}
		}
		if newKind != "" {
			nd = readNew(newKind, c.NewPath)
			if newKind == kindFeature {
				dropFeat[pm.latticeRel(c.NewPath)] = true
			}
		}
		// Same kind on both sides (edited, or moved within its directory):
		// one manifest, compared field by field. A copy leaves the original.
		if oldKind != "" && oldKind == newKind && c.Status != "C" {
			mc := compare(od, nd)
			mc.Path = c.NewPath
			if c.OldPath != c.NewPath {
				mc.OldPath = c.OldPath
			}
			if mc.OldPath != "" || mc.OldID != "" {
				kinds[newKind].Renamed = append(kinds[newKind].Renamed, mc)
			} else if len(mc.Fields) > 0 || mc.Error != "" {
				kinds[newKind].Changed = append(kinds[newKind].Changed, mc)
			}
			continue
		}
		// Moved out of (or into) a manifest directory: removed (or added).
		if oldKind != "" && c.Status != "C" {
			removed[oldKind] = append(removed[oldKind], side{c.OldPath, od})
		}
		if newKind != "" {
			added[newKind] = append(added[newKind], side{c.NewPath, nd})
		}
	}

	// An added and a removed file of one kind with the same id is the same
	// manifest moved (git saw too little similarity to call it a rename).
	for kind, kd := range kinds {
		used := map[int]bool{}
		for _, r := range removed[kind] {
			matched := false
			for j, a := range added[kind] {
				if used[j] || r.d.id == "" || a.d.id != r.d.id {
					continue
				}
				used[j], matched = true, true
				mc := compare(r.d, a.d)
				mc.Path, mc.OldPath = a.path, r.path
				kd.Renamed = append(kd.Renamed, mc)
				break
			}
			if !matched {
				kd.Removed = append(kd.Removed, ManifestRef{ID: r.d.id, Path: r.path, Error: r.d.err})
			}
		}
		for j, a := range added[kind] {
			if !used[j] {
				kd.Added = append(kd.Added, ManifestRef{ID: a.d.id, Path: a.path, Error: a.d.err})
			}
		}
		sortKind(kd)
	}

	var baseFeatures []schema.Manifest
	for _, f := range headFeatures {
		if !dropFeat[f.SourcePath] {
			baseFeatures = append(baseFeatures, f)
		}
	}
	for _, p := range sortedKeys(baseFeat) {
		m := *baseFeat[p]
		m.SourcePath = p
		baseFeatures = append(baseFeatures, m)
	}
	return md, baseFeatures
}

// latticeRel turns a repo-relative manifest path into the lattice/-relative
// form a manifest's SourcePath uses ("features/x.yaml").
func (pm pathMap) latticeRel(p string) string {
	root := strings.TrimSuffix(pm.featureDir, "features/")
	return strings.TrimPrefix(p, root)
}

func sortKind(k *KindDiff) {
	sort.Slice(k.Added, func(i, j int) bool { return k.Added[i].Path < k.Added[j].Path })
	sort.Slice(k.Removed, func(i, j int) bool { return k.Removed[i].Path < k.Removed[j].Path })
	sort.Slice(k.Renamed, func(i, j int) bool { return k.Renamed[i].Path < k.Renamed[j].Path })
	sort.Slice(k.Changed, func(i, j int) bool { return k.Changed[i].Path < k.Changed[j].Path })
}

// compare diffs the two sides of one manifest.
func compare(old, new doc) ManifestChange {
	mc := ManifestChange{ID: new.id}
	if mc.ID == "" {
		mc.ID = old.id
	}
	if old.id != "" && new.id != "" && old.id != new.id {
		mc.OldID = old.id
	}
	switch {
	case old.err != "" && new.err != "":
		mc.Error = "base: " + old.err + "; working tree: " + new.err
		return mc
	case new.err != "":
		mc.Error = "working tree: " + new.err
		return mc
	case old.err != "":
		mc.Error = "base: " + old.err
		return mc
	}
	diffNode("", old.node, new.node, &mc.Fields)
	return mc
}

// listKeys are the fields that name an item of a list, in preference order.
// The first one every item carries, with distinct values, is used.
var listKeys = []string{"id", "code", "adr", "seat", "feature", "symbol"}

// diffNode walks two canonical YAML trees and records every leaf that
// differs. It walks the schema's own fields generically, so a field added
// to a struct is compared without this code changing.
// @enforces impact:INV-4
func diffNode(path string, a, b *yaml.Node, out *[]FieldChange) {
	switch {
	case a == nil && b == nil:
		return
	case a == nil:
		*out = append(*out, FieldChange{Field: path, Change: "added", After: value(b)})
		return
	case b == nil:
		*out = append(*out, FieldChange{Field: path, Change: "removed", Before: value(a)})
		return
	}
	if a.Kind == yaml.MappingNode && b.Kind == yaml.MappingNode {
		am, akeys := mapping(a)
		bm, bkeys := mapping(b)
		for _, k := range bkeys {
			diffNode(join(path, k), am[k], bm[k], out)
		}
		for _, k := range akeys {
			if _, ok := bm[k]; !ok {
				diffNode(join(path, k), am[k], nil, out)
			}
		}
		return
	}
	if a.Kind == yaml.SequenceNode && b.Kind == yaml.SequenceNode {
		if key := itemKey(a, b); key != "" {
			am, akeys := items(a, key)
			bm, bkeys := items(b, key)
			for _, k := range bkeys {
				diffNode(path+"["+k+"]", am[k], bm[k], out)
			}
			for _, k := range akeys {
				if _, ok := bm[k]; !ok {
					diffNode(path+"["+k+"]", am[k], nil, out)
				}
			}
			return
		}
	}
	if av, bv := value(a), value(b); !reflect.DeepEqual(av, bv) {
		*out = append(*out, FieldChange{Field: path, Change: "changed", Before: av, After: bv})
	}
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func mapping(n *yaml.Node) (map[string]*yaml.Node, []string) {
	m := map[string]*yaml.Node{}
	var keys []string
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i].Value
		if _, dup := m[k]; !dup {
			keys = append(keys, k)
		}
		m[k] = n.Content[i+1]
	}
	return m, keys
}

// itemKey picks the field that identifies the items of both lists, or "".
func itemKey(a, b *yaml.Node) string {
	for _, key := range listKeys {
		if keyed(a, key) && keyed(b, key) {
			return key
		}
	}
	return ""
}

func keyed(seq *yaml.Node, key string) bool {
	if len(seq.Content) == 0 {
		return true
	}
	seen := map[string]bool{}
	for _, it := range seq.Content {
		if it.Kind != yaml.MappingNode {
			return false
		}
		m, _ := mapping(it)
		v, ok := m[key]
		if !ok || v.Kind != yaml.ScalarNode || v.Value == "" || seen[v.Value] {
			return false
		}
		seen[v.Value] = true
	}
	return true
}

func items(seq *yaml.Node, key string) (map[string]*yaml.Node, []string) {
	out := map[string]*yaml.Node{}
	var keys []string
	for _, it := range seq.Content {
		m, _ := mapping(it)
		k := m[key].Value
		out[k] = it
		keys = append(keys, k)
	}
	return out, keys
}

func value(n *yaml.Node) interface{} {
	if n == nil {
		return nil
	}
	var v interface{}
	if err := n.Decode(&v); err != nil {
		return n.Value
	}
	return v
}
