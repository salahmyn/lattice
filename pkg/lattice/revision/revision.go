// Package revision loads, saves, and indexes change requests (CRs) —
// the only legitimate path for changing grounded business intent.
// A revision lives at lattice/revisions/<id>.yaml (id form CR-<n>,
// global and append-only: rejected CRs are archived in place, never
// deleted, so an idea isn't re-litigated from zero later).
package revision

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Dir is the revisions directory name under lattice/.
const Dir = "revisions"

// PathFor returns the on-disk path for a revision id in latticeDir.
func PathFor(latticeDir, id string) string {
	return filepath.Join(latticeDir, Dir, id+".yaml")
}

// LoadAll walks lattice/revisions and parses every *.yaml as a
// Revision. A missing directory is not an error — the CR flow is
// opt-in. Malformed files surface as REVISION_SCHEMA violations
// without breaking the rest of the load.
func LoadAll(latticeDir string) ([]schema.Revision, []schema.Violation) {
	var revs []schema.Revision
	var viol []schema.Violation
	dir := filepath.Join(latticeDir, Dir)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return revs, viol
	}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		rel, _ := filepath.Rel(latticeDir, path)
		r, lerr := schema.LoadRevision(path)
		if lerr != nil {
			viol = append(viol, schema.Violation{
				Code:     schema.CodeRevisionSchema,
				Severity: schema.SeverityError,
				Message:  "failed to parse revision: " + lerr.Error(),
				Location: &schema.Location{File: filepath.ToSlash(rel)},
			})
			return nil
		}
		r.SourcePath = filepath.ToSlash(rel)
		revs = append(revs, *r)
		return nil
	})
	sort.Slice(revs, func(i, j int) bool { return num(revs[i].ID) < num(revs[j].ID) })
	return revs, viol
}

// Find returns the revision with the given id, or nil.
func Find(revs []schema.Revision, id string) *schema.Revision {
	for i := range revs {
		if revs[i].ID == id {
			return &revs[i]
		}
	}
	return nil
}

// NextID returns the next free CR id (CR-1, CR-2, …) — ids are global
// and never reused, so the counter only moves forward.
func NextID(revs []schema.Revision) string {
	max := 0
	for _, r := range revs {
		if n := num(r.ID); n > max {
			max = n
		}
	}
	return fmt.Sprintf("CR-%d", max+1)
}

// Save writes a revision as canonical YAML, creating the directory.
func Save(latticeDir string, r schema.Revision) (string, error) {
	if strings.TrimSpace(r.ID) == "" {
		return "", fmt.Errorf("revision has no id")
	}
	if len(r.Targets) == 0 {
		return "", fmt.Errorf("revision %s has no targets", r.ID)
	}
	path := PathFor(latticeDir, r.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := schema.SaveCanonical(path, r); err != nil {
		return "", err
	}
	return path, nil
}

// RetirementCovered reports whether unit (a test FQN or symbol) is
// covered by a retirement item of any *approved* revision. Test/code
// deletion is legal only against such an item.
func RetirementCovered(revs []schema.Revision, unit string) (string, bool) {
	for _, r := range revs {
		if r.Status != schema.RevisionApproved && r.Status != schema.RevisionReconverged {
			continue
		}
		for _, item := range r.RetirementItems {
			if item == unit || strings.HasSuffix(unit, item) || strings.HasSuffix(item, unit) {
				return r.ID, true
			}
		}
	}
	return "", false
}

func num(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "CR-"))
	return n
}
