// Package extract discovers and loads every Lattice artifact: feature
// manifests, initiatives, tasks (all under the lattice/ directory) and the
// parsed source IR of the workspace's code roots.
package extract

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/salahmyn/lattice/pkg/lattice/adapters"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// Result is everything extraction found.
type Result struct {
	Manifests   []schema.Manifest
	Initiatives []schema.Initiative
	Tasks       []schema.Task
	Modules     []ir.Module
	Violations  []schema.Violation // load/parse failures, surfaced as violations
	// Review is true when no code root was accessible: manifests, initiatives
	// and tasks were loaded, but no source was parsed.
	Review bool
}

// Options controls extraction breadth.
type Options struct {
	IncludeProposals bool // load manifests under proposals/ directories
	ForceReview      bool // skip source parsing even when code is available
}

// skipDirs are directory names never descended into when scanning code.
var skipDirs = map[string]bool{
	".git": true, ".lattice": true, "node_modules": true, "vendor": true,
	"__pycache__": true, ".venv": true, "venv": true, "dist": true, "build": true,
}

// Extract loads all artifacts for a workspace. Source files are parsed
// concurrently through the adapter registry.
func Extract(ctx context.Context, w *workspace.Workspace, reg *adapters.Registry, opts Options) (Result, error) {
	var res Result

	manifests, mErr := loadManifests(w, opts)
	res.Manifests = manifests
	res.Violations = append(res.Violations, mErr...)

	inits, tasks, iErr := loadInitiativesAndTasks(w)
	res.Initiatives = inits
	res.Tasks = tasks
	res.Violations = append(res.Violations, iErr...)

	if w.Review || opts.ForceReview {
		res.Review = true
		return res, nil
	}

	modules, parseViol := parseSources(ctx, w, reg)
	res.Modules = modules
	res.Violations = append(res.Violations, parseViol...)
	return res, nil
}

// loadManifests loads every feature manifest under lattice/features/.
// SourcePath is recorded relative to the lattice/ directory.
func loadManifests(w *workspace.Workspace, opts Options) ([]schema.Manifest, []schema.Violation) {
	var manifests []schema.Manifest
	var viol []schema.Violation

	_ = filepath.WalkDir(w.FeaturesDir(), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !isYAML(path) {
			return nil
		}
		rel, _ := filepath.Rel(w.LatticeDir, path)
		rel = filepath.ToSlash(rel)
		if !opts.IncludeProposals && strings.Contains(rel, "/proposals/") {
			return nil
		}
		m, lerr := schema.LoadManifest(path)
		if lerr != nil {
			viol = append(viol, schema.Violation{
				Code: schema.CodeManifestSchema, Severity: schema.SeverityError,
				Message:  "failed to parse manifest: " + lerr.Error(),
				Location: &schema.Location{File: rel},
			})
			return nil
		}
		m.SourcePath = rel
		manifests = append(manifests, *m)
		return nil
	})

	sort.Slice(manifests, func(i, j int) bool { return manifests[i].ID < manifests[j].ID })
	return manifests, viol
}

// loadInitiativesAndTasks loads every initiative and its tasks from
// lattice/initiatives/<id>/.
func loadInitiativesAndTasks(w *workspace.Workspace) ([]schema.Initiative, []schema.Task, []schema.Violation) {
	root := w.InitiativesDir()
	var inits []schema.Initiative
	var tasks []schema.Task
	var viol []schema.Violation

	entries, err := os.ReadDir(root)
	if err != nil {
		return inits, tasks, viol // no initiatives dir is fine
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		initPath := filepath.Join(root, e.Name(), "initiative.yaml")
		rel, _ := filepath.Rel(w.LatticeDir, initPath)
		rel = filepath.ToSlash(rel)
		in, lerr := schema.LoadInitiative(initPath)
		if lerr != nil {
			if !os.IsNotExist(lerr) {
				viol = append(viol, schema.Violation{
					Code: schema.CodeInitiativeSchema, Severity: schema.SeverityError,
					Message:  "failed to parse initiative: " + lerr.Error(),
					Location: &schema.Location{File: rel},
				})
			}
			continue
		}
		in.SourcePath = rel
		inits = append(inits, *in)

		taskDir := filepath.Join(root, e.Name(), "tasks")
		taskEntries, _ := os.ReadDir(taskDir)
		for _, te := range taskEntries {
			if te.IsDir() || !isYAML(te.Name()) {
				continue
			}
			tp := filepath.Join(taskDir, te.Name())
			trel, _ := filepath.Rel(w.LatticeDir, tp)
			trel = filepath.ToSlash(trel)
			t, terr := schema.LoadTask(tp)
			if terr != nil {
				viol = append(viol, schema.Violation{
					Code: schema.CodeTaskSchema, Severity: schema.SeverityError,
					Message:  "failed to parse task: " + terr.Error(),
					Location: &schema.Location{File: trel},
				})
				continue
			}
			t.SourcePath = trel
			tasks = append(tasks, *t)
		}
	}

	sort.Slice(inits, func(i, j int) bool { return inits[i].ID < inits[j].ID })
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].Initiative != tasks[j].Initiative {
			return tasks[i].Initiative < tasks[j].Initiative
		}
		return tasks[i].ID < tasks[j].ID
	})
	return inits, tasks, viol
}

// sourceFile pairs an absolute path with its workspace-relative display path.
type sourceFile struct {
	abs string
	rel string
}

// parseSources walks every available code root and parses adapter-handled
// files into IR. Display paths are relative to the code root; when more than
// one root is present they are prefixed with the root name.
func parseSources(ctx context.Context, w *workspace.Workspace, reg *adapters.Registry) ([]ir.Module, []schema.Violation) {
	multiRoot := availableRootCount(w) > 1
	var files []sourceFile

	for _, root := range w.CodeRoots {
		if !root.Available {
			continue
		}
		_ = filepath.WalkDir(root.Abs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if skipDirs[d.Name()] || path == w.LatticeDir {
					return filepath.SkipDir
				}
				return nil
			}
			if reg.For(path) == nil {
				return nil
			}
			rel, _ := filepath.Rel(root.Abs, path)
			rel = filepath.ToSlash(rel)
			if multiRoot {
				rel = root.Name + "/" + rel
			}
			files = append(files, sourceFile{abs: path, rel: rel})
			return nil
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })

	type out struct {
		mod  ir.Module
		viol *schema.Violation
	}
	results := make([]out, len(files))

	const workers = 8
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for i, f := range files {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, f sourceFile) {
			defer wg.Done()
			defer func() { <-sem }()
			ad := reg.For(f.abs)
			src, rerr := os.ReadFile(f.abs)
			if rerr != nil {
				results[i] = out{viol: &schema.Violation{
					Code: schema.CodeAdapterParseError, Severity: schema.SeverityError,
					Message: "failed to read source: " + rerr.Error(), Location: &schema.Location{File: f.rel},
				}}
				return
			}
			mod, perr := ad.Parse(ctx, f.rel, src)
			if perr != nil {
				v := &schema.Violation{
					Code: schema.CodeAdapterParseError, Severity: schema.SeverityError,
					Message: perr.Error(), Location: &schema.Location{File: f.rel},
				}
				if pe, ok := perr.(*adapters.ParseError); ok {
					v.Location.Line = pe.Line
				}
				results[i] = out{viol: v}
				return
			}
			results[i] = out{mod: mod}
		}(i, f)
	}
	wg.Wait()

	var modules []ir.Module
	var viol []schema.Violation
	for _, r := range results {
		if r.viol != nil {
			viol = append(viol, *r.viol)
			continue
		}
		for _, d := range r.mod.Diagnostics {
			viol = append(viol, schema.Violation{
				Code: diagnosticCode(d.Code), Severity: schema.SeverityError,
				Message:  d.Message,
				Location: &schema.Location{File: r.mod.File, Line: d.Line},
			})
		}
		modules = append(modules, r.mod)
	}
	return modules, viol
}

func availableRootCount(w *workspace.Workspace) int {
	n := 0
	for _, r := range w.CodeRoots {
		if r.Available {
			n++
		}
	}
	return n
}

func isYAML(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}

// diagnosticCode maps an adapter diagnostic code to a known validation code,
// defaulting to ADAPTER_PARSE_ERROR.
func diagnosticCode(code string) string {
	switch code {
	case schema.CodeAnnotationArgNotLiteral:
		return schema.CodeAnnotationArgNotLiteral
	default:
		return schema.CodeAdapterParseError
	}
}
