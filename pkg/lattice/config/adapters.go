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
}

// DefaultAdapters returns all three v1.0 adapters enabled.
func DefaultAdapters() AdaptersConfig {
	return AdaptersConfig{Adapters: map[string]AdapterSettings{
		"python":     {Enabled: true},
		"typescript": {Enabled: true},
		"php":        {Enabled: true},
	}}
}

// IsEnabled reports whether the named adapter should run. Unknown adapters
// default to enabled so a repo need not list every language.
func (a AdaptersConfig) IsEnabled(name string) bool {
	s, ok := a.Adapters[name]
	if !ok {
		return true
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
