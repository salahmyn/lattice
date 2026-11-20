package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Lattice describes its own CLI: each top-level command is a declared entry
// point, so the call graph roots at its handler and the features its flow
// names read as reached. A command added without a manifest leaves its code
// reachable only through func main, and lattice check then reports that code
// as reached by no entry point.
func TestEveryCommandIsAnEntryPoint(t *testing.T) {
	dir := filepath.Join("..", "..", "lattice", "entry-points")
	for _, cmd := range NewRootCommand().Commands() {
		name := cmd.Name()
		data, err := os.ReadFile(filepath.Join(dir, "ep.cli."+name+".yaml"))
		if err != nil {
			t.Errorf("command %q has no entry-point manifest: %v", name, err)
			continue
		}
		if !strings.Contains(string(data), "command: lattice "+name+"\n") {
			t.Errorf("ep.cli.%s.yaml: trigger is not `lattice %s`", name, name)
		}
	}
}
