package graph

// @module-feature graph

import (
	"encoding/json"
	"os"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// CarryBlocks copies the block graph (written by refresh) from the existing
// graph at path onto kg, so a rebuild does not drop it. A missing or
// unparsable file leaves kg untouched.
// @enforces graph:INV-3
func CarryBlocks(path string, kg *schema.KnowledgeGraph) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var old schema.KnowledgeGraph
	if json.Unmarshal(data, &old) != nil {
		return
	}
	kg.Blocks, kg.BlockEdges, kg.Sources, kg.Flows = old.Blocks, old.BlockEdges, old.Sources, old.Flows
}
