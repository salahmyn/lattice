// Package extract discovers and loads every Lattice artifact in a repository:
// feature manifests, initiatives, tasks, and parsed source IR.
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
)

// Result is everything extraction found in a repository.
type Result struct {
	Manifests   []schema.Manifest
	Initiatives []schema.Initiative
	Tasks       []schema.Task
	Modules     []ir.Module
	Violations  []schema.Violation // load/parse failures, surfaced as violations
}

// Options controls extraction breadth.
type Options struct {
	IncludeProposals bool // load manifests under proposals/ directories
}

// skipDirs are directory names never descended into.
var skipDirs = map[string]bool{
	".git": true, ".lattice": true, "node_modules": true, "vendor": true,
	"__pycache__": true, ".venv": true, "venv": true, "dist": true, "build": true,
}

// Extract scans repoPath and returns all artifacts. Source files are parsed
// concurrently through the adapter registry.
func Extract(ctx context.Context, repoPath string, reg *adapters.Registry, opts Options) (Result, error) {
	var res Result

	manifests, mErr := loadManifests(repoPath, opts)
	res.Manifests = manifests
	res.Violations = append(res.Violations, mErr...)

	inits, tasks, iErr := loadInitiativesAndTasks(repoPath)
	res.Initiatives = inits
	res.Tasks = tasks
	res.Violations = append(res.Violations, iErr...)

	modules, parseViol := parseSources(ctx, repoPath, reg)
	res.Modules = modules
	res.Violations = append(res.Violations, parseViol...)

	return res, nil
}

// loadManifests loads every feature manifest under features/.
func loadManifests(repoPath string, opts Options) ([]schema.Manifest, []schema.Violation) {
	dir := filepath.Join(repoPath, "features")
	var manifests []schema.Manifest
	var viol []schema.Violation

	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !isYAML(path) {
			return nil
		}
		rel, _ := filepath.Rel(repoPath, path)
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

// loadInitiativesAndTasks loads every initiative and its tasks.
func loadInitiativesAndTasks(repoPath string) ([]schema.Initiative, []schema.Task, []schema.Violation) {
	root := filepath.Join(repoPath, "work", "initiatives")
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
		rel, _ := filepath.Rel(repoPath, initPath)
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
			trel, _ := filepath.Rel(repoPath, tp)
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

// parseSources walks the repo and parses every adapter-handled file into IR.
func parseSources(ctx context.Context, repoPath string, reg *adapters.Registry) ([]ir.Module, []schema.Violation) {
	var paths []string
	_ = filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if reg.For(path) != nil {
			paths = append(paths, path)
		}
		return nil
	})
	sort.Strings(paths)

	type out struct {
		mod  ir.Module
		viol *schema.Violation
	}
	results := make([]out, len(paths))

	const workers = 8
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for i, p := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, p string) {
			defer wg.Done()
			defer func() { <-sem }()
			rel, _ := filepath.Rel(repoPath, p)
			rel = filepath.ToSlash(rel)
			ad := reg.For(p)
			src, rerr := os.ReadFile(p)
			if rerr != nil {
				results[i] = out{viol: &schema.Violation{
					Code: schema.CodeAdapterParseError, Severity: schema.SeverityError,
					Message: "failed to read source: " + rerr.Error(), Location: &schema.Location{File: rel},
				}}
				return
			}
			mod, perr := ad.Parse(ctx, rel, src)
			if perr != nil {
				v := &schema.Violation{
					Code: schema.CodeAdapterParseError, Severity: schema.SeverityError,
					Message: perr.Error(), Location: &schema.Location{File: rel},
				}
				if pe, ok := perr.(*adapters.ParseError); ok {
					v.Location.Line = pe.Line
				}
				results[i] = out{viol: v}
				return
			}
			results[i] = out{mod: mod}
		}(i, p)
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
