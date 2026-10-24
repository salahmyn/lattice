// Package all wires every built-in language adapter into a registry. It is a
// separate package so the core `adapters` package stays free of import cycles
// (each adapter imports `adapters`).
package all

// @module-feature adapters

import (
	"github.com/salahmyn/lattice/pkg/lattice/adapters"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/golang"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/php"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/postgres"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/python"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/typescript"
	"github.com/salahmyn/lattice/pkg/lattice/config"
)

// Registry returns a registry containing every built-in adapter that is
// enabled in the given adapters configuration.
// @enforces adapters:INV-2
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
	if cfg.IsEnabled(golang.Name) {
		r.Register(golang.New())
	}
	if cfg.IsEnabled(postgres.Name) {
		s := cfg.Adapters[postgres.Name]
		r.Register(postgres.New(s.Migrations, s.Tests))
	}
	return r
}

// All returns a registry with every built-in adapter, ignoring config
// (postgres included, though a workspace must opt in to run it).
func All() *adapters.Registry {
	return adapters.NewRegistry(python.New(), typescript.New(), php.New(), golang.New(), postgres.New(nil, nil))
}
