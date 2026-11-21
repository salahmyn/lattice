package reach

// @module-feature reachability

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// kindChannel is the node kind of a channel: a queue, a NOTIFY channel or an
// event the code publishes to and consumes from.
const kindChannel = "channel"

// ChannelFQN is the node name of a channel: chan.<kind>.<name>.
func ChannelFQN(kind, name string) string { return "chan." + kind + "." + name }

// ChannelEnd is one end of a channel: the node whose code publishes to it, or
// the node that runs when a message arrives, and where that is written.
type ChannelEnd struct {
	Symbol string `json:"symbol"`
	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
}

// Channel is a channel node and the ends that name it.
type Channel struct {
	FQN        string       `json:"fqn"`
	Kind       string       `json:"kind"`
	Name       string       `json:"name"`
	Publishers []ChannelEnd `json:"publishers,omitempty"`
	Consumers  []ChannelEnd `json:"consumers,omitempty"`
}

// Channels lists every channel of the graph, sorted by FQN. A channel exists
// when at least one end names it with a literal.
func (g *Graph) Channels() []Channel {
	out := make([]Channel, 0, len(g.chans))
	for _, c := range g.chans {
		cc := *c
		cc.Publishers = append([]ChannelEnd(nil), c.Publishers...)
		cc.Consumers = append([]ChannelEnd(nil), c.Consumers...)
		out = append(out, cc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FQN < out[j].FQN })
	return out
}

// isAsyncKind reports whether an edge kind crosses a channel.
func isAsyncKind(k string) bool { return k == ir.RefPublish || k == ir.RefConsume }

// channelNode returns the channel a key names, creating its node on first
// use. ok is false for a key that is no "<kind>:<name>".
func (b *builder) channelNode(key string) (*Channel, bool) {
	kind, name, ok := ir.SplitChannelKey(key)
	if !ok {
		return nil, false
	}
	fqn := ChannelFQN(kind, name)
	g := b.g
	if c, have := g.chans[fqn]; have {
		return c, true
	}
	c := &Channel{FQN: fqn, Kind: kind, Name: name}
	if g.chans == nil {
		g.chans = map[string]*Channel{}
	}
	g.chans[fqn] = c
	if _, dup := g.nodes[fqn]; !dup {
		g.nodes[fqn] = &node{fqn: fqn, name: fqn, kind: kindChannel, refsKnown: true, mod: -1}
	}
	return c, true
}

// resolvePublish joins a publish reference to its channel: an edge of kind
// publish from the publisher to the channel node. A computed channel name
// ("" key) makes no edge and is unresolved, dynamic.
// @enforces reachability:INV-13
func (b *builder) resolvePublish(from string, r ir.Ref, resolved func(string, string), unresolved func(string, []string)) {
	c, ok := b.channelNode(r.Name)
	if !ok {
		unresolved("dynamic", nil)
		return
	}
	end := ChannelEnd{Symbol: from, Line: r.Line}
	if n := b.g.nodes[from]; n != nil {
		end.File = n.file
	}
	c.Publishers = append(c.Publishers, end)
	resolved(c.FQN, "channel")
}

// resolveConsume joins a consume reference to its channel: an edge of kind
// consume from the channel node to the consumer, which is the function the
// reference names as the handler (a named function in the owner's scope or
// imports) or, with no named handler, the code that owns the reference. A
// named handler that is no node of the workspace is unresolved (missing).
// @enforces reachability:INV-13
func (b *builder) resolveConsume(mc *modCtx, owner *node, from string, r ir.Ref, unresolved func(string, []string)) {
	c, ok := b.channelNode(r.Name)
	if !ok {
		unresolved("dynamic", nil)
		return
	}
	consumer := from
	if r.Handler != "" {
		if consumer = b.bareName(mc, owner, r.Handler); consumer == "" {
			unresolved("missing", nil)
			return
		}
	}
	b.addConsumer(c, consumer, r.Line, "channel")
}

// addConsumer records one consumer of a channel and draws the consume edge.
func (b *builder) addConsumer(c *Channel, consumer string, line int, via string) {
	end := ChannelEnd{Symbol: consumer, Line: line}
	if n := b.g.nodes[consumer]; n != nil {
		end.File = n.file
	}
	for _, e := range c.Consumers {
		if e.Symbol == consumer {
			return
		}
	}
	c.Consumers = append(c.Consumers, end)
	b.g.Stats.Resolved++
	b.addEdge(Edge{From: c.FQN, To: consumer, Kind: ir.RefConsume, Via: via, Line: line})
}

// bareName resolves a name written in owner's code to the FQN of a node: an
// import binding, then the owner's scopes, then dot imports. "" when it names
// nothing of the workspace.
func (b *builder) bareName(mc *modCtx, owner *node, name string) string {
	if bd, ok := mc.binds[name]; ok {
		return bd.fqn
	}
	if owner != nil {
		for _, scope := range b.scopes(owner, mc.mod.Namespace) {
			if t := scope + "." + name; b.exists(t) {
				return t
			}
		}
	}
	for _, ns := range mc.dots {
		if t := ns + "." + name; b.exists(t) {
			return t
		}
	}
	return ""
}

// collectEntryConsumers draws the consume edges of the entry points that name
// a queue or an event: the handler is the consumer of that channel.
func (b *builder) collectEntryConsumers(eps []schema.EntryPoint) {
	for _, ep := range eps {
		var kind, name string
		switch {
		case ep.Kind == schema.EntryPointKindQueue && ep.Trigger.Queue != "":
			kind, name = ir.ChannelQueue, ep.Trigger.Queue
		case ep.Kind == schema.EntryPointKindEventConsumer && ep.Trigger.Event != "":
			kind, name = ir.ChannelEvent, ep.Trigger.Event
		default:
			continue
		}
		n := b.g.nodes[ep.Handler.Symbol]
		if n == nil || n.isTest || !n.symbol {
			continue // a handler that is no symbol of the graph has no node to draw an edge to
		}
		if c, ok := b.channelNode(ir.ChannelKey(kind, name)); ok {
			b.addConsumer(c, ep.Handler.Symbol, ep.Handler.Line, "entry_point")
		}
	}
}

// asyncDistances returns, for every node reached from start, how many
// channels the best path crosses: 0 for a synchronous path, 1 for one that
// goes through one channel, and so on. A node is listed once, at its smallest
// count. Only resolved edges are followed, and a channel leads on only to the
// consumers that name it, so a publish nothing consumes reaches nothing.
func (g *Graph) asyncDistances(start []string) map[string]int {
	dist := map[string]int{}
	frontier := append([]string(nil), start...)
	for level := 0; len(frontier) > 0; level++ {
		var queue, chans []string
		for _, f := range frontier {
			if _, seen := dist[f]; !seen {
				dist[f] = level
				queue = append(queue, f)
			}
		}
		visit := func(n string) {
			if _, seen := dist[n]; !seen {
				dist[n] = level
				queue = append(queue, n)
			}
		}
		for i := 0; i < len(queue); i++ {
			cur := queue[i]
			if n := g.nodes[cur]; n != nil && n.kind == kindChannel {
				chans = append(chans, cur)
			}
			for _, n := range g.out[cur] {
				visit(n)
			}
			for _, n := range g.pub[cur] {
				visit(n)
			}
		}
		frontier = nil
		for _, c := range chans {
			for _, n := range g.cons[c] {
				if _, seen := dist[n]; !seen {
					frontier = append(frontier, n)
				}
			}
		}
	}
	return dist
}

// ChannelFindings raises the findings of the channel join, at info:
//
//   - CHANNEL_NO_CONSUMER, one per channel that code publishes to and no
//     consumer in the workspace names (a dangling out end);
//   - CHANNEL_NO_PUBLISHER, one per channel a consumer names and no publisher
//     does (fine for a queue fed from outside the workspace, but named);
//   - CHANNEL_DYNAMIC, one per file: the count of its channel ends whose name
//     is computed, and the first lines, so a large codebase is not flooded.
//
// Ends in test code are left out.
// @enforces reachability:INV-13
func ChannelFindings(r *Result) []schema.Violation {
	if r == nil || r.Graph == nil {
		return nil
	}
	g := r.Graph
	live := func(ends []ChannelEnd) []ChannelEnd {
		var out []ChannelEnd
		for _, e := range ends {
			if n := g.nodes[e.Symbol]; n != nil && n.isTest {
				continue
			}
			out = append(out, e)
		}
		return out
	}
	var out []schema.Violation
	for _, c := range g.Channels() {
		pubs, cons := live(c.Publishers), live(c.Consumers)
		switch {
		case len(pubs) > 0 && len(c.Consumers) == 0:
			out = append(out, schema.Violation{
				Code: schema.CodeChannelNoConsumer, Severity: schema.SeverityInfo,
				Subject:  c.FQN,
				Message:  fmt.Sprintf("%s publishes to %s %q (%s), and no consumer in the workspace names it (evidence: static call graph, literal channel name)", pubs[0].Symbol, c.Kind, c.Name, endLines(pubs)),
				Location: endLocation(pubs[0]),
				NextAction: &schema.NextAction{Kind: "edit_code",
					Detail: "fix the channel name at one end, or declare the consumer: an entry point with trigger.queue or trigger.event, or a channels.consume wrapper in workspace.yaml; a consumer outside the workspace needs nothing"},
			})
		case len(cons) > 0 && len(c.Publishers) == 0:
			out = append(out, schema.Violation{
				Code: schema.CodeChannelNoPublisher, Severity: schema.SeverityInfo,
				Subject:  c.FQN,
				Message:  fmt.Sprintf("%s consumes %s %q (%s), and no publisher in the workspace names it (evidence: static call graph, literal channel name)", cons[0].Symbol, c.Kind, c.Name, endLines(cons)),
				Location: endLocation(cons[0]),
				NextAction: &schema.NextAction{Kind: "edit_code",
					Detail: "fine when something outside the workspace feeds it (a webhook, another service); otherwise fix the channel name at one end, or declare the publisher wrapper in workspace.yaml channels.publish"},
			})
		}
	}
	byFile := map[string][]Unresolved{}
	var files []string
	for _, u := range g.Unresolved {
		if !isAsyncKind(u.Kind) || u.Why != "dynamic" {
			continue
		}
		if n := g.nodes[u.From]; n != nil && n.isTest {
			continue
		}
		if byFile[u.File] == nil {
			files = append(files, u.File)
		}
		byFile[u.File] = append(byFile[u.File], u)
	}
	sort.Strings(files)
	for _, f := range files {
		us := byFile[f]
		lines := make([]string, 0, firstLines)
		for _, u := range us {
			if len(lines) < firstLines {
				lines = append(lines, fmt.Sprint(u.Line))
			}
		}
		more := ""
		if len(us) > len(lines) {
			more = fmt.Sprintf(" and %d more", len(us)-len(lines))
		}
		out = append(out, schema.Violation{
			Code: schema.CodeChannelDynamic, Severity: schema.SeverityInfo,
			Subject:  f,
			Message:  fmt.Sprintf("%s has %d queue or event end(s) whose channel name is computed, so which channel they use is unknown (lines %s%s; evidence: static call graph)", f, len(us), strings.Join(lines, ", "), more),
			Location: &schema.Location{File: f, Line: us[0].Line},
			NextAction: &schema.NextAction{Kind: "edit_code",
				Detail: "write the channel name as a literal where it is fixed; a computed name never makes an edge"},
		})
	}
	return out
}

func endLocation(e ChannelEnd) *schema.Location {
	if e.File == "" {
		return nil
	}
	return &schema.Location{File: e.File, Line: e.Line}
}

// endLines lists the first sites of a set of ends, as file:line.
func endLines(ends []ChannelEnd) string {
	var parts []string
	for _, e := range ends {
		if len(parts) == firstLines {
			parts = append(parts, fmt.Sprintf("and %d more", len(ends)-firstLines))
			break
		}
		if e.File != "" && e.Line > 0 {
			parts = append(parts, fmt.Sprintf("%s:%d", e.File, e.Line))
		} else if e.File != "" {
			parts = append(parts, e.File)
		}
	}
	if len(parts) == 0 {
		return "no source line"
	}
	return strings.Join(parts, ", ")
}
