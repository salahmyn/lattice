// Package onboarding manages the v0.5.0 two-surface setup flow:
// `lattice init` writes a state file capturing what's been answered;
// the UI's /onboarding page reads the same file and lets the user
// finish the remaining steps in the browser.
//
// State lives at lattice/onboarding.yaml. It is intentionally separate
// from config.yaml — config is the canonical settings file, onboarding
// is a transient setup ledger that gets `completed: true` and stops
// driving the UI when the user finishes.
package onboarding

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/detect"
)

// FileName is the on-disk name of the onboarding state file.
const FileName = "onboarding.yaml"

// Scope is the user's declared relationship with the existing codebase.
// Greenfield means "I'll write artifacts as I build"; the two
// brownfield modes differ in whether the user wants Lattice to
// regenerate everything from existing code (full) or only track new
// work going forward (incremental).
type Scope string

const (
	ScopeGreenfield            Scope = "greenfield"
	ScopeBrownfieldFull        Scope = "brownfield_full"
	ScopeBrownfieldIncremental Scope = "brownfield_incremental"
	ScopeUnset                 Scope = ""
)

// Step is the current step the wizard is on. The UI reads State.Step
// to know where to drop the user. Each step's data lives in the State
// struct rather than a sub-map, so the YAML round-trip is canonical.
type Step string

const (
	StepProjectMetadata Step = "project_metadata"   // title, owners, tone
	StepConfirmRoots    Step = "confirm_code_roots" // user adjusts detected code roots
	StepPluginInstall   Step = "plugin_install"     // adapter / SCIP indexer install
	StepScopeAction     Step = "scope_action"       // greenfield first-BRD or brownfield bulk import
	StepDone            Step = "done"
)

// State is the on-disk shape of lattice/onboarding.yaml. Field order
// is fixed by the struct — same canonical-YAML contract as Manifest.
//
// The state is small (under a hundred bytes once filled) so we
// re-read on every UI request rather than caching; SSE
// invalidates the page if anyone edits the file directly.
type State struct {
	Version int  `yaml:"version"`
	Step    Step `yaml:"step"`

	// Scope is set in the CLI's interactive step 1; the UI uses it to
	// pick which final action to show in step 4 (greenfield → "create
	// your first BRD", brownfield-full → "run bulk import").
	Scope Scope `yaml:"scope,omitempty"`

	// Detected is the output of pkg/lattice/detect at init time.
	// Re-runs of `lattice init` may re-detect and overwrite this; the
	// UI shows it as a confirmation in step 2.
	Detected DetectedSummary `yaml:"detected,omitempty"`

	// ProjectMetadata is the step-1 ledger.
	ProjectMetadata ProjectMetadata `yaml:"project_metadata,omitempty"`

	// PackagesInstalled records which plugin packages step-3 has
	// actually run — so re-runs of the wizard don't re-install.
	PackagesInstalled []string `yaml:"packages_installed,omitempty"`

	// Completed flips to true after step DONE; the UI then hides
	// the /onboarding link and treats the workspace as fully
	// configured. The state file stays on disk for audit; a user
	// can re-run `lattice init` to reset.
	Completed bool `yaml:"completed,omitempty"`
}

// DetectedSummary mirrors the parts of detect.Detection that survive
// past the init moment — the user-visible decisions, not the evidence
// trail. The full detect.Detection is recomputable any time.
type DetectedSummary struct {
	Language    detect.Language  `yaml:"language,omitempty"`
	Framework   detect.Framework `yaml:"framework,omitempty"`
	Confidence  string           `yaml:"confidence,omitempty"`
	CodeRoots   []string         `yaml:"code_roots,omitempty"`
	NeedsPackages []NeedsPackage `yaml:"needs_packages,omitempty"`
}

// NeedsPackage is a flattened detect.Package — same fields, kept here
// so the onboarding state file is self-contained and round-trips
// independently of the detect package's internal representation.
type NeedsPackage struct {
	Name    string `yaml:"name"`
	Manager string `yaml:"manager"`
	Reason  string `yaml:"reason,omitempty"`
}

// ProjectMetadata is the step-1 form. Title falls back to the directory
// name; owners default to git config user.email.
//
// JSON tags mirror the YAML keys so the wizard's POST body uses the
// same snake_case shape the on-disk file does — one schema, two
// surfaces.
type ProjectMetadata struct {
	Title            string `yaml:"title,omitempty"             json:"title,omitempty"`
	BusinessOwner    string `yaml:"business_owner,omitempty"    json:"business_owner,omitempty"`
	EngineeringOwner string `yaml:"engineering_owner,omitempty" json:"engineering_owner,omitempty"`
	ToneAudience     string `yaml:"tone_audience,omitempty"     json:"tone_audience,omitempty"`      // business | product | engineering | mixed
	ToneReadingLevel string `yaml:"tone_reading_level,omitempty" json:"tone_reading_level,omitempty"` // simple | intermediate | expert
}

// Load reads lattice/onboarding.yaml. A missing file is not an error —
// it just means onboarding hasn't been started; callers receive a
// zero-value State.
func Load(latticeDir string) (State, error) {
	path := filepath.Join(latticeDir, FileName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if err := yaml.Unmarshal(data, &s); err != nil {
		return State{}, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Save writes lattice/onboarding.yaml. Always re-creates the file (the
// state is small, no atomic-write concerns) so partial writes don't
// happen.
func Save(latticeDir string, s State) error {
	if s.Version == 0 {
		s.Version = 1
	}
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(latticeDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(latticeDir, FileName), data, 0o644)
}

// FromDetection seeds a fresh State from a detect.Detection. Step is
// set to project_metadata — the first user-facing step.
func FromDetection(d detect.Detection, scope Scope) State {
	pkgs := make([]NeedsPackage, 0, len(d.RequiredPackages))
	for _, p := range d.RequiredPackages {
		pkgs = append(pkgs, NeedsPackage{Name: p.Name, Manager: p.Manager, Reason: p.Reason})
	}
	return State{
		Version: 1,
		Step:    StepProjectMetadata,
		Scope:   scope,
		Detected: DetectedSummary{
			Language:      d.Language,
			Framework:     d.Framework,
			Confidence:    d.Confidence,
			CodeRoots:     d.CodeRoots,
			NeedsPackages: pkgs,
		},
	}
}

// NextStep advances `step` linearly through the wizard. Used by the
// UI's "Continue" handler after each step is submitted.
func NextStep(current Step) Step {
	switch current {
	case StepProjectMetadata:
		return StepConfirmRoots
	case StepConfirmRoots:
		return StepPluginInstall
	case StepPluginInstall:
		return StepScopeAction
	case StepScopeAction:
		return StepDone
	}
	return StepDone
}
