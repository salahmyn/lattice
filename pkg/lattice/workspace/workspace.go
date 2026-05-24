// Package workspace resolves where a project's Lattice artifacts live and
// which code roots they govern.
//
// Every Lattice-maintained file — manifests, initiatives, decisions, schemas,
// config, skills, and the knowledge graph — lives under one visible `lattice/`
// directory. A workspace.yaml inside it selects one of two modes:
//
//   - embedded:   the lattice/ directory sits inside a single code repository.
//   - standalone: the lattice/ directory is its own repository and declares
//     the external code roots (repos, modules, packages) it
//     governs. Useful for multi-repo projects and for giving
//     PMs/QA access to meaning without access to code.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Dir is the single directory that holds every Lattice-maintained artifact.
const Dir = "lattice"

// Mode selects how the lattice/ directory relates to code.
type Mode string

const (
	// ModeEmbedded: lattice/ is a subdirectory of one code repository.
	ModeEmbedded Mode = "embedded"
	// ModeStandalone: lattice/ is its own repository governing external code.
	ModeStandalone Mode = "standalone"
)

// CodeRoot is one directory tree of source code that Lattice extracts from.
type CodeRoot struct {
	Name string `yaml:"name"`
	// Path is repo-relative (to the lattice/ dir's parent in embedded mode,
	// or to the lattice/ dir in standalone mode) or absolute.
	Path string `yaml:"path,omitempty"`
	// Git is an optional clone URL, recorded for documentation and for
	// standalone setups that vendor code roots as submodules.
	Git string `yaml:"git,omitempty"`

	// Abs and Available are resolved at Open time, not serialized.
	Abs       string `yaml:"-"`
	Available bool   `yaml:"-"`
}

// file is the on-disk workspace.yaml model.
type file struct {
	Mode      Mode       `yaml:"mode"`
	CodeRoots []CodeRoot `yaml:"code_roots,omitempty"`
}

// Workspace is the resolved view of a project's Lattice setup.
type Workspace struct {
	// LatticeDir is the absolute path of the lattice/ directory.
	LatticeDir string
	Mode       Mode
	CodeRoots  []CodeRoot
	// Review is true when no code root is accessible, so only manifest-level
	// operations are possible (the PM/QA review case).
	Review bool
}

// Open resolves the workspace starting from startPath.
//
// It finds the lattice/ directory by checking, in order: <startPath>/lattice/,
// then <startPath> itself (a standalone lattice/ repo). It returns an error
// if neither is an initialized Lattice directory.
func Open(startPath string) (*Workspace, error) {
	abs, err := filepath.Abs(startPath)
	if err != nil {
		return nil, err
	}

	var latticeDir string
	if isLatticeDir(filepath.Join(abs, Dir)) {
		latticeDir = filepath.Join(abs, Dir)
	} else if isLatticeDir(abs) {
		latticeDir = abs
	} else {
		return nil, fmt.Errorf("no Lattice workspace found at %s (run `lattice init`)", startPath)
	}

	w := &Workspace{LatticeDir: latticeDir, Mode: ModeEmbedded}
	if err := w.loadWorkspaceFile(); err != nil {
		return nil, err
	}
	w.resolveCodeRoots()
	return w, nil
}

// isLatticeDir reports whether dir looks like an initialized lattice/ dir.
func isLatticeDir(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "config.yaml"))
	return err == nil && !st.IsDir()
}

// loadWorkspaceFile reads workspace.yaml, defaulting to embedded mode.
func (w *Workspace) loadWorkspaceFile() error {
	path := filepath.Join(w.LatticeDir, "workspace.yaml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil // embedded with a single default code root
	}
	if err != nil {
		return err
	}
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if f.Mode != "" {
		w.Mode = f.Mode
	}
	w.CodeRoots = f.CodeRoots
	return nil
}

// resolveCodeRoots resolves each code root to an absolute path and records
// whether it is currently accessible. It synthesizes a default root when none
// are declared.
func (w *Workspace) resolveCodeRoots() {
	if len(w.CodeRoots) == 0 {
		// Embedded default: the code repository is the parent of lattice/.
		// Standalone default: the lattice/ dir itself (manifest-only).
		base := filepath.Dir(w.LatticeDir)
		if w.Mode == ModeStandalone {
			base = w.LatticeDir
		}
		w.CodeRoots = []CodeRoot{{Name: "default", Path: base}}
	}

	anchor := filepath.Dir(w.LatticeDir)
	if w.Mode == ModeStandalone {
		anchor = w.LatticeDir
	}

	allMissing := true
	for i := range w.CodeRoots {
		r := &w.CodeRoots[i]
		abs := r.Path
		if abs == "" {
			abs = anchor
		} else if !filepath.IsAbs(abs) {
			abs = filepath.Join(anchor, abs)
		}
		abs = filepath.Clean(abs)
		r.Abs = abs
		if st, err := os.Stat(abs); err == nil && st.IsDir() {
			r.Available = true
			allMissing = false
		}
	}
	w.Review = allMissing
}

// --- path accessors: everything Lattice maintains lives under LatticeDir ---

// ConfigPath returns the path to config.yaml.
func (w *Workspace) ConfigPath() string { return filepath.Join(w.LatticeDir, "config.yaml") }

// AdaptersPath returns the path to adapters.yaml.
func (w *Workspace) AdaptersPath() string { return filepath.Join(w.LatticeDir, "adapters.yaml") }

// MCPPath returns the path to mcp.yaml.
func (w *Workspace) MCPPath() string { return filepath.Join(w.LatticeDir, "mcp.yaml") }

// WorkspacePath returns the path to workspace.yaml.
func (w *Workspace) WorkspacePath() string { return filepath.Join(w.LatticeDir, "workspace.yaml") }

// ContextPath returns the path to context.yaml (C4 Level-1 declarations).
func (w *Workspace) ContextPath() string { return filepath.Join(w.LatticeDir, "context.yaml") }

// FeaturesDir returns the directory holding feature manifests.
func (w *Workspace) FeaturesDir() string { return filepath.Join(w.LatticeDir, "features") }

// BRDsDir returns the directory holding Business Requirements Documents
// — the v0.5.0 business-intent layer above features. Peer to FeaturesDir
// so the on-disk layout reads top-down: brds/ → features/ → entry-points/.
func (w *Workspace) BRDsDir() string { return filepath.Join(w.LatticeDir, "brds") }

// EntryPointsDir returns the directory holding accepted entry-point
// manifests — peer to FeaturesDir for the invocation axis.
func (w *Workspace) EntryPointsDir() string { return filepath.Join(w.LatticeDir, "entry-points") }

// InitiativesDir returns the directory holding initiatives and tasks.
func (w *Workspace) InitiativesDir() string { return filepath.Join(w.LatticeDir, "initiatives") }

// DecisionsDir returns the directory holding ADRs.
func (w *Workspace) DecisionsDir() string { return filepath.Join(w.LatticeDir, "decisions") }

// SchemasDir returns the directory holding locked contracts.
func (w *Workspace) SchemasDir() string { return filepath.Join(w.LatticeDir, "schemas") }

// SkillsDir returns the directory holding shipped and custom skills.
func (w *Workspace) SkillsDir() string { return filepath.Join(w.LatticeDir, "skills") }

// ViewsDir returns the directory holding view-template overrides.
func (w *Workspace) ViewsDir() string { return filepath.Join(w.LatticeDir, "views") }

// GraphPath returns the path to the knowledge graph (or its shard index).
func (w *Workspace) GraphPath() string { return filepath.Join(w.LatticeDir, "lattice.json") }

// GraphShardDir returns the directory holding knowledge-graph shards.
func (w *Workspace) GraphShardDir() string { return filepath.Join(w.LatticeDir, "graph") }

// ImportDir returns the directory holding the brownfield import session
// (session.yaml, candidates.json, drafts).
func (w *Workspace) ImportDir() string { return filepath.Join(w.LatticeDir, "import") }

// MutationScoresPath returns the path to the committed mutation scores.
func (w *Workspace) MutationScoresPath() string {
	return filepath.Join(w.LatticeDir, "mutation-scores.json")
}

// CacheDir returns the gitignored runtime cache directory.
func (w *Workspace) CacheDir() string { return filepath.Join(w.LatticeDir, ".cache") }

// EmbeddingsDir returns the cache directory for semantic embeddings.
func (w *Workspace) EmbeddingsDir() string { return filepath.Join(w.CacheDir(), "embeddings") }

// SCIPDir returns the cache directory for SCIP indexes.
func (w *Workspace) SCIPDir() string { return filepath.Join(w.CacheDir(), "scip") }

// PrimaryCodeRoot returns the first available code root, or the first declared
// one when none are available.
func (w *Workspace) PrimaryCodeRoot() CodeRoot {
	for _, r := range w.CodeRoots {
		if r.Available {
			return r
		}
	}
	return w.CodeRoots[0]
}
