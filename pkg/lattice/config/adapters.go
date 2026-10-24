package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
	// Strict: an adapter block at the top level instead of under adapters:,
	// or a misspelt adapter name, silently changes what is scanned.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && err != io.EOF {
		return cfg, fmt.Errorf("%s: %w (adapters go under `adapters:`, e.g. adapters: {postgres: {enabled: true}})", path, err)
	}
	for name := range cfg.Adapters {
		if !knownAdapter(name) {
			return cfg, fmt.Errorf("%s: unknown adapter %q (known: %s)", path, name, strings.Join(KnownAdapters(), ", "))
		}
	}
	return cfg, nil
}

// KnownAdapters lists the adapter names adapters.yaml accepts.
func KnownAdapters() []string {
	seen := map[string]bool{}
	for name := range DefaultAdapters().Adapters {
		seen[name] = true
	}
	for name := range optInAdapters {
		seen[name] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func knownAdapter(name string) bool {
	_, def := DefaultAdapters().Adapters[name]
	return def || optInAdapters[name]
}
