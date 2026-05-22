package cli

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

type migrateResult struct {
	Path    string   `json:"path"`
	Moved   []string `json:"moved"`
	Skipped []string `json:"skipped"`
	Done    bool     `json:"done"`
}

// flatMove is one source->destination relocation in the v0.1.0 -> v0.1.1
// layout migration.
type flatMove struct {
	from string
	to   string
}

func newMigrateCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "migrate [path]",
		Short: "Migrate a v0.1.0 flat-layout repository into the lattice/ directory",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path := io.Repo
			if len(args) == 1 {
				path = args[0]
			}
			res, err := migrate(path)
			if err != nil {
				return io.fail("MIGRATE_FAILED", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(res)
			}
			if !res.Done {
				io.printf("Nothing to migrate: %s has no v0.1.0 flat layout.\n", path)
				return nil
			}
			io.printf("Migrated %s to the lattice/ layout\n", path)
			for _, m := range res.Moved {
				io.printf("  moved %s\n", m)
			}
			io.printf("\nReview lattice/workspace.yaml, then run `lattice validate`.\n")
			return nil
		},
	}
}

// migrate relocates a flat-layout repository into lattice/.
func migrate(root string) (migrateResult, error) {
	res := migrateResult{Path: root}
	latticeDir := filepath.Join(root, workspace.Dir)

	flatConfig := filepath.Join(root, ".lattice", "config.yaml")
	if _, err := os.Stat(flatConfig); err != nil {
		return res, nil // no flat layout to migrate
	}
	if _, err := os.Stat(filepath.Join(latticeDir, "config.yaml")); err == nil {
		return res, nil // already on the new layout
	}

	if err := os.MkdirAll(latticeDir, 0o755); err != nil {
		return res, err
	}

	moves := []flatMove{
		{".lattice/config.yaml", "lattice/config.yaml"},
		{".lattice/adapters.yaml", "lattice/adapters.yaml"},
		{".lattice/mcp.yaml", "lattice/mcp.yaml"},
		{".lattice/mutation-scores.json", "lattice/mutation-scores.json"},
		{".lattice/skills", "lattice/skills"},
		{".lattice/views", "lattice/views"},
		{".lattice/embeddings", "lattice/.cache/embeddings"},
		{".lattice/scip", "lattice/.cache/scip"},
		{"features", "lattice/features"},
		{"work/initiatives", "lattice/initiatives"},
		{"decisions", "lattice/decisions"},
		{"schemas", "lattice/schemas"},
		{"lattice.json", "lattice/lattice.json"},
	}

	for _, m := range moves {
		src := filepath.Join(root, filepath.FromSlash(m.from))
		dst := filepath.Join(root, filepath.FromSlash(m.to))
		if _, err := os.Stat(src); err != nil {
			res.Skipped = append(res.Skipped, m.from)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return res, err
		}
		if err := os.Rename(src, dst); err != nil {
			return res, err
		}
		res.Moved = append(res.Moved, m.from+" -> "+m.to)
	}

	// Drop the now-empty legacy directories.
	_ = os.RemoveAll(filepath.Join(root, ".lattice"))
	_ = os.Remove(filepath.Join(root, "work", "initiatives"))
	_ = os.Remove(filepath.Join(root, "work"))

	// Write a workspace.yaml and refresh .gitignore.
	wsPath := filepath.Join(latticeDir, "workspace.yaml")
	if _, err := os.Stat(wsPath); err != nil {
		if err := os.WriteFile(wsPath, []byte(embeddedWorkspaceYAML), 0o644); err != nil {
			return res, err
		}
		res.Moved = append(res.Moved, "(created) lattice/workspace.yaml")
	}
	_ = os.WriteFile(filepath.Join(root, ".gitignore"), []byte(defaultGitignore), 0o644)

	res.Done = true
	return res, nil
}
