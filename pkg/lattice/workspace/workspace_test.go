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

func TestSourcesAndJudgments(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	writeLatticeDir(t, filepath.Join(root, "lattice"), `mode: standalone
judgments: rules
sources:
  - id: web
    repo: example/web
    clone: ~/src/web
    ref: origin/main
    catalogue: docs/flows/*.json
    prefix: fe
    tier: frontend
    system: shop
  - id: api
    path: ../data/api
    catalogue: "*.json"
    prefix: be
`)
	w, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(w.Sources) != 2 || w.Sources[0].ID != "web" || w.Sources[0].System != "shop" || w.Sources[1].Path != "../data/api" {
		t.Fatalf("sources = %+v", w.Sources)
	}
	if got, want := w.JudgmentsDir(), filepath.Join(w.LatticeDir, "rules"); got != want {
		t.Errorf("JudgmentsDir = %s want %s", got, want)
	}
	if got, want := w.ResolvePath("../data/api"), filepath.Join(root, "data", "api"); got != want {
		t.Errorf("ResolvePath relative = %s want %s", got, want)
	}
	home, _ := os.UserHomeDir()
	if got, want := w.ResolvePath("~/src/web"), filepath.Join(home, "src", "web"); got != want {
		t.Errorf("ResolvePath home = %s want %s", got, want)
	}
}

func TestJudgmentsDirDefault(t *testing.T) {
	root := t.TempDir()
	writeLatticeDir(t, filepath.Join(root, "lattice"), "")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := w.JudgmentsDir(), filepath.Join(w.LatticeDir, "judgments"); got != want {
		t.Errorf("JudgmentsDir = %s want %s", got, want)
	}
}

func TestStorage(t *testing.T) {
	root := t.TempDir()
	writeLatticeDir(t, filepath.Join(root, "lattice"), "mode: standalone\n")
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if w.Storage.Backend != "files" || w.Storage.Postgres.DSNEnv != "LATTICE_PG_DSN" || w.Storage.Postgres.Schema != "lattice" {
		t.Errorf("default storage = %+v", w.Storage)
	}

	root = t.TempDir()
	writeLatticeDir(t, filepath.Join(root, "lattice"), "mode: standalone\nstorage: {backend: postgres, postgres: {dsn_env: SHOP_PG, schema: shop_map}}\n")
	if w, err = Open(root); err != nil || w.Storage.Backend != "postgres" || w.Storage.Postgres.DSNEnv != "SHOP_PG" || w.Storage.Postgres.Schema != "shop_map" {
		t.Errorf("storage = %+v, %v", w.Storage, err)
	}

	for _, bad := range []string{"storage: {backend: s3}\n", "storage: {backend: postgres, postgres: {schema: \"x; drop\"}}\n"} {
		root = t.TempDir()
		writeLatticeDir(t, filepath.Join(root, "lattice"), bad)
		if _, err := Open(root); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

func TestExcludeGlobs(t *testing.T) {
	root := t.TempDir()
	writeLatticeDir(t, filepath.Join(root, "lattice"), "mode: embedded\nexclude:\n  - \"apps/legacy/**\"\n  - \"**/*.generated.ts\"\n")
	w, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(w.Exclude) != 2 || w.Exclude[0] != "apps/legacy/**" {
		t.Errorf("Exclude = %v", w.Exclude)
	}

	bad := t.TempDir()
	writeLatticeDir(t, filepath.Join(bad, "lattice"), "mode: embedded\nexclude:\n  - \"src/[\"\n")
	if _, err := Open(bad); err == nil {
		t.Error("an invalid exclude glob should fail to open")
	}
}
