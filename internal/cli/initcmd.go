package cli

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/skills"
)

type initResult struct {
	Path        string   `json:"path"`
	Created     []string `json:"created"`
	AlreadyInit bool     `json:"already_initialized"`
}

func newInitCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "init [path]",
		Short: "Scaffold a Lattice repository layout",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path := io.Repo
			if len(args) == 1 {
				path = args[0]
			}
			res, err := scaffold(path)
			if err != nil {
				return io.fail("INIT_FAILED", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(res)
			}
			if res.AlreadyInit {
				io.printf("Already initialized: %s\n", filepath.Join(path, config.Dir))
				return nil
			}
			io.printf("Initialized Lattice repository at %s\n", path)
			for _, c := range res.Created {
				io.printf("  + %s\n", c)
			}
			io.printf("\nNext: write a manifest under features/, then run `lattice validate`.\n")
			return nil
		},
	}
}

// scaffoldDirs are the directories `lattice init` creates.
var scaffoldDirs = []string{
	".lattice",
	".lattice/embeddings",
	".lattice/scip",
	".lattice/skills",
	".lattice/skills/lattice",
	".lattice/views",
	"features",
	"work/initiatives",
	"decisions",
	"schemas",
	"src",
	"tests",
}

func scaffold(root string) (initResult, error) {
	res := initResult{Path: root}

	if st, err := os.Stat(filepath.Join(root, config.Dir, config.ConfigFile)); err == nil && !st.IsDir() {
		res.AlreadyInit = true
		return res, nil
	}

	for _, d := range scaffoldDirs {
		full := filepath.Join(root, filepath.FromSlash(d))
		if err := os.MkdirAll(full, 0o755); err != nil {
			return res, err
		}
		res.Created = append(res.Created, d+"/")
	}

	files := map[string]string{
		filepath.Join(config.Dir, config.ConfigFile):      defaultConfigYAML,
		filepath.Join(config.Dir, config.AdaptersFile):    defaultAdaptersYAML,
		filepath.Join(config.Dir, config.MCPFile):         defaultMCPYAML,
		filepath.Join(config.Dir, "mutation-scores.json"): "{}\n",
		".gitignore": defaultGitignore,
	}
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if _, err := os.Stat(full); err == nil {
			continue // never clobber
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return res, err
		}
		res.Created = append(res.Created, rel)
	}

	// Copy the shipped agent skills so they version-control with the project.
	skillsDir := filepath.Join(root, config.Dir, "skills")
	if err := skills.ExportAll(skillsDir); err != nil {
		return res, err
	}
	res.Created = append(res.Created, ".lattice/skills/lattice/ (8 shipped skills)")
	return res, nil
}

const defaultConfigYAML = `# Lattice configuration. See the design doc, section 30 and 23.

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

const defaultGitignore = `# Lattice runtime artifacts
.lattice/embeddings/
.lattice/scip/
`
