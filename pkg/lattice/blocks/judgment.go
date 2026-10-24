package blocks

// @module-feature blocks

import (
	"bytes"
	"fmt"
	"path"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Judgment is a decision a person made about the map, kept as data: which
// systems exist, how externals group, which edges a catalogue cannot show,
// display names, and id conventions. Every element it produces carries
// provenance "judgment". Judgments of a type apply in id order.
type Judgment struct {
	ID        string          `yaml:"id"`
	Type      string          `yaml:"type"` // system | external | edge | group | label | alias
	Rationale string          `yaml:"rationale,omitempty"`
	Author    string          `yaml:"author,omitempty"`
	Entries   []JudgmentEntry `yaml:"entries"`
}

// JudgmentEntry is one row; which fields matter depends on the judgment type.
type JudgmentEntry struct {
	ID           string   `yaml:"id,omitempty"`            // system, external, group, label
	Title        string   `yaml:"title,omitempty"`         // system, external, group, label
	Kind         string   `yaml:"kind,omitempty"`          // block kind (system, external, group) or edge kind
	Parent       string   `yaml:"parent,omitempty"`        // system, external, group
	Technology   string   `yaml:"technology,omitempty"`    // system, external, group
	Description  string   `yaml:"description,omitempty"`   // system, external, group
	Members      []string `yaml:"members,omitempty"`       // group: blocks moved under it
	From         string   `yaml:"from,omitempty"`          // edge
	To           string   `yaml:"to,omitempty"`            // edge
	Label        string   `yaml:"label,omitempty"`         // edge
	Exclude      []string `yaml:"exclude,omitempty"`       // edge: ids or patterns left out of a pattern end
	Blocks       string   `yaml:"blocks,omitempty"`        // edge (config, events): only blocks whose id matches this pattern
	ConfigKind   string   `yaml:"config_kind,omitempty"`   // edge: from -> every block whose `when` reads a config of this kind
	ConfigSource string   `yaml:"config_source,omitempty"` // edge: from -> every block whose config_source has this type
	Scopes       []string `yaml:"scopes,omitempty"`        // edge with config_source: allowed scopes ("" = none)
	Events       []string `yaml:"events,omitempty"`        // edge: event names or patterns; from -> emitter, or emitter -> to
	Prefix       string   `yaml:"prefix,omitempty"`        // alias: a prefix that turns a short id into a full one
}

var judgmentTypes = map[string]bool{"system": true, "external": true, "edge": true, "group": true, "label": true, "alias": true}

// ParseJudgment reads one judgment record strictly. label names it in errors:
// a file path, or a store record.
func ParseJudgment(data []byte, label string) (Judgment, error) {
	var j Judgment
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // hand-written records: a misspelt key must not pass silently
	if err := dec.Decode(&j); err != nil {
		return Judgment{}, fmt.Errorf("%s: %w", label, err)
	}
	switch {
	case j.ID == "":
		return Judgment{}, fmt.Errorf("%s: judgment has no id", label)
	case !judgmentTypes[j.Type]:
		return Judgment{}, fmt.Errorf("%s: judgment %s has unknown type %q (system, external, edge, group, label, alias)", label, j.ID, j.Type)
	case len(j.Entries) == 0:
		return Judgment{}, fmt.Errorf("%s: judgment %s has no entries", label, j.ID)
	}
	if err := j.check(); err != nil {
		return Judgment{}, fmt.Errorf("%s: judgment %s: %w", label, j.ID, err)
	}
	return j, nil
}

// check enforces the fields each judgment type needs.
func (j Judgment) check() error {
	for i, e := range j.Entries {
		var need map[string]string
		switch j.Type {
		case "edge":
			rule := len(e.Events) > 0 || e.ConfigKind != "" || e.ConfigSource != ""
			switch {
			case len(e.Events) > 0 && (e.ConfigKind != "" || e.ConfigSource != ""), e.ConfigKind != "" && e.ConfigSource != "":
				return fmt.Errorf("entry %d: use one of events, config_kind and config_source", i)
			case len(e.Scopes) > 0 && e.ConfigSource == "":
				return fmt.Errorf("entry %d: scopes needs config_source", i)
			case rule && (isIDPattern(e.From) || isIDPattern(e.To)):
				return fmt.Errorf("entry %d: a rule entry's from and to are block ids, not patterns", i)
			}
			switch {
			case len(e.Events) > 0:
				need = map[string]string{"kind": e.Kind}
				if (e.From == "") == (e.To == "") {
					return fmt.Errorf("entry %d needs exactly one of from and to", i)
				}
				for _, p := range e.Events {
					if _, err := path.Match(p, ""); err != nil {
						return fmt.Errorf("entry %d: bad event pattern %q", i, p)
					}
				}
			case e.ConfigKind != "" || e.ConfigSource != "":
				need = map[string]string{"kind": e.Kind, "from": e.From}
			default:
				need = map[string]string{"from": e.From, "to": e.To, "kind": e.Kind}
			}
		case "label":
			need = map[string]string{"id": e.ID, "title": e.Title}
		case "alias":
			need = map[string]string{"prefix": e.Prefix}
		default:
			need = map[string]string{"id": e.ID}
		}
		for _, f := range []string{"id", "title", "from", "to", "kind", "prefix"} {
			if v, ok := need[f]; ok && v == "" {
				return fmt.Errorf("entry %d needs %s", i, f)
			}
		}
	}
	return nil
}

// ResolveID maps an id to a known block id: the id itself when known, else the
// first alias prefix that makes it known.
func ResolveID(id string, js []Judgment, known func(string) bool) (string, bool) {
	if known(id) {
		return id, true
	}
	for _, j := range js {
		if j.Type != "alias" {
			continue
		}
		for _, e := range j.Entries {
			if known(e.Prefix + id) {
				return e.Prefix + id, true
			}
		}
	}
	return "", false
}

func judgmentProvenance(j Judgment) schema.Provenance {
	return schema.Provenance{Kind: schema.ProvenanceJudgment, Source: "judgment:" + j.ID}
}

// applyNodes adds the blocks a system, external or group judgment declares.
func (g *graph) applyNodes(j Judgment, defaultKind string) {
	for _, e := range j.Entries {
		kind := e.Kind
		if kind == "" {
			kind = defaultKind
		}
		if g.blocks[e.ID] != nil {
			g.warnf("judgment %s: %s replaces an existing block", j.ID, e.ID)
		}
		var attrs map[string]string
		if e.Technology != "" {
			attrs = map[string]string{"technology": e.Technology}
		}
		if e.Parent != "" && g.blocks[e.Parent] == nil {
			g.warnf("judgment %s: %s parent %q is not a block", j.ID, e.ID, e.Parent)
		}
		g.blocks[e.ID] = &schema.Block{ID: e.ID, Kind: kind, Parent: e.Parent, Title: e.Title,
			Description: e.Description, Truth: schema.TruthDeclared, Provenance: judgmentProvenance(j), Attrs: attrs}
	}
}

// resolve maps a possibly short id to a block id through the alias judgments;
// an unresolved id is returned unchanged so callers keep their warnings.
func (g *graph) resolve(id string) string {
	if r, ok := ResolveID(id, g.aliases, func(x string) bool { return g.blocks[x] != nil }); ok {
		return r
	}
	return id
}

// applyGroup adds group blocks and moves their members under them.
func (g *graph) applyGroup(j Judgment) {
	g.applyNodes(j, "group")
	for _, e := range j.Entries {
		for _, m := range e.Members {
			m = g.resolve(m)
			b := g.blocks[m]
			if m == e.ID {
				g.warnf("judgment %s: group %s lists itself as a member", j.ID, e.ID)
				continue
			}
			if b == nil {
				g.warnf("judgment %s: group %s member %q is not a block", j.ID, e.ID, m)
				continue
			}
			b.Parent = e.ID
		}
	}
}

// applyEdge adds edges a catalogue cannot show. An end may be an id pattern.
// Config entries are rule edges (rules.go); event entries are applied by
// applyEvents across all edge judgments, so they are skipped here.
func (g *graph) applyEdge(j Judgment) {
	prov := judgmentProvenance(j)
	for _, e := range j.Entries {
		switch {
		case len(e.Events) > 0:
			continue
		case e.ConfigKind != "" || e.ConfigSource != "":
			g.applyConfig(j, e)
			continue
		}
		froms, tos := g.expand(e.From, e.Exclude), g.expand(e.To, e.Exclude)
		for _, end := range []struct {
			raw string
			ids []string
		}{{e.From, froms}, {e.To, tos}} {
			switch {
			case len(end.ids) == 0:
				g.warnf("judgment %s: %q matches no block", j.ID, end.raw)
			case len(end.ids) == 1 && g.blocks[end.ids[0]] == nil:
				g.warnf("judgment %s: edge end %q is not a block", j.ID, end.raw)
			}
		}
		for _, f := range froms {
			for _, t := range tos {
				g.edge(f, t, e.Kind, e.Label, nil, prov)
			}
		}
	}
}

// applyLabel renames blocks.
func (g *graph) applyLabel(j Judgment) {
	for _, e := range j.Entries {
		b := g.blocks[g.resolve(e.ID)]
		if b == nil {
			g.warnf("judgment %s: label target %q is not a block", j.ID, e.ID)
			continue
		}
		b.Title = e.Title
	}
}
