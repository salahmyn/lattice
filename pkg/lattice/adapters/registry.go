package adapters

import (
	"path/filepath"
	"sort"
	"strings"
)

// Registry holds the active language adapters and dispatches files by
// extension.
type Registry struct {
	adapters []LanguageAdapter
	byExt    map[string]LanguageAdapter
}

// NewRegistry builds a registry from the given adapters. Later adapters win on
// extension collisions.
func NewRegistry(adapters ...LanguageAdapter) *Registry {
	r := &Registry{byExt: map[string]LanguageAdapter{}}
	for _, a := range adapters {
		r.Register(a)
	}
	return r
}

// Register adds an adapter to the registry.
func (r *Registry) Register(a LanguageAdapter) {
	r.adapters = append(r.adapters, a)
	for _, ext := range a.FileExtensions() {
		r.byExt[strings.ToLower(ext)] = a
	}
}

// For returns the adapter that handles path, or nil if none does.
func (r *Registry) For(path string) LanguageAdapter {
	if a, ok := r.byExt[strings.ToLower(filepath.Ext(path))]; ok {
		return a
	}
	return nil
}

// ByName returns the adapter with the given language name, or nil.
func (r *Registry) ByName(name string) LanguageAdapter {
	for _, a := range r.adapters {
		if a.Name() == name {
			return a
		}
	}
	return nil
}

// All returns every registered adapter, sorted by name for determinism.
func (r *Registry) All() []LanguageAdapter {
	out := make([]LanguageAdapter, len(r.adapters))
	copy(out, r.adapters)
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Names returns the sorted list of registered adapter names.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.adapters))
	for _, a := range r.adapters {
		names = append(names, a.Name())
	}
	sort.Strings(names)
	return names
}
