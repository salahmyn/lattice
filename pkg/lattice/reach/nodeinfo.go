package reach

// @module-feature reachability

// NodeInfo describes one node of the graph: a symbol, or a pseudo-node the
// graph treats like one (a module's top-level code, a cron job, a database
// table).
type NodeInfo struct {
	FQN string `json:"fqn"`
	// Kind is the symbol kind (function, method, ...) or the pseudo-node
	// kind (top, cron, serve, table).
	Kind string `json:"kind"`
	// File is the graph path of the module the node belongs to.
	File string `json:"file,omitempty"`
	// Language is the adapter's language name.
	Language string `json:"language,omitempty"`
	// Symbol is true for a symbol of the graph, false for a pseudo-node.
	Symbol bool `json:"symbol"`
}

// NodeInfo returns what the graph knows of fqn, and false when it is not a
// node of the graph.
func (g *Graph) NodeInfo(fqn string) (NodeInfo, bool) {
	n, ok := g.nodes[fqn]
	if !ok {
		return NodeInfo{}, false
	}
	return NodeInfo{FQN: n.fqn, Kind: n.kind, File: n.file, Language: n.lang, Symbol: n.symbol}, true
}
