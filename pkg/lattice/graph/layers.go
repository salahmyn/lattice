package graph

// @module-feature graph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// postgresLanguage is the language the Postgres adapter reports (its Name).
// It is repeated here so the graph builder does not import an adapter.
const postgresLanguage = "postgres"

// AssignLayers gives every production symbol of kg its runtime layer and says
// where the layer came from. It never guesses; the first rung that applies
// decides:
//
//  1. an adapter fact: a symbol of the postgres adapter is data, and the
//     handler symbol of a declared entry point takes the layer of the entry
//     point's kind (schema.EntryPointLayer). The fact belongs to that symbol
//     alone, not to what it calls;
//  2. a declared path rule: the first of rules whose glob matches the
//     symbol's file;
//  3. otherwise unknown, with no source.
//
// A rule with a glob or layer that is not valid is skipped and reported as a
// VALIDATE_POLICY error. The result is a pure function of the graph and the
// rules.
// @enforces graph:INV-6
func AssignLayers(kg *schema.KnowledgeGraph, rules []schema.LayerRule) []schema.Violation {
	var viol []schema.Violation
	var valid []schema.LayerRule
	for i, r := range rules {
		switch {
		case !schema.ValidLayer(r.Layer):
			viol = append(viol, layerRuleError(i, fmt.Sprintf("layer %q is not one of %s", r.Layer, strings.Join(schema.Layers, ", "))))
		case !doublestar.ValidatePattern(r.Glob) || r.Glob == "":
			viol = append(viol, layerRuleError(i, fmt.Sprintf("glob %q is not a valid path pattern", r.Glob)))
		default:
			valid = append(valid, r)
		}
	}

	handler := map[string]string{} // handler symbol FQN -> layer of its entry point's kind
	eps := append([]schema.EntryPoint(nil), kg.EntryPoints...)
	sort.SliceStable(eps, func(i, j int) bool { return eps[i].ID < eps[j].ID })
	for _, ep := range eps {
		l, ok := schema.EntryPointLayer(ep.Kind)
		if _, seen := handler[ep.Handler.Symbol]; ok && ep.Handler.Symbol != "" && !seen { // the first entry point by id decides
			handler[ep.Handler.Symbol] = l
		}
	}

	for i := range kg.Symbols {
		s := &kg.Symbols[i]
		s.Layer, s.LayerSource = schema.LayerUnknown, ""
		if s.Language == postgresLanguage {
			s.Layer, s.LayerSource = schema.LayerData, schema.LayerSourceAdapter
			continue
		}
		if l, ok := handler[s.FQN]; ok {
			s.Layer, s.LayerSource = l, schema.LayerSourceAdapter
			continue
		}
		for _, r := range valid {
			if ok, _ := doublestar.Match(r.Glob, s.File); ok {
				if r.Layer != schema.LayerUnknown {
					s.Layer, s.LayerSource = r.Layer, schema.LayerSourceRule
				}
				break
			}
		}
	}
	return viol
}

func layerRuleError(i int, msg string) schema.Violation {
	return schema.Violation{
		Code: schema.CodeValidatePolicy, Severity: schema.SeverityError,
		Subject:  fmt.Sprintf("layers[%d]", i),
		Message:  fmt.Sprintf("workspace.yaml layers[%d]: %s", i, msg),
		Location: &schema.Location{File: "workspace.yaml"},
		NextAction: &schema.NextAction{Kind: "edit_workspace",
			Detail: "use a doublestar glob and one of: " + strings.Join(schema.Layers, ", ")},
	}
}

// FeatureLayers returns, per feature id, the sorted set of the layers of the
// symbols that implement it (the symbols of its Implementations). A feature
// with no layered implementation is absent.
func FeatureLayers(kg schema.KnowledgeGraph) map[string][]string {
	by := map[string][]string{}
	for _, s := range kg.Symbols {
		if s.Layer != "" && s.Feature != "" && isImplementationEdge(s) {
			by[s.Feature] = append(by[s.Feature], s.Layer)
		}
	}
	for f, ls := range by {
		by[f] = schema.LayerSet(ls)
	}
	return by
}

// DeriveBlockLayers sets Layers on the blocks of kg from the layers of its
// symbols: a feature block takes the set of its implementation symbols'
// layers, an invariant block the set of its enforcers', and an entry-point
// block the layer of its handler's kind. Nothing is declared; a block with no
// symbol in the graph gets none. Blocks are matched by their id (feat:<id>,
// inv:<feature>.<INV-n>, ep:<id>), the form the block graph builds them in.
func DeriveBlockLayers(kg *schema.KnowledgeGraph) {
	feat := FeatureLayers(*kg)
	inv := map[string][]string{}
	for _, s := range kg.Symbols {
		if s.Layer == "" {
			continue
		}
		for _, ref := range s.EnforcesInvariants {
			f, item := s.Feature, ref
			if f2, i2, ok := splitRef(ref); ok {
				f, item = f2, i2
			}
			inv["inv:"+f+"."+item] = append(inv["inv:"+f+"."+item], s.Layer)
		}
	}
	ep := map[string][]string{}
	for _, e := range kg.EntryPoints {
		if l, ok := schema.EntryPointLayer(e.Kind); ok {
			ep["ep:"+e.ID] = []string{l}
		}
	}
	for i := range kg.Blocks {
		b := &kg.Blocks[i]
		var ls []string
		switch {
		case strings.HasPrefix(b.ID, "feat:"):
			ls = feat[strings.TrimPrefix(b.ID, "feat:")]
		case strings.HasPrefix(b.ID, "inv:"):
			ls = inv[b.ID]
		case strings.HasPrefix(b.ID, "ep:"):
			ls = ep[b.ID]
		}
		b.Layers = schema.LayerSet(ls)
	}
}
