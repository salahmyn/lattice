package blocks

import (
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// SourceInfo names where one source's catalogues go in the graph.
type SourceInfo struct {
	ID     string // workspace source id, for provenance
	Prefix string // block id namespace, e.g. "fe"
	Tier   string
	System string // the system block that owns this source's areas
	Commit string // commit the catalogues were read at; empty for a directory
}

// imported is one source's catalogues as blocks. Edges stay keyed by catalogue
// id until Build has every source's keys.
type imported struct {
	blocks map[string]*schema.Block
	keys   map[string]string // catalogue node id -> block id (an area root maps to its area block)
	nodes  map[string]CatNode
	prov   map[string]schema.Provenance // catalogue node id -> provenance
	calls  [][2]string                  // catalogue ids: a parent in another area calls the child
}

func importSource(cats []*Catalogue, src SourceInfo) imported {
	im := imported{blocks: map[string]*schema.Block{}, keys: map[string]string{}, nodes: map[string]CatNode{},
		prov: map[string]schema.Provenance{}}
	area := map[string]string{} // catalogue node id -> area
	for _, c := range cats {
		prov := schema.Provenance{Kind: schema.ProvenanceAdapter, Source: sourceRef(src, c.Path)}
		akey := src.Prefix + ":" + c.Area
		title := c.Title
		if title == "" {
			title = c.Area
		}
		im.blocks[akey] = &schema.Block{ID: akey, Kind: "area", Parent: src.System, Title: title,
			Description: c.Summary, Tier: src.Tier, System: src.System, Truth: schema.TruthDeclared, Provenance: prov}
		for _, n := range c.Nodes {
			im.nodes[n.ID] = n
			area[n.ID] = c.Area
			im.prov[n.ID] = prov
		}
	}
	areaKey := func(id string) string { return src.Prefix + ":" + area[id] }
	for id, n := range im.nodes {
		if n.Parent == "" {
			im.keys[id] = areaKey(id)
		} else {
			im.keys[id] = src.Prefix + ":" + id
		}
	}
	for _, id := range sortedKeys(im.nodes) {
		n := im.nodes[id]
		if n.Parent == "" { // the area root describes its area block
			b := im.blocks[areaKey(id)]
			if n.Detail != "" {
				b.Description = n.Detail
			}
			b.Attrs = nodeAttrs(n)
			continue
		}
		parent := areaKey(id)
		if p, ok := im.nodes[n.Parent]; ok {
			switch {
			case area[n.Parent] != area[id]: // a block lives in its own area; a parent elsewhere is its caller
				im.calls = append(im.calls, [2]string{n.Parent, id})
			case p.Parent != "":
				parent = src.Prefix + ":" + n.Parent
			}
		}
		im.blocks[im.keys[id]] = &schema.Block{ID: im.keys[id], Kind: n.Kind, Parent: parent, Title: n.Title,
			Description: n.Detail, Tier: src.Tier, System: src.System, Truth: schema.TruthDeclared,
			Provenance: im.prov[id], Attrs: nodeAttrs(n)}
	}
	return im
}

func nodeAttrs(n CatNode) map[string]string {
	a := map[string]string{"catalogue_id": n.ID}
	set := func(k, v string) {
		if v != "" {
			a[k] = v
		}
	}
	if n.API != nil {
		set("api", strings.TrimSpace(n.API.Method+" "+n.API.Path))
		set("api_variant", n.API.Variant)
	}
	if n.HTTP != nil {
		set("http", strings.TrimSpace(n.HTTP.Method+" "+n.HTTP.Path))
	}
	set("when", strings.Join(n.When, " ∧ "))
	set("events", strings.Join(n.Events, ", "))
	set("status", n.Status)
	set("source", strings.Join(n.Source, " | "))
	if cs := n.ConfigSource; cs != nil {
		v := cs.Type
		if cs.Ref != "" {
			v += " · " + cs.Ref
		}
		set("config_source", v)
		set("config_scope", cs.Scope)
	}
	for _, k := range []string{"react", "vue", "php"} {
		set("tests_"+k, strings.Join(n.Tests[k], " | "))
	}
	set("e2e_exempt", n.E2EExempt)
	return a
}

func sourceRef(src SourceInfo, path string) string {
	if src.Commit == "" {
		return src.ID + ":" + path
	}
	c := src.Commit
	if len(c) > 7 {
		c = c[:7]
	}
	return src.ID + "@" + c + ":" + path
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
