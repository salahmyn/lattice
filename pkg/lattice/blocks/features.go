package blocks

// @module-feature blocks

import (
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// addFeatures converts the feature axis into blocks (rnd/0003): a feature is a
// composite block carrying its BRD link, its invariants are leaf blocks, and an
// entry point is a boundary-in block. Feature YAML on disk is unchanged.
func (g *graph) addFeatures(ms []schema.Manifest, eps []schema.EntryPoint) {
	for _, m := range ms {
		prov := schema.Provenance{Kind: schema.ProvenanceHuman, Source: "features/" + m.ID + ".yaml"}
		id := "feat:" + m.ID
		g.blocks[id] = &schema.Block{ID: id, Kind: "composite", Title: m.ID, Description: m.Purpose,
			BRD: m.ImplementsBRD, Truth: schema.TruthDeclared, Provenance: prov, Attrs: ownerAttrs(m.Owners)}
		for _, inv := range m.Invariants {
			iid := "inv:" + m.ID + "." + inv.ID
			g.blocks[iid] = &schema.Block{ID: iid, Kind: "invariant", Parent: id, Title: inv.ID,
				Description: inv.Statement, Truth: schema.TruthDeclared, Provenance: prov}
		}
	}
	known := map[string]bool{}
	for _, m := range ms {
		known[m.ID] = true
	}
	for _, m := range ms {
		prov := schema.Provenance{Kind: schema.ProvenanceHuman, Source: "features/" + m.ID + ".yaml"}
		for _, d := range m.DependsOn {
			if !known[d] {
				g.warnf("feature %s: depends_on %q is not a feature", m.ID, d)
				continue
			}
			g.edge("feat:"+m.ID, "feat:"+d, "uses", "depends on", nil, prov)
		}
	}
	for _, ep := range eps {
		prov := schema.Provenance{Kind: schema.ProvenanceExtraction, Source: "entry-points/" + ep.ID}
		id := "ep:" + ep.ID
		// the extractor found this handler in code, so the block is wired
		g.blocks[id] = &schema.Block{ID: id, Kind: "boundary-in", Title: triggerTitle(ep), Description: ep.Purpose,
			Truth: schema.TruthWired, Provenance: prov, Attrs: map[string]string{"entry_kind": ep.Kind}}
		for _, s := range ep.Flow {
			if !known[s.Feature] {
				g.warnf("entry point %s: flow step %q is not a feature", ep.ID, s.Feature)
				continue
			}
			g.edge(id, "feat:"+s.Feature, "flow", s.Capability, nil, prov)
		}
	}
}

// ownerAttrs carries a manifest's owners onto its feature block, so the block
// map can group by owner: the engineering owner, else the business owner.
func ownerAttrs(o schema.Owners) map[string]string {
	owner := o.Engineering
	if owner == "" {
		owner = o.Business
	}
	if owner == "" {
		return nil
	}
	return map[string]string{"owner": owner}
}

func triggerTitle(ep schema.EntryPoint) string {
	t := ep.Trigger
	for _, s := range []string{strings.TrimSpace(t.Method + " " + t.Path), t.Command, t.Schedule, t.Queue, t.Event} {
		if s != "" {
			return s
		}
	}
	return ep.ID
}
