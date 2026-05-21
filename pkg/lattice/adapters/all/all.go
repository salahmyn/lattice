// Package all wires every built-in language adapter into a registry. It is a
// separate package so the core `adapters` package stays free of import cycles
// (each adapter imports `adapters`).
package all

import (
	"github.com/salahmyn/lattice/pkg/lattice/adapters"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/php"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/python"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/typescript"
	"github.com/salahmyn/lattice/pkg/lattice/config"
)

// Registry returns a registry containing every built-in adapter that is
// enabled in the given adapters configuration.
func Registry(cfg config.AdaptersConfig) *adapters.Registry {
	r := adapters.NewRegistry()
	if cfg.IsEnabled("python") {
		r.Register(python.New())
	}
	if cfg.IsEnabled("typescript") {
		r.Register(typescript.New())
	}
	if cfg.IsEnabled("php") {
		r.Register(php.New())
	}
	return r
}

// All returns a registry with every built-in adapter, ignoring config.
func All() *adapters.Registry {
	return adapters.NewRegistry(python.New(), typescript.New(), php.New())
}
