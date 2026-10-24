package schema

// @module-feature schema

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// CanonResult is what formatting one workspace file would do.
type CanonResult struct {
	// Canonical is the file as Lattice writes it: struct field order,
	// 2-space indent. Empty when the file is Skipped.
	Canonical []byte
	// Changed reports that Canonical differs from the input.
	Changed bool
	// Skipped says why the file cannot be rewritten without losing
	// something (a comment, an unknown key, an alias), or is "" when it can.
	Skipped string
}

// Canonicalize renders a feature manifest, BRD or entry point (v points
// at a zero value of its type) in canonical form, the way Lattice's own
// writers do. It refuses rather than lose information: a file with YAML
// comments, anchors or aliases, or keys its type does not know, is
// Skipped with the reason, and must be fixed by hand.
// @enforces schema:INV-2
func Canonicalize(data []byte, v interface{}) (CanonResult, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return CanonResult{}, err
	}
	if why := lossy(&root); why != "" {
		return CanonResult{Skipped: why}, nil
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return CanonResult{Skipped: "a key the schema does not know would be dropped: " + oneLine(err.Error())}, nil
	}
	out, err := MarshalCanonical(v)
	if err != nil {
		return CanonResult{}, err
	}
	return CanonResult{Canonical: out, Changed: !bytes.Equal(out, data)}, nil
}

// lossy returns why rewriting the document would lose something, or "".
func lossy(n *yaml.Node) string {
	if n.HeadComment != "" || n.LineComment != "" || n.FootComment != "" {
		return fmt.Sprintf("a comment at line %d would be dropped", n.Line)
	}
	if n.Anchor != "" || n.Kind == yaml.AliasNode {
		return fmt.Sprintf("an anchor or alias at line %d would be expanded", n.Line)
	}
	for _, c := range n.Content {
		if why := lossy(c); why != "" {
			return why
		}
	}
	return ""
}

// oneLine flattens a yaml error ("yaml: unmarshal errors:\n  line 3:
// field x not found in type T") onto one line.
func oneLine(s string) string {
	s = strings.TrimPrefix(s, "yaml: ")
	s = strings.TrimPrefix(s, "unmarshal errors:")
	return strings.Join(strings.Fields(s), " ")
}
