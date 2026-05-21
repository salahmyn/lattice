package patch

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// applySetField applies a generic SetField operation by round-tripping the
// artifact through a YAML map, setting the dotted path, and decoding back.
// This works uniformly across manifests, initiatives, and tasks.
func applySetField(artifact interface{}, op schema.Operation) error {
	field, err := argString(op, "field")
	if err != nil {
		return err
	}
	value, ok := op.Args["value"]
	if !ok {
		return fmt.Errorf("%s: missing argument %q", op.Op, "value")
	}

	b, err := yaml.Marshal(artifact)
	if err != nil {
		return err
	}
	var m map[string]interface{}
	if err := yaml.Unmarshal(b, &m); err != nil {
		return err
	}

	if err := setPath(m, strings.Split(field, "."), value); err != nil {
		return fmt.Errorf("%s: %w", op.Op, err)
	}

	nb, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(nb, artifact)
}

// setPath sets a dotted key path within a nested map, creating intermediate
// maps as needed.
func setPath(m map[string]interface{}, path []string, value interface{}) error {
	if len(path) == 0 {
		return fmt.Errorf("empty field path")
	}
	if len(path) == 1 {
		m[path[0]] = value
		return nil
	}
	next, ok := m[path[0]].(map[string]interface{})
	if !ok {
		next = map[string]interface{}{}
		m[path[0]] = next
	}
	return setPath(next, path[1:], value)
}
