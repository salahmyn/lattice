// Package brd loads, saves, and indexes Business Requirements Documents.
//
// A BRD lives at lattice/brds/<id>.yaml. The package is intentionally
// thin — schema parsing lives in pkg/lattice/schema; the validator
// reads what's emitted here through the KnowledgeGraph.
package brd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// FileNameFor returns the on-disk file name for a BRD id. The id is
// stored as the file name (with ".yaml") so a `git mv` is enough to
// rename and the on-disk path is grep-able by id.
func FileNameFor(id string) string { return id + ".yaml" }

// PathFor returns the absolute on-disk path for a BRD in the given dir.
func PathFor(dir, id string) string { return filepath.Join(dir, FileNameFor(id)) }

// LoadAll walks dir and parses every *.yaml file as a BRD. Returns the
// loaded BRDs plus any parse violations (so a single malformed file
// doesn't break the rest of the load).
//
// The dir not existing is not an error — brownfield projects start
// without any BRDs, and the v0.5 design treats `brds/` as optional.
func LoadAll(dir, latticeRoot string) ([]schema.BRD, []schema.Violation) {
	var brds []schema.BRD
	var viol []schema.Violation

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return brds, viol
	}

	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !isYAML(path) {
			return nil
		}
		rel, _ := filepath.Rel(latticeRoot, path)
		rel = filepath.ToSlash(rel)
		b, lerr := schema.LoadBRD(path)
		if lerr != nil {
			viol = append(viol, schema.Violation{
				Code:     schema.CodeBRDSchema,
				Severity: schema.SeverityError,
				Message:  "failed to parse BRD: " + lerr.Error(),
				Location: &schema.Location{File: rel},
			})
			return nil
		}
		b.SourcePath = rel
		brds = append(brds, *b)
		return nil
	})

	sort.Slice(brds, func(i, j int) bool { return brds[i].ID < brds[j].ID })
	return brds, viol
}

// Save writes a BRD to dir as canonical YAML, named by id. Creates the
// directory if necessary. Refuses to silently overwrite — callers that
// want overwrite-on-update use SaveForce.
func Save(dir string, b schema.BRD) (string, error) {
	if strings.TrimSpace(b.ID) == "" {
		return "", fmt.Errorf("BRD has no id")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := PathFor(dir, b.ID)
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("BRD already exists: %s", path)
	}
	if err := schema.SaveCanonical(path, b); err != nil {
		return "", err
	}
	return path, nil
}

// SaveForce writes a BRD unconditionally — used by `brd approve`,
// `brd link`, and `brd from-code` which intentionally update existing
// BRDs on disk.
func SaveForce(dir string, b schema.BRD) (string, error) {
	if strings.TrimSpace(b.ID) == "" {
		return "", fmt.Errorf("BRD has no id")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := PathFor(dir, b.ID)
	if err := schema.SaveCanonical(path, b); err != nil {
		return "", err
	}
	return path, nil
}

// Find returns the BRD with the given id, or nil if not found.
func Find(brds []schema.BRD, id string) *schema.BRD {
	for i := range brds {
		if brds[i].ID == id {
			return &brds[i]
		}
	}
	return nil
}

// ByStatus groups BRDs by their lifecycle status. The map values are
// id-sorted slices so the UI can render stable columns.
func ByStatus(brds []schema.BRD) map[schema.BRDStatus][]schema.BRD {
	out := map[schema.BRDStatus][]schema.BRD{}
	for _, b := range brds {
		out[b.Status] = append(out[b.Status], b)
	}
	for k := range out {
		sort.Slice(out[k], func(i, j int) bool { return out[k][i].ID < out[k][j].ID })
	}
	return out
}

// FeaturesByBRD returns a map from BRD id to the feature ids that
// declare `implements_brd: <id>` *or* are listed in BRD.ImplementsVia.
// The forward and reverse links are unioned so either side is
// authoritative.
func FeaturesByBRD(brds []schema.BRD, features []schema.Manifest) map[string][]string {
	out := map[string]map[string]bool{}
	for _, b := range brds {
		out[b.ID] = map[string]bool{}
		for _, fid := range b.ImplementsVia {
			out[b.ID][fid] = true
		}
	}
	for _, f := range features {
		if f.ImplementsBRD == "" {
			continue
		}
		if _, ok := out[f.ImplementsBRD]; !ok {
			out[f.ImplementsBRD] = map[string]bool{}
		}
		out[f.ImplementsBRD][f.ID] = true
	}
	result := map[string][]string{}
	for brdID, set := range out {
		ids := make([]string, 0, len(set))
		for id := range set {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		result[brdID] = ids
	}
	return result
}

func isYAML(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}
