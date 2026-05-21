package graph

import (
	"bytes"
	"encoding/json"
	"os"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Marshal renders the knowledge graph as deterministic, indented JSON. The Go
// encoder sorts map keys and every slice is pre-sorted by Build, so the output
// is byte-stable for byte-stable input.
func Marshal(kg schema.KnowledgeGraph) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(kg); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Write emits the knowledge graph to lattice.json at path.
func Write(path string, kg schema.KnowledgeGraph) error {
	data, err := Marshal(kg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
