package entrypoints

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// EntryPointsDirName is the workspace-relative directory where accepted
// entry-point manifests live — peer to lattice/features/ for the
// invocation-axis artefacts.
const EntryPointsDirName = "entry-points"

// LoadEntryPoints reads every *.yaml under entryPointsDir (recursive)
// into a slice. Missing directory returns an empty result, not an
// error — most workspaces won't have any persisted EPs yet. Files that
// do not parse are skipped; LoadEntryPointsReport says which.
func LoadEntryPoints(entryPointsDir string) ([]schema.EntryPoint, error) {
	eps, _, err := LoadEntryPointsReport(entryPointsDir)
	return eps, err
}

// LoadProblem is a persisted entry-point file that could not be used.
type LoadProblem struct {
	Path    string // absolute path of the file
	Line    int    // 1-based line of a YAML error, when known
	Message string
}

// yamlLine finds the "line N" a yaml.v3 error names.
var yamlLine = regexp.MustCompile(`line (\d+)`)

// LoadEntryPointsReport is LoadEntryPoints that also returns the files it
// could not use (a YAML error, or no id), so a dropped entry point is a
// finding instead of a silent gap in the graph.
func LoadEntryPointsReport(entryPointsDir string) ([]schema.EntryPoint, []LoadProblem, error) {
	out := []schema.EntryPoint{}
	var problems []LoadProblem
	if _, err := os.Stat(entryPointsDir); os.IsNotExist(err) {
		return out, nil, nil
	}
	err := filepath.WalkDir(entryPointsDir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// Skip the v0.3.2 .rejected/ archive — rejected EPs are kept
		// there for recovery but must not contribute to the graph.
		if d.IsDir() {
			if d.Name() == ".rejected" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".yaml") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			problems = append(problems, LoadProblem{Path: p, Message: err.Error()})
			return nil
		}
		var ep schema.EntryPoint
		if err := yaml.Unmarshal(data, &ep); err != nil {
			line := 0
			if m := yamlLine.FindStringSubmatch(err.Error()); m != nil {
				line, _ = strconv.Atoi(m[1])
			}
			problems = append(problems, LoadProblem{Path: p, Line: line, Message: err.Error()})
			return nil
		}
		if ep.ID == "" {
			problems = append(problems, LoadProblem{Path: p, Message: "no id"})
			return nil
		}
		out = append(out, ep)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, problems, err
}

// SaveEntryPoint writes one EP to its canonical path under
// entryPointsDir, mirroring the lattice/features/ convention of
// turning a dotted id into a nested directory structure. Returns the
// absolute path so callers can show it in CLI output.
func SaveEntryPoint(entryPointsDir string, ep schema.EntryPoint) (string, error) {
	if ep.ID == "" {
		return "", nil
	}
	path := filepath.Join(entryPointsDir,
		filepath.FromSlash(strings.ReplaceAll(ep.ID, ".", "/"))+".yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	data, err := yaml.Marshal(ep)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// Merge combines detected EPs with persisted EPs, preferring the
// persisted version when the IDs match (so a labelled purpose isn't
// lost when the detector re-emits the same trigger). Detector-only
// EPs come through unchanged.
func Merge(detected, persisted []schema.EntryPoint) []schema.EntryPoint {
	byID := map[string]schema.EntryPoint{}
	for _, ep := range detected {
		byID[ep.ID] = ep
	}
	// Persisted wins on conflict — the human (or LLM-with-review) has
	// touched these.
	for _, ep := range persisted {
		merged := ep
		// Keep the detector's runtime flow tracing if persisted didn't
		// declare any — flows are derived state, not authored.
		if len(merged.Flow) == 0 {
			if det, ok := byID[ep.ID]; ok {
				merged.Flow = det.Flow
				merged.SideEffects = det.SideEffects
			}
		}
		byID[ep.ID] = merged
	}
	out := make([]schema.EntryPoint, 0, len(byID))
	for _, ep := range byID {
		out = append(out, ep)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
