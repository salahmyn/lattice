package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// writeLatticeDir creates a minimal lattice/ directory at dir.
func writeLatticeDir(t *testing.T, dir, workspaceYAML string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if workspaceYAML != "" {
		if err := os.WriteFile(filepath.Join(dir, "workspace.yaml"), []byte(workspaceYAML), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenEmbedded(t *testing.T) {
	root := t.TempDir()
	writeLatticeDir(t, filepath.Join(root, "lattice"), "")
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}

	w, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if w.Mode != ModeEmbedded {
		t.Errorf("mode = %q want embedded", w.Mode)
	}
	if w.Review {
		t.Error("embedded workspace with present code should not be in review mode")
	}
	if w.FeaturesDir() != filepath.Join(root, "lattice", "features") {
		t.Errorf("FeaturesDir = %q", w.FeaturesDir())
	}
}

func TestOpenStandaloneReviewMode(t *testing.T) {
	root := t.TempDir()
	// A standalone lattice/ repo whose declared code root does not exist.
	writeLatticeDir(t, root, "mode: standalone\ncode_roots:\n  - name: api\n    path: ../missing-api\n")

	w, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if w.Mode != ModeStandalone {
		t.Errorf("mode = %q want standalone", w.Mode)
	}
	if !w.Review {
		t.Error("standalone workspace with a missing code root should be in review mode")
	}
}

func TestOpenNotInitialized(t *testing.T) {
	if _, err := Open(t.TempDir()); err == nil {
		t.Error("expected an error opening a directory with no lattice/")
	}
}
