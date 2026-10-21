// Package blocks builds the v0.9 block graph (rnd/0003): catalogue/v1 files
// declare blocks, judgment records add what people decided, and Build computes
// the graph as a pure function of those inputs.
package blocks

import (
	"encoding/json"
	"fmt"
)

// File is one file read from a source: its path inside the source and its bytes.
type File struct {
	Path string
	Data []byte
}

// Catalogue is one catalogue/v1 file: the declared blocks of one area.
type Catalogue struct {
	Area    string      `json:"area"`
	Title   string      `json:"title"`
	Summary string      `json:"summary"`
	Configs []CatConfig `json:"configs"`
	Nodes   []CatNode   `json:"nodes"`
	Path    string      `json:"-"` // where it was read from, for provenance
}

// CatConfig is a configuration input an area reads, with the kind of its producer.
type CatConfig struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

// CatNode is one declared block.
type CatNode struct {
	ID           string              `json:"id"`
	Parent       string              `json:"parent"`
	Kind         string              `json:"kind"`
	Title        string              `json:"title"`
	Detail       string              `json:"detail"`
	When         []string            `json:"when"`
	Next         []CatNext           `json:"next"`
	API          *CatHTTP            `json:"api"`  // the request a client block sends
	HTTP         *CatHTTP            `json:"http"` // the route a server block answers
	Events       []string            `json:"events"`
	Source       []string            `json:"source"`
	Status       string              `json:"status"`
	Tests        map[string][]string `json:"tests"`
	ConfigSource *CatConfigSource    `json:"config_source"`
	Fulfills     []string            `json:"fulfills"` // catalogue ids of the client blocks this one answers
	E2EExempt    string              `json:"e2eExempt"`
}

// CatNext is a journey step to another block.
type CatNext struct {
	To    string   `json:"to"`
	Label string   `json:"label"`
	When  []string `json:"when"`
	Loop  bool     `json:"loop"`
}

// CatHTTP is a method and path; Variant names the response case a client block handles.
type CatHTTP struct {
	Method  string `json:"method"`
	Path    string `json:"path"`
	Variant string `json:"variant"`
}

// CatConfigSource says where a configuration value comes from.
type CatConfigSource struct {
	Type  string `json:"type"`
	Ref   string `json:"ref"`
	Scope string `json:"scope"`
}

// Parse reads one catalogue/v1 file. ok is false for valid JSON that is not a
// catalogue (no "area" and "nodes"), so a source directory can hold other files.
func Parse(f File) (c *Catalogue, ok bool, err error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(f.Data, &probe); err != nil {
		if json.Valid(f.Data) {
			return nil, false, nil // JSON, but not an object
		}
		return nil, false, fmt.Errorf("%s: not JSON: %w", f.Path, err)
	}
	if probe["area"] == nil && probe["nodes"] == nil {
		return nil, false, nil
	}
	if probe["area"] == nil || probe["nodes"] == nil { // one key without the other is a broken catalogue, not another file
		return nil, false, fmt.Errorf("%s: catalogue/v1: needs both area and nodes", f.Path)
	}
	c = &Catalogue{Path: f.Path}
	if err := json.Unmarshal(f.Data, c); err != nil {
		return nil, false, fmt.Errorf("%s: catalogue/v1: %w", f.Path, err)
	}
	if c.Area == "" {
		return nil, false, fmt.Errorf("%s: catalogue/v1: empty area", f.Path)
	}
	seen := map[string]bool{}
	for i, n := range c.Nodes {
		switch {
		case n.ID == "":
			return nil, false, fmt.Errorf("%s: catalogue/v1: node %d has no id", f.Path, i)
		case n.Kind == "":
			return nil, false, fmt.Errorf("%s: catalogue/v1: node %s has no kind", f.Path, n.ID)
		case seen[n.ID]:
			return nil, false, fmt.Errorf("%s: catalogue/v1: node id %s appears twice", f.Path, n.ID)
		}
		seen[n.ID] = true
	}
	return c, true, nil
}

// ParseAll parses every file of one source. It returns the catalogues and the
// paths that were JSON but not catalogues. A node id may appear in one file only.
func ParseAll(files []File) ([]*Catalogue, []string, error) {
	var cats []*Catalogue
	var skipped []string
	owner := map[string]string{}
	for _, f := range files {
		c, ok, err := Parse(f)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			skipped = append(skipped, f.Path)
			continue
		}
		for _, n := range c.Nodes {
			if prev, dup := owner[n.ID]; dup {
				return nil, nil, fmt.Errorf("catalogue/v1: node id %s is in %s and %s", n.ID, prev, f.Path)
			}
			owner[n.ID] = f.Path
		}
		cats = append(cats, c)
	}
	return cats, skipped, nil
}
