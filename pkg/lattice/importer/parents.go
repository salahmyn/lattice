package importer

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// PromoteParents finds every dotted feature id in featuresDir whose
// ancestors are not already declared, and creates an umbrella manifest for
// each missing ancestor. It returns the list of created feature ids.
//
// The motivation is the SUBFEATURE_PARENT_MISSING cascade that hit the
// v0.2.0 dogfood run: 49 LLM-drafted dotted ids needed 21 ancestor
// manifests, which a human reviewer had to author by hand. PromoteParents
// closes that loop deterministically — and is idempotent, so it's safe to
// re-run after every accept.
func PromoteParents(featuresDir string) ([]string, error) {
	existing, err := scanFeatureIDs(featuresDir)
	if err != nil {
		return nil, err
	}
	need := missingAncestors(existing)
	if len(need) == 0 {
		return nil, nil
	}
	created := make([]string, 0, len(need))
	for _, fid := range need {
		path := filepath.Join(featuresDir, filepath.FromSlash(strings.ReplaceAll(fid, ".", "/"))+".yaml")
		if _, err := os.Stat(path); err == nil {
			// File exists at the expected path but its id didn't match the
			// expected one (or it would be in `existing`). Skip rather than
			// clobber.
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return created, err
		}
		if err := schema.SaveCanonical(path, umbrellaManifest(fid)); err != nil {
			return created, err
		}
		created = append(created, fid)
	}
	return created, nil
}

// umbrellaManifest builds a minimal schema-valid parent manifest. It is
// flagged as a proposal so a reviewer can either keep it as a grouping or
// promote it into a real leaf feature.
func umbrellaManifest(id string) schema.Manifest {
	leaf := strings.ReplaceAll(lastSegment(id), "_", " ")
	return schema.Manifest{
		ID:      id,
		Version: 1,
		Status:  schema.StatusProposal,
		Purpose: schema.InlineText(fmt.Sprintf(
			"Umbrella feature grouping the %s sub-features. Drafted by `lattice import`.",
			leaf)),
		Owners: schema.Owners{Business: "TODO-team", Engineering: "TODO-team"},
		Capabilities: []schema.Capability{{
			ID:      "groups_subfeatures",
			Summary: schema.InlineText(fmt.Sprintf("Acts as a parent grouping for sub-features under %s.", id)),
			Rules:   []string{"TODO: state the policy this grouping enforces (or convert into a leaf feature)."},
		}},
	}
}

// missingAncestors computes every dotted ancestor id that does not appear
// in existing. Result is sorted by depth ascending then alphabetically so
// callers create parents before grandparents — keeps verify warnings clean
// during partial runs.
func missingAncestors(existing map[string]bool) []string {
	need := map[string]bool{}
	for fid := range existing {
		parts := strings.Split(fid, ".")
		for i := 1; i < len(parts); i++ {
			anc := strings.Join(parts[:i], ".")
			if !existing[anc] {
				need[anc] = true
			}
		}
	}
	out := make([]string, 0, len(need))
	for k := range need {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		di, dj := strings.Count(out[i], "."), strings.Count(out[j], ".")
		if di != dj {
			return di < dj
		}
		return out[i] < out[j]
	})
	return out
}

// idLineRE matches the leading `id:` field of a feature manifest. It is
// anchored to a line start so a value containing the substring "id:" in
// prose cannot match.
var idLineRE = regexp.MustCompile(`(?m)^id:\s*(\S+)`)

// scanFeatureIDs walks dir for *.yaml manifests and returns the set of
// declared feature ids. It tolerates malformed files (skips them silently)
// because the broader pipeline runs `lattice validate` to surface those.
func scanFeatureIDs(dir string) (map[string]bool, error) {
	ids := map[string]bool{}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return ids, nil
	}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return walkErr
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if m := idLineRE.FindSubmatch(data); m != nil {
			ids[string(m[1])] = true
		}
		return nil
	})
	return ids, err
}

func lastSegment(id string) string {
	if i := strings.LastIndex(id, "."); i >= 0 {
		return id[i+1:]
	}
	return id
}
