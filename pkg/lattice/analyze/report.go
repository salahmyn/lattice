// Package analyze implements Lattice's conflict and impact analysis: the
// deterministic graph checks, the embedding-based semantic checks, and the
// proposal impact report.
package analyze

// FindingLevel classifies an analysis finding.
type FindingLevel string

const (
	LevelOK      FindingLevel = "ok"
	LevelWarning FindingLevel = "warning"
	LevelError   FindingLevel = "error"
)

// Finding is one result from the analyzer.
type Finding struct {
	Level   FindingLevel `json:"level"`
	Code    string       `json:"code"`
	Message string       `json:"message"`
	// Detail carries structured context: consumers, similarity scores, etc.
	Detail map[string]interface{} `json:"detail,omitempty"`
}

// InvariantRequirement records what a proposed invariant still needs.
type InvariantRequirement struct {
	Invariant string   `json:"invariant"`
	Needs     []string `json:"needs"`
}

// BlastRadius is the code-level impact of a proposal, derived from SCIP.
type BlastRadius struct {
	Modifies          []string `json:"modifies,omitempty"`
	Adds              []string `json:"adds,omitempty"`
	AffectedTests     int      `json:"affected_tests"`
	ExternalConsumers int      `json:"external_consumers"`
	Available         bool     `json:"available"`
}

// ImpactReport is the full output of `lattice analyze proposal`.
type ImpactReport struct {
	Proposal              string                 `json:"proposal"`
	Target                string                 `json:"target"`
	Mode                  string                 `json:"mode"` // extend | create
	DeterministicFindings []Finding              `json:"deterministic_findings"`
	SemanticFindings      []Finding              `json:"semantic_findings"`
	BlastRadius           *BlastRadius           `json:"blast_radius,omitempty"`
	OpenInvariants        []InvariantRequirement `json:"open_invariant_requirements,omitempty"`
	ResolutionsRequired   []string               `json:"resolutions_required,omitempty"`
}

// HasConflicts reports whether any finding is error-level.
func (r ImpactReport) HasConflicts() bool {
	for _, f := range append(r.DeterministicFindings, r.SemanticFindings...) {
		if f.Level == LevelError {
			return true
		}
	}
	return false
}
