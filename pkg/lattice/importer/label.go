package importer

import (
	"path"
	"strconv"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// DraftsDirName is the subdirectory of the import dir holding draft manifests.
const DraftsDirName = "drafts"

// Mode is how a single draft was produced. The CLI prints a per-mode count so
// silent LLM failures (every candidate falling back to deterministic) are
// visible instead of hidden behind a single "[llm]" label.
type Mode string

const (
	// ModeDeterministic — no LLM configured; deterministic labeler ran.
	ModeDeterministic Mode = "deterministic"
	// ModeLLM — fresh LLM response this run.
	ModeLLM Mode = "llm"
	// ModeCached — served from the label cache (still LLM-derived).
	ModeCached Mode = "cached"
	// ModeFallback — LLM was configured but the call or parse failed; the
	// deterministic skeleton was used instead.
	ModeFallback Mode = "fallback"
)

// Draft pairs a candidate with the manifest drafted for it.
type Draft struct {
	CandidateID string          `json:"candidate_id"`
	Mode        Mode            `json:"mode"`
	Manifest    schema.Manifest `json:"manifest"`
}

// pkgPrefixes are leading directory segments dropped when deriving a feature
// id — they describe layout, not meaning.
var pkgPrefixes = map[string]bool{
	"src": true, "app": true, "lib": true, "pkg": true,
	"internal": true, "source": true, "packages": true,
}

// Label runs Stage 2 deterministically: each candidate becomes a draft
// manifest skeleton. No LLM is involved — ids are derived mechanically from
// the package path and prose is left as TODO. This is the air-gapped
// fallback; an LLM step later improves the labels, never the structure.
func Label(cf CandidatesFile) []Draft {
	drafts := make([]Draft, 0, len(cf.Candidates))
	seen := map[string]bool{}
	for _, c := range cf.Candidates {
		id := uniqueID(deriveFeatureID(c.Package), seen)
		drafts = append(drafts, Draft{
			CandidateID: c.ID,
			Mode:        ModeDeterministic,
			Manifest:    draftManifest(id, c),
		})
	}
	return drafts
}

// draftManifest builds a minimal, schema-valid proposal manifest. It declares
// no invariants on purpose: an unenforceable invariant would fail Stage-4
// verification, so the deterministic labeler never drafts one.
func draftManifest(id string, c Candidate) schema.Manifest {
	return schema.Manifest{
		ID:      id,
		Version: 1,
		Status:  schema.StatusProposal,
		Purpose: "TODO: state what " + id + " does. Drafted by `lattice import` from " +
			c.Package + " (" + plural(len(c.Symbols), "symbol") + ").",
		Owners: schema.Owners{Business: "TODO-team", Engineering: "TODO-team"},
		Capabilities: []schema.Capability{{
			ID:      "todo_capability",
			Summary: "TODO: name a behavior of this feature.",
			Rules:   []string{"TODO: state a rule this capability must follow."},
		}},
	}
}

// deriveFeatureID turns a source directory into a dotted feature id.
func deriveFeatureID(pkg string) string {
	pkg = path.Clean(pkg)
	if pkg == "." || pkg == "" || pkg == "/" {
		return "root"
	}
	segs := strings.Split(strings.Trim(pkg, "/"), "/")
	if len(segs) > 1 && pkgPrefixes[strings.ToLower(segs[0])] {
		segs = segs[1:]
	}
	parts := make([]string, 0, len(segs))
	for _, s := range segs {
		if clean := sanitizeSegment(s); clean != "" {
			parts = append(parts, clean)
		}
	}
	if len(parts) == 0 {
		return "root"
	}
	return strings.Join(parts, ".")
}

// sanitizeSegment lowercases a path segment and reduces it to an id-safe token.
func sanitizeSegment(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out != "" && out[0] >= '0' && out[0] <= '9' {
		out = "f_" + out
	}
	return out
}

// uniqueID disambiguates a derived id against ids already taken.
func uniqueID(base string, seen map[string]bool) string {
	id := base
	for n := 2; seen[id]; n++ {
		id = base + "_" + strconv.Itoa(n)
	}
	seen[id] = true
	return id
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
