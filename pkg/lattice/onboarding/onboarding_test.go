package onboarding

import (
	"path/filepath"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/detect"
)

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	state, err := Load(dir)
	if err != nil {
		t.Fatalf("Load on empty dir failed: %v", err)
	}
	if state.Step != "" || state.Completed {
		t.Errorf("expected zero state, got %+v", state)
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	state := State{
		Version: 1,
		Step:    StepConfirmRoots,
		Scope:   ScopeBrownfieldFull,
		Detected: DetectedSummary{
			Language: detect.LanguagePHP, Framework: detect.FrameworkLaravel,
			Confidence: "high", CodeRoots: []string{"app", "Modules"},
		},
		ProjectMetadata: ProjectMetadata{Title: "Demo", BusinessOwner: "x"},
		PackagesInstalled: []string{"sourcegraph/scip-php"},
	}
	if err := Save(dir, state); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Step != state.Step || got.Scope != state.Scope ||
		got.Detected.Language != state.Detected.Language ||
		len(got.PackagesInstalled) != 1 {
		t.Errorf("round-trip lost data: %+v", got)
	}
	// File path is the expected one.
	if _, err := Load(filepath.Dir(filepath.Join(dir, FileName))); err != nil {
		t.Error("LoadAll on correct dir should succeed")
	}
}

func TestNextStepLinearity(t *testing.T) {
	cases := []struct{ in, want Step }{
		{StepProjectMetadata, StepConfirmRoots},
		{StepConfirmRoots, StepPluginInstall},
		{StepPluginInstall, StepScopeAction},
		{StepScopeAction, StepDone},
		{StepDone, StepDone}, // terminal
	}
	for _, c := range cases {
		if got := NextStep(c.in); got != c.want {
			t.Errorf("NextStep(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFromDetectionSeedsAllFields(t *testing.T) {
	d := detect.Detection{
		Root: ".", Language: detect.LanguagePython, Framework: detect.FrameworkDjango,
		Confidence: "high", CodeRoots: []string{"app"},
		RequiredPackages: []detect.Package{{Name: "scip-python", Manager: "npm", Reason: "for SCIP"}},
	}
	s := FromDetection(d, ScopeBrownfieldFull)
	if s.Step != StepProjectMetadata {
		t.Errorf("step = %q, want project_metadata", s.Step)
	}
	if s.Scope != ScopeBrownfieldFull {
		t.Errorf("scope = %q, want brownfield_full", s.Scope)
	}
	if s.Detected.Language != detect.LanguagePython || s.Detected.Framework != detect.FrameworkDjango {
		t.Errorf("detected = %+v", s.Detected)
	}
	if len(s.Detected.NeedsPackages) != 1 || s.Detected.NeedsPackages[0].Name != "scip-python" {
		t.Errorf("needs_packages = %+v", s.Detected.NeedsPackages)
	}
}
