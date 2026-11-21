package blocks

// @module-feature blocks

import (
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// FeatureBlockID is the id of the block a feature converts to.
func FeatureBlockID(feature string) string { return "feat:" + feature }

// InvariantBlockID is the id of the block an invariant of a feature converts to.
func InvariantBlockID(feature, invariant string) string { return "inv:" + feature + "." + invariant }

// FeatureGroupBlockID is the id of the group block that holds the features
// under a dotted id prefix when no feature declares that prefix itself.
func FeatureGroupBlockID(prefix string) string { return "feat-group:" + prefix }

// addFeatures converts the feature axis into blocks (rnd/0003): a feature is a
// composite block carrying its BRD link, its invariants are leaf blocks, and an
// entry point is a boundary-in block. Feature YAML on disk is unchanged.
//
// Every feature and invariant becomes a block. A dotted feature sits inside
// its parent: a.b.c inside the block of a.b when a feature a.b is declared,
// otherwise inside a group block for a.b, which in turn sits inside a (or a's
// group). A group judgment can still move any of them.
// @enforces blocks:INV-4
func (g *graph) addFeatures(ms []schema.Manifest, eps []schema.EntryPoint) {
	known := map[string]bool{}
	for _, m := range ms {
		known[m.ID] = true
	}
	for _, m := range ms {
		prov := featureProvenance(m)
		id := FeatureBlockID(m.ID)
		g.blocks[id] = &schema.Block{ID: id, Kind: "composite", Parent: g.featureParent(m.ID, known), Title: m.ID,
			Description: m.Purpose, BRD: m.ImplementsBRD, Truth: schema.TruthDeclared, Provenance: prov, Attrs: ownerAttrs(m.Owners)}
		for _, inv := range m.Invariants {
			iid := InvariantBlockID(m.ID, inv.ID)
			g.blocks[iid] = &schema.Block{ID: iid, Kind: "invariant", Parent: id, Title: inv.ID,
				Description: inv.Statement, Truth: schema.TruthDeclared, Provenance: prov}
		}
	}
	for _, m := range ms {
		prov := featureProvenance(m)
		for _, d := range m.DependsOn {
			if !known[d] {
				g.warnf("feature %s: depends_on %q is not a feature (%s)", m.ID, d, schema.CodeDependsOnMissing)
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
				g.warnf("entry point %s: flow step %q is not a feature (%s)", ep.ID, s.Feature, schema.CodePhantomFlow)
				continue
			}
			g.edge(id, "feat:"+s.Feature, "flow", s.Capability, nil, prov)
		}
	}
}

// featureParent is the block a feature's block sits in: the feature named by
// its id's prefix (a.b for a.b.c), else a group block for that prefix,
// created here and nested by the same rule. A top-level id has no parent.
func (g *graph) featureParent(id string, known map[string]bool) string {
	i := strings.LastIndex(id, ".")
	if i <= 0 {
		return ""
	}
	prefix := id[:i]
	if known[prefix] {
		return FeatureBlockID(prefix)
	}
	gid := FeatureGroupBlockID(prefix)
	if g.blocks[gid] == nil {
		g.blocks[gid] = &schema.Block{ID: gid, Kind: "group", Parent: g.featureParent(prefix, known), Title: prefix,
			Description: "Features under " + prefix + "; no feature " + prefix + " is declared.", Truth: schema.TruthDeclared,
			Provenance: schema.Provenance{Kind: schema.ProvenanceExtraction, Source: "features (id prefix " + prefix + ")"}}
	}
	return gid
}

// featureProvenance names the manifest a feature's blocks come from.
func featureProvenance(m schema.Manifest) schema.Provenance {
	src := m.SourcePath
	if src == "" {
		src = "features/" + m.ID + ".yaml"
	}
	return schema.Provenance{Kind: schema.ProvenanceHuman, Source: src}
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
