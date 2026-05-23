// Package entrypoints discovers and represents the triggers of a running
// system — HTTP routes, CLI commands, scheduled jobs, queue workers,
// event consumers — alongside the handler symbol each fires. It is the
// invocation-axis counterpart to the meaning-axis features Lattice has
// modelled since v0.1.
package entrypoints

import (
	"context"
	"sort"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// Detector is one framework's mapping from source to entry points. Each
// detector ships in its own subpackage (pkg/lattice/entrypoints/laravel,
// .../fastapi, .../express) and registers itself via Register.
type Detector interface {
	// Name identifies the detector ("laravel-http", "fastapi", ...) —
	// used for logging and the EntryPoint provenance.
	Name() string

	// Detect runs against the workspace's source files plus the
	// already-parsed IR modules (so detectors can resolve handler FQNs
	// against the symbol table without re-parsing). Detect is pure: it
	// reports findings, never writes files.
	Detect(ctx context.Context, ws *workspace.Workspace, modules []ir.Module) ([]schema.EntryPoint, error)
}

// registry holds every detector registered at init time. Detect runs the
// registry against a workspace and dedupes findings.
var registry []Detector

// Register adds a detector to the global registry. Detectors call this
// from an init() so importing the package is enough to enable them.
func Register(d Detector) { registry = append(registry, d) }

// All returns the registered detectors in deterministic order (by Name).
func All() []Detector {
	out := append([]Detector(nil), registry...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// DetectAll runs every registered detector against the workspace and
// returns the union of their findings, deduped on (kind, trigger,
// handler) so two frameworks emitting the same route appear once.
func DetectAll(ctx context.Context, ws *workspace.Workspace, modules []ir.Module) ([]schema.EntryPoint, error) {
	seen := map[string]bool{}
	var out []schema.EntryPoint
	for _, d := range All() {
		eps, err := d.Detect(ctx, ws, modules)
		if err != nil {
			return out, err
		}
		for _, ep := range eps {
			key := ep.Kind + "|" + triggerKey(ep.Trigger) + "|" + ep.Handler.Symbol
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, ep)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// triggerKey hashes the trigger to a stable string for dedup.
func triggerKey(t schema.Trigger) string {
	return t.Method + " " + t.Path + " " + t.Schedule + " " + t.Queue + " " + t.Event + " " + t.Command
}

// ResetForTest clears the registry. Test-only.
func ResetForTest() { registry = nil }
