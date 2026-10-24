package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// AdaptersConfig is the .lattice/adapters.yaml model. It controls which
// language adapters are active and lets a repo override the default indexer
// and mutation-runner commands.
type AdaptersConfig struct {
	Adapters map[string]AdapterSettings `yaml:"adapters"`
}

// AdapterSettings holds per-language overrides.
type AdapterSettings struct {
	Enabled         bool     `yaml:"enabled"`
	IndexerCommand  []string `yaml:"indexer_command,omitempty"`
	MutationCommand []string `yaml:"mutation_command,omitempty"`
	// Migrations and Tests (postgres) are slash-path globs, relative to the
	// code root, of migration files and pgTAP test files.
	Migrations []string `yaml:"migrations,omitempty"`
	Tests      []string `yaml:"tests,omitempty"`
}

// optInAdapters run only when adapters.yaml enables them explicitly: a
// repository's .sql files are often seed data or dumps, not migrations.
var optInAdapters = map[string]bool{"postgres": true}

// DefaultAdapters returns the built-in language adapters enabled: python,
// typescript, php and go. Opt-in adapters (postgres) are not listed.
func DefaultAdapters() AdaptersConfig {
	return AdaptersConfig{Adapters: map[string]AdapterSettings{
		"python":     {Enabled: true},
		"typescript": {Enabled: true},
		"php":        {Enabled: true},
		"go":         {Enabled: true},
	}}
}

// IsEnabled reports whether the named adapter should run. Unknown adapters
// default to enabled so a repo need not list every language.
func (a AdaptersConfig) IsEnabled(name string) bool {
	s, ok := a.Adapters[name]
	if !ok {
		return !optInAdapters[name]
	}
	return s.Enabled
}

// LoadAdapters reads adapters.yaml from the lattice/ directory, falling back
// to DefaultAdapters.
func LoadAdapters(latticeDir string) (AdaptersConfig, error) {
	cfg := DefaultAdapters()
	path := filepath.Join(latticeDir, AdaptersFile)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}
