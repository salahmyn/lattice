// Package patch applies typed, atomic edits to Lattice artifacts. Every edit
// is a two-phase operation: Preview computes the diff and the violations the
// patch would introduce or resolve; Apply writes atomically and rolls back if
// new error-severity violations appear.
package patch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/all"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/extract"
	"github.com/salahmyn/lattice/pkg/lattice/graph"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/validate"
)

// Engine applies patches against one repository.
type Engine struct {
	repo string
}

// New returns a patch engine rooted at repoPath.
func New(repoPath string) *Engine { return &Engine{repo: repoPath} }

// evaluation is the shared result of computing a patch's effect.
type evaluation struct {
	targetPath string // repo-relative path of the edited artifact
	newYAML    []byte
	preview    schema.PatchPreview
}

// Preview computes a patch's effect without writing.
func (e *Engine) Preview(ctx context.Context, p schema.Patch) (schema.PatchPreview, error) {
	ev, err := e.evaluate(ctx, p)
	if err != nil {
		return schema.PatchPreview{}, err
	}
	return ev.preview, nil
}

// Apply writes the patch atomically. If it would introduce error-severity
// violations the write is refused and the result is marked rolled back.
func (e *Engine) Apply(ctx context.Context, p schema.Patch) (schema.PatchResult, error) {
	ev, err := e.evaluate(ctx, p)
	if err != nil {
		return schema.PatchResult{}, err
	}
	if !ev.preview.IsAcceptable() {
		return schema.PatchResult{
			Applied: false, RolledBack: true, Diff: ev.preview.Diff,
			Violations: ev.preview.IntroducedViolations,
			Message:    "patch refused: would introduce error-severity violations",
		}, nil
	}
	abs := filepath.Join(e.repo, filepath.FromSlash(ev.targetPath))
	if err := atomicWrite(abs, ev.newYAML); err != nil {
		return schema.PatchResult{}, err
	}
	return schema.PatchResult{
		Applied: true, Diff: ev.preview.Diff, NewVersion: p.BaseVersion,
		Message: "patch applied to " + ev.targetPath,
	}, nil
}

// evaluate loads the corpus, applies the patch to an in-memory copy of the
// target, and computes the before/after violation delta.
func (e *Engine) evaluate(ctx context.Context, p schema.Patch) (evaluation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	adCfg, err := config.LoadAdapters(e.repo)
	if err != nil {
		return evaluation{}, err
	}
	cfg, _ := config.Load(e.repo)
	reg := all.Registry(adCfg)

	res, err := extract.Extract(ctx, e.repo, reg, extract.Options{IncludeProposals: true})
	if err != nil {
		return evaluation{}, err
	}

	before := validate.Validate(buildGraph(res), cfg)

	ev, err := e.applyToCorpus(&res, p)
	if err != nil {
		return evaluation{}, err
	}

	after := validate.Validate(buildGraph(res), cfg)
	ev.preview.IntroducedViolations = diffViolations(after, before)
	ev.preview.ResolvedViolations = diffViolations(before, after)
	return ev, nil
}

// applyToCorpus mutates the matching artifact within res and produces the
// diff and target path.
func (e *Engine) applyToCorpus(res *extract.Result, p schema.Patch) (evaluation, error) {
	switch p.TargetKind {
	case schema.TargetManifest:
		for i := range res.Manifests {
			if res.Manifests[i].ID != p.TargetID {
				continue
			}
			m := res.Manifests[i]
			if p.BaseVersion != 0 && p.BaseVersion != m.Version {
				return evaluation{}, fmt.Errorf("stale patch: base_version %d does not match manifest version %d",
					p.BaseVersion, m.Version)
			}
			orig, _ := schema.MarshalCanonical(stripAuto(m))
			if err := applyToManifest(&m, p.Operations); err != nil {
				return evaluation{}, err
			}
			res.Manifests[i] = m
			updated, _ := schema.MarshalCanonical(stripAuto(m))
			return evaluation{
				targetPath: m.SourcePath,
				newYAML:    updated,
				preview:    schema.PatchPreview{Diff: UnifiedDiff(string(orig), string(updated), m.SourcePath)},
			}, nil
		}
		return evaluation{}, fmt.Errorf("manifest %q not found", p.TargetID)

	case schema.TargetInitiative:
		for i := range res.Initiatives {
			if res.Initiatives[i].ID != p.TargetID {
				continue
			}
			in := res.Initiatives[i]
			orig, _ := schema.MarshalCanonical(in)
			if err := applyToInitiative(&in, p.Operations); err != nil {
				return evaluation{}, err
			}
			res.Initiatives[i] = in
			updated, _ := schema.MarshalCanonical(in)
			return evaluation{
				targetPath: in.SourcePath,
				newYAML:    updated,
				preview:    schema.PatchPreview{Diff: UnifiedDiff(string(orig), string(updated), in.SourcePath)},
			}, nil
		}
		return evaluation{}, fmt.Errorf("initiative %q not found", p.TargetID)

	case schema.TargetTask:
		for i := range res.Tasks {
			if res.Tasks[i].ID != p.TargetID {
				continue
			}
			t := res.Tasks[i]
			orig, _ := schema.MarshalCanonical(t)
			if err := applyToTask(&t, p.Operations); err != nil {
				return evaluation{}, err
			}
			res.Tasks[i] = t
			updated, _ := schema.MarshalCanonical(t)
			return evaluation{
				targetPath: t.SourcePath,
				newYAML:    updated,
				preview:    schema.PatchPreview{Diff: UnifiedDiff(string(orig), string(updated), t.SourcePath)},
			}, nil
		}
		return evaluation{}, fmt.Errorf("task %q not found", p.TargetID)

	default:
		return evaluation{}, fmt.Errorf("unsupported target kind %q", p.TargetKind)
	}
}

// stripAuto clears auto-populated fields so a patched manifest is written with
// only hand-edited content.
func stripAuto(m schema.Manifest) schema.Manifest {
	m.Implementations = nil
	m.Verifications = nil
	m.MutationScores = nil
	m.Children = nil
	m.SourcePath = ""
	return m
}

func buildGraph(res extract.Result) schema.KnowledgeGraph {
	return graph.Build(graph.Input{
		Manifests:   res.Manifests,
		Modules:     res.Modules,
		Initiatives: res.Initiatives,
		Tasks:       res.Tasks,
		Violations:  res.Violations,
	}, graph.Options{})
}

// diffViolations returns the violations in a that are not in b.
func diffViolations(a, b []schema.Violation) []schema.Violation {
	have := map[string]bool{}
	for _, v := range b {
		have[violationKey(v)] = true
	}
	var out []schema.Violation
	for _, v := range a {
		if !have[violationKey(v)] {
			out = append(out, v)
		}
	}
	return out
}

func violationKey(v schema.Violation) string {
	k := v.Code + "|" + v.Message
	if v.Location != nil {
		k += fmt.Sprintf("|%s:%d", v.Location.File, v.Location.Line)
	}
	return k
}

// atomicWrite writes data to path via a temp file and rename.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".lattice-patch-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
