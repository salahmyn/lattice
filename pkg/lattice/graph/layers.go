package graph

// @module-feature graph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
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
		if l := schema.LayerOfFile(valid, s.File); l != schema.LayerUnknown {
			s.Layer, s.LayerSource = l, schema.LayerSourceRule
		}
	}
	return viol
}

// CheckHTTPOrigins reports each http.origins entry of workspace.yaml that is
// not a scheme plus host with an optional port and no path, nor env:<NAME>
// nor setting:<name> with a non-empty name of allowed characters, as a
// VALIDATE_POLICY error. The reach graph ignores such an entry.
// @enforces reachability:INV-11
func CheckHTTPOrigins(origins []string) []schema.Violation {
	var out []schema.Violation
	for i, o := range origins {
		if ir.ValidHTTPOriginEntry(o) {
			continue
		}
		out = append(out, schema.Violation{
			Code: schema.CodeValidatePolicy, Severity: schema.SeverityError,
			Subject:  fmt.Sprintf("http.origins[%d]", i),
			Message:  fmt.Sprintf("workspace.yaml http.origins[%d]: %q is not an origin (a scheme and a host with an optional port and no path, env:NAME or setting:name)", i, o),
			Location: &schema.Location{File: "workspace.yaml"},
			NextAction: &schema.NextAction{Kind: "edit_workspace",
				Detail: `write each origin like "https://app.example.com", "http://localhost:3000", "env:VITE_API_URL" or "setting:app.api_url"`},
		})
	}
	return out
}

// CheckChannelWrappers reports each channels: entry of workspace.yaml that
// cannot be read (an empty call, a negative argument index, a kind that is not
// one of ir.ChannelKinds, a handler_arg below -1) as a
// VALIDATE_POLICY error. The adapters ignore such an entry.
// @enforces reachability:INV-13
func CheckChannelWrappers(publish, consume []ir.ChannelWrapper) []schema.Violation {
	var out []schema.Violation
	bad := func(list string, i int, msg string) {
		subject := fmt.Sprintf("channels.%s[%d]", list, i)
		out = append(out, schema.Violation{
			Code: schema.CodeValidatePolicy, Severity: schema.SeverityError,
			Subject:  subject,
			Message:  fmt.Sprintf("workspace.yaml %s: %s", subject, msg),
			Location: &schema.Location{File: "workspace.yaml"},
			NextAction: &schema.NextAction{Kind: "edit_workspace",
				Detail: `write each entry like {call: "jobs.enqueue", arg: 0, kind: queue}; kind is one of ` + strings.Join(ir.ChannelKinds, ", ")},
		})
	}
	check := func(list string, ws []ir.ChannelWrapper) {
		for i, w := range ws {
			switch {
			case strings.TrimSpace(w.Call) == "":
				bad(list, i, "call is empty")
			case w.Arg < 0:
				bad(list, i, fmt.Sprintf("arg %d is not an argument index", w.Arg))
			case !ir.ValidChannelKind(w.Kind):
				bad(list, i, fmt.Sprintf("kind %q is not one of %s", w.Kind, strings.Join(ir.ChannelKinds, ", ")))
			case w.HandlerArg < -1:
				bad(list, i, fmt.Sprintf("handler_arg %d is not an argument index", w.HandlerArg))
			}
		}
	}
	check("publish", publish)
	check("consume", consume)
	return out
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
