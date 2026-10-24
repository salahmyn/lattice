package golang_test

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/all"
	"github.com/salahmyn/lattice/pkg/lattice/config"
)

// TestRegisteredByDefault checks the Go adapter is on by default, like the
// other language adapters, and can be switched off in adapters.yaml.
func TestRegisteredByDefault(t *testing.T) {
	if a := all.Registry(config.DefaultAdapters()).For("cmd/tool/main.go"); a == nil || a.Name() != "go" {
		t.Fatalf("default registry: .go handled by %v, want the go adapter", a)
	}
	if a := all.All().ByName("go"); a == nil {
		t.Fatal("all.All() has no go adapter")
	}
	off := config.DefaultAdapters()
	off.Adapters["go"] = config.AdapterSettings{Enabled: false}
	if a := all.Registry(off).For("main.go"); a != nil {
		t.Errorf("disabled go adapter still handles .go: %v", a.Name())
	}
}
