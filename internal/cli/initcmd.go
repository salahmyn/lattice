package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/detect"
	"github.com/salahmyn/lattice/pkg/lattice/onboarding"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
	"github.com/salahmyn/lattice/skills"
)

type initResult struct {
	Path        string                `json:"path"`
	LatticeDir  string                `json:"lattice_dir"`
	Mode        string                `json:"mode"`
	Created     []string              `json:"created"`
	AlreadyInit bool                  `json:"already_initialized"`
	Detected    *detect.Detection     `json:"detected,omitempty"`
	Onboarding  *onboarding.State     `json:"onboarding,omitempty"`
}

func newInitCommand(io *IO) *cobra.Command {
	var standalone, noWizard bool
	var scopeFlag string
	cmd := &cobra.Command{
		Use:   "init [path]",
		Short: "Scaffold a Lattice workspace (with interactive wizard)",
		Long: `Creates a lattice/ directory holding every Lattice-maintained artifact.

In interactive mode (default when stdin is a TTY), runs the v0.5.0
wizard:
  1. auto-detect language + framework
  2. ask for scope (greenfield / brownfield-full / brownfield-incremental)
  3. write lattice/onboarding.yaml so the UI can pick up where the CLI
     left off (open with 'lattice serve' and visit /onboarding)

Use --no-wizard for the v0.4-era one-shot scaffold; use --scope to
pre-answer the scope question in non-interactive runs (CI).`,
		Args: cobra.MaximumNArgs(1),
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

			if !noWizard && !res.AlreadyInit {
				det := detect.Detect(path)
				res.Detected = &det

				scope := onboarding.Scope(scopeFlag)
				if scope == "" && isTerminal() {
					scope = promptScope(io, det)
				}
				state := onboarding.FromDetection(det, scope)
				// Default tone matches the v0.2.1 importer's default
				// audience contract — engineering-leaning but mixed-
				// reading-level. User can change in step 1 of the UI.
				state.ProjectMetadata = onboarding.ProjectMetadata{
					Title:            filepath.Base(filepath.Clean(path)),
					BusinessOwner:    gitConfigEmail(),
					EngineeringOwner: gitConfigEmail(),
					ToneAudience:     "mixed",
					ToneReadingLevel: "intermediate",
				}
				if err := onboarding.Save(res.LatticeDir, state); err != nil {
					return io.fail("ONBOARDING_SAVE_FAILED", err.Error(), nil)
				}
				res.Onboarding = &state
				res.Created = append(res.Created, "lattice/"+onboarding.FileName)
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
			if res.Detected != nil {
				io.printf("\nDetected:\n")
				renderDetection(io, *res.Detected)
			}
			if res.Onboarding != nil && !noWizard {
				io.printf("\nNext: finish setup in the browser\n")
				io.printf("  lattice serve            # then open http://127.0.0.1:7070/onboarding\n")
			} else {
				io.printf("\nNext: write a manifest under lattice/features/, then run `lattice validate`.\n")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&standalone, "standalone", false, "standalone workspace governing external code roots")
	cmd.Flags().BoolVar(&noWizard, "no-wizard", false, "skip the v0.5 onboarding wizard (just scaffold files)")
	cmd.Flags().StringVar(&scopeFlag, "scope", "",
		"pre-answer the wizard's scope question (greenfield|brownfield_full|brownfield_incremental)")
	return cmd
}

// promptScope asks the user which scope they want. Defaults to
// greenfield on a project that looks empty (low/none confidence
// detection); brownfield_full when detection is high-confidence.
func promptScope(io *IO, d detect.Detection) onboarding.Scope {
	def := onboarding.ScopeGreenfield
	if d.Confidence == "high" || d.Confidence == "medium" {
		def = onboarding.ScopeBrownfieldFull
	}
	io.printf("\nScope of this project?\n")
	io.printf("  1. greenfield               — I'll write BRDs/features as I build new things\n")
	io.printf("  2. brownfield_full          — generate BRDs/features from the existing code now\n")
	io.printf("  3. brownfield_incremental   — only track new BRDs/features from now on\n")
	io.printf("> default [%s]: ", def)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	switch line {
	case "1", "greenfield":
		return onboarding.ScopeGreenfield
	case "2", "brownfield_full":
		return onboarding.ScopeBrownfieldFull
	case "3", "brownfield_incremental":
		return onboarding.ScopeBrownfieldIncremental
	}
	return def
}

// isTerminal reports whether stdin is attached to a tty. Used to skip
// interactive prompts in CI / scripted runs.
func isTerminal() bool {
	st, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (st.Mode() & os.ModeCharDevice) != 0
}

// gitConfigEmail returns the user.email from git config, or "" if not
// set. Used as the default for owners in the wizard's metadata step.
func gitConfigEmail() string {
	out, err := exec.Command("git", "config", "user.email").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// fmt import is consumed via runtime construction of error messages
// in renderDetection (which lives in detect.go); silence the unused
// warning in builds where renderDetection happens not to need fmt.
var _ = fmt.Sprintf

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
