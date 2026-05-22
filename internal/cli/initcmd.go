package cli

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/workspace"
	"github.com/salahmyn/lattice/skills"
)

type initResult struct {
	Path        string   `json:"path"`
	LatticeDir  string   `json:"lattice_dir"`
	Mode        string   `json:"mode"`
	Created     []string `json:"created"`
	AlreadyInit bool     `json:"already_initialized"`
}

func newInitCommand(io *IO) *cobra.Command {
	var standalone bool
	cmd := &cobra.Command{
		Use:   "init [path]",
		Short: "Scaffold a Lattice workspace",
		Long:  "Creates a lattice/ directory holding every Lattice-maintained artifact.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path := io.Repo
			if len(args) == 1 {
				path = args[0]
			}
			mode := workspace.ModeEmbedded
			if standalone {
				mode = workspace.ModeStandalone
			}
			res, err := scaffold(path, mode)
			if err != nil {
				return io.fail("INIT_FAILED", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(res)
			}
			if res.AlreadyInit {
				io.printf("Already initialized: %s\n", res.LatticeDir)
				return nil
			}
			io.printf("Initialized Lattice workspace (%s mode) at %s\n", res.Mode, res.LatticeDir)
			for _, c := range res.Created {
				io.printf("  + %s\n", c)
			}
			io.printf("\nNext: write a manifest under lattice/features/, then run `lattice validate`.\n")
			return nil
		},
	}
	cmd.Flags().BoolVar(&standalone, "standalone", false, "standalone workspace governing external code roots")
	return cmd
}

// scaffoldDirs are the directories `lattice init` creates under lattice/.
var scaffoldDirs = []string{
	"features",
	"initiatives",
	"decisions",
	"schemas",
	"views",
	"skills",
	".cache/embeddings",
	".cache/scip",
}

func scaffold(root string, mode workspace.Mode) (initResult, error) {
	latticeDir := filepath.Join(root, workspace.Dir)
	res := initResult{Path: root, LatticeDir: latticeDir, Mode: string(mode)}

	if st, err := os.Stat(filepath.Join(latticeDir, "config.yaml")); err == nil && !st.IsDir() {
		res.AlreadyInit = true
		return res, nil
	}

	if err := os.MkdirAll(latticeDir, 0o755); err != nil {
		return res, err
	}
	res.Created = append(res.Created, "lattice/")
	for _, d := range scaffoldDirs {
		if err := os.MkdirAll(filepath.Join(latticeDir, filepath.FromSlash(d)), 0o755); err != nil {
			return res, err
		}
		res.Created = append(res.Created, "lattice/"+d+"/")
	}

	workspaceYAML := embeddedWorkspaceYAML
	if mode == workspace.ModeStandalone {
		workspaceYAML = standaloneWorkspaceYAML
	}
	files := map[string]string{
		filepath.Join(latticeDir, "config.yaml"):          defaultConfigYAML,
		filepath.Join(latticeDir, "adapters.yaml"):        defaultAdaptersYAML,
		filepath.Join(latticeDir, "mcp.yaml"):             defaultMCPYAML,
		filepath.Join(latticeDir, "workspace.yaml"):       workspaceYAML,
		filepath.Join(latticeDir, "context.yaml"):         defaultContextYAML,
		filepath.Join(latticeDir, "mutation-scores.json"): "{}\n",
		filepath.Join(root, ".gitignore"):                 defaultGitignore,
	}
	for full, content := range files {
		if _, err := os.Stat(full); err == nil {
			continue // never clobber
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return res, err
		}
		rel, _ := filepath.Rel(root, full)
		res.Created = append(res.Created, rel)
	}

	// Copy the shipped agent skills so they version-control with the project.
	if err := skills.ExportAll(filepath.Join(latticeDir, "skills")); err != nil {
		return res, err
	}
	res.Created = append(res.Created, "lattice/skills/lattice/ (8 shipped skills)")
	return res, nil
}

const defaultConfigYAML = `# Lattice configuration.

agentic:
  llm:
    enabled: false           # set true to enable LLM-backed capabilities
    provider: anthropic      # anthropic | openai | ollama
    model: claude-sonnet-4-7
    api_key_env: ANTHROPIC_API_KEY
    base_url: ""
    timeout: 30s
    max_tokens: 2000

mutation_testing:
  enabled: false
  scope: invariant_enforcers
  thresholds:
    default: 80
    overrides: {}
  timeout_per_mutant: 60s

analysis:
  similarity_warn_threshold: 0.75
  similarity_duplicate_threshold: 0.9

subprocess:
  default_timeout: 60s

scip:
  commit_indexes: false

decomposition:
  max_invariants: 20
  max_capabilities: 15
  max_surfaces: 8

knowledge:
  sharding:
    enabled: false              # split lattice.json into per-group shards
    strategy: by_feature_group  # by_feature_group | by_size
    max_features_per_shard: 200
`

const defaultAdaptersYAML = `# Language adapter activation.
adapters:
  python:
    enabled: true
  typescript:
    enabled: true
  php:
    enabled: true
`

const defaultMCPYAML = `# MCP server configuration (consumed by @salahmyn/mcp-server).
lattice_binary: lattice     # path to the lattice CLI
confirmation_policy: preview # preview | auto
tools:
  enabled: []                # empty = all tools enabled
`

const embeddedWorkspaceYAML = `# Lattice workspace.
#
# embedded: this lattice/ directory lives inside a single code repository.
# Lattice extracts source from the repository that contains lattice/.
mode: embedded
`

const standaloneWorkspaceYAML = `# Lattice workspace.
#
# standalone: this lattice/ directory is its own repository and governs the
# external code roots listed below. Useful for multi-repo projects and for
# giving PMs/QA access to meaning without access to code. When a code root is
# not present, Lattice runs in review mode (manifest-only validation).
mode: standalone
code_roots:
  - name: example-service
    path: ../example-service   # local path relative to lattice/, or absolute
    git: ""                    # optional clone URL, for documentation
`

const defaultContextYAML = `# C4 Level-1 (System Context): the people and external systems that the
# code cannot reveal. Consumed by ` + "`lattice view c4`" + `.
#
# system: ""                 # optional display name override
# actors:
#   - id: customer
#     name: Customer
#     description: A person buying products.
#     uses: [checkout]        # component / feature ids this actor interacts with
# external_systems:
#   - id: payment_gateway
#     name: Payment Gateway
#     description: Third-party card processor.
#     used_by: [checkout]     # components that call this external system
`

const defaultGitignore = `# Lattice runtime cache
lattice/.cache/
`
