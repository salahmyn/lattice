package schema

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ArchitectureContext declares the C4 Level-1 (System Context) elements that
// cannot be derived from code: the people who use the system and the external
// systems it integrates with. It is hand-authored at lattice/context.yaml.
type ArchitectureContext struct {
	// System overrides the displayed software-system name (default: the
	// project directory name).
	System          string           `yaml:"system,omitempty"`
	Description     string           `yaml:"description,omitempty"`
	Actors          []Actor          `yaml:"actors,omitempty"`
	ExternalSystems []ExternalSystem `yaml:"external_systems,omitempty"`
}

// Actor is a person (user role) who interacts with the system.
type Actor struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	// Uses lists the component or feature ids this actor interacts with.
	Uses []string `yaml:"uses,omitempty"`
}

// ExternalSystem is a third-party system the system integrates with.
type ExternalSystem struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	// UsedBy lists components that call this external system; Uses lists
	// components this external system calls into.
	UsedBy []string `yaml:"used_by,omitempty"`
	Uses   []string `yaml:"uses,omitempty"`
}

// LoadContext reads lattice/context.yaml. A missing file is not an error: an
// empty context is returned (the C4 view then approximates Level 1).
func LoadContext(path string) (ArchitectureContext, error) {
	var ctx ArchitectureContext
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ctx, nil
	}
	if err != nil {
		return ctx, err
	}
	if err := yaml.Unmarshal(data, &ctx); err != nil {
		return ctx, fmt.Errorf("%s: %w", path, err)
	}
	return ctx, nil
}
