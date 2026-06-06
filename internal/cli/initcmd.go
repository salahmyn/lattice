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
	var scopeFlag, archFlag string
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

			// v0.7 — AMA architecture template. Opt-in: only fires
			// when the operator passes --architecture ama, and we
			// refuse on a workspace that was already initialized
			// (no clobbering existing config / src trees).
			if archFlag != "" && !res.AlreadyInit {
				if archFlag != "ama" {
					return io.fail("UNKNOWN_ARCHITECTURE",
						"architecture "+archFlag+" not supported (currently only: ama)", nil)
				}
				added, ferr := scaffoldAMA(path, res.LatticeDir)
				if ferr != nil {
					return io.fail("AMA_SCAFFOLD_FAILED", ferr.Error(), nil)
				}
				res.Created = append(res.Created, added...)
			}

			if !noWizard && !res.AlreadyInit {
				det := detect.Detect(path)
				res.Detected = &det

				scope := onboarding.Scope(scopeFlag)
				if scope == "" && isTerminal() {
					scope = promptScope(io, det)
				}
				// v0.7 — for greenfield, prompt for architecture
				// when not pre-answered. Brownfield never auto-converts
				// (the design doc is explicit), so we only ask greenfield.
				if archFlag == "" && scope == onboarding.ScopeGreenfield && isTerminal() {
					if promptArchitecture(io) {
						if added, ferr := scaffoldAMA(path, res.LatticeDir); ferr == nil {
							res.Created = append(res.Created, added...)
						}
					}
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
	cmd.Flags().StringVar(&archFlag, "architecture", "",
		"opt into a project architecture (currently: ama — AI-Agentic Modular Architecture)")
	return cmd
}

// scaffoldAMA writes the v0.7 AMA folder layout and architecture
// metadata: src/Core/Contracts/ + src/Features/ skeletons, an
// `architecture/ama.md` cheat-sheet, and an `architecture` block
// appended to lattice/config.yaml flipping ama_mode on.
//
// Safe by construction: never overwrites an existing file. If the
// operator has already started writing code into src/, the
// skeleton READMEs are skipped and only the lattice/-side bits
// are added.
func scaffoldAMA(root, latticeDir string) ([]string, error) {
	var created []string

	// Code-side skeletons. These are README placeholders that
	// describe the AMA invariants; they're harmless to commit and
	// give an AI agent the AMA rules without re-reading docs.
	codeFiles := map[string]string{
		filepath.Join(root, "src", "Core", "Contracts", "README.md"):  amaCoreContractsREADME,
		filepath.Join(root, "src", "Core", "SharedUtils", "README.md"): amaCoreSharedUtilsREADME,
		filepath.Join(root, "src", "Features", "README.md"):           amaFeaturesREADME,
	}
	for full, content := range codeFiles {
		if _, err := os.Stat(full); err == nil {
			continue // never clobber
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return created, err
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return created, err
		}
		rel, _ := filepath.Rel(root, full)
		created = append(created, rel)
	}

	// Lattice-side architecture doc.
	archDir := filepath.Join(latticeDir, "architecture")
	if err := os.MkdirAll(archDir, 0o755); err != nil {
		return created, err
	}
	archDoc := filepath.Join(archDir, "ama.md")
	if _, err := os.Stat(archDoc); err != nil {
		if err := os.WriteFile(archDoc, []byte(amaArchitectureMD), 0o644); err != nil {
			return created, err
		}
		rel, _ := filepath.Rel(root, archDoc)
		created = append(created, rel)
	}

	// Append the architecture block to config.yaml so the v0.7
	// structural checks fire as errors. We append rather than
	// rewriting the whole file — keeps the config edit a
	// single discoverable diff if the operator already changed
	// other settings.
	cfgPath := filepath.Join(latticeDir, "config.yaml")
	cfgBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		return created, err
	}
	if !strings.Contains(string(cfgBytes), "\narchitecture:") {
		merged := strings.TrimRight(string(cfgBytes), "\n") + "\n\n" + amaConfigBlock
		if err := os.WriteFile(cfgPath, []byte(merged), 0o644); err != nil {
			return created, err
		}
		// We're modifying config.yaml in place, not creating it;
		// reflect that in the listing for honest output.
		created = append(created, "lattice/config.yaml (architecture block appended)")
	}
	return created, nil
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

// promptArchitecture asks the operator whether to scaffold the AMA
// folder layout + enforcement. Defaults to no — AMA is opinionated
// and shouldn't surprise someone who just typed `lattice init`.
// Returns true only on an explicit "y"/"yes"/"ama".
func promptArchitecture(io *IO) bool {
	io.printf("\nArchitecture (greenfield only):\n")
	io.printf("  1. none  — scaffold no opinion about how code is laid out\n")
	io.printf("  2. ama   — AI-Agentic Modular Architecture (vertical slices,\n")
	io.printf("             ≤500-word .ai-spec.md per feature, line caps enforced)\n")
	io.printf("> default [none]: ")
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	switch strings.TrimSpace(strings.ToLower(line)) {
	case "2", "ama":
		return true
	}
	return false
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

// --- v0.7 AMA scaffold templates ---------------------------------
//
// These are committed-to-the-tree skeletons rather than runtime
// generators so an operator (or an AI agent) can read them with
// no Lattice knowledge. AMA's whole point is "the codebase tells
// you the rules"; the READMEs follow that ethos.

const amaCoreContractsREADME = `# Core/Contracts

Public, system-wide interfaces. Anything a Feature needs to refer
to outside its own folder lives here as a typed contract.

**AMA rule:** Features can only depend on ` + "`Core/*`" + ` — never on
each other directly. Cross-feature communication runs through the
event mediator.
`

const amaCoreSharedUtilsREADME = `# Core/SharedUtils

Low-level primitives: date formatting, math helpers, string
utilities. No business logic. No I/O.

**AMA rule:** Pure functions only. If you find yourself reaching
for a database, an HTTP client, or framework magic in here, the
code belongs in a Feature instead.
`

const amaFeaturesREADME = `# Features

One folder per business capability — the **vertical slice**.

Each feature folder is self-contained. An AI agent can load
exactly one Features/X/ directory and have 100% of the context
it needs to act on X.

## Required files per feature

	Features/CapturePayment/
	├── .ai-spec.md          # ≤500 words — generated by ` + "`lattice feature spec`" + `
	├── RequestDTO.<ext>     # Input contract (strict primitives or sub-DTOs)
	├── IntentHandler.<ext>  # Single entrypoint, ≤80 lines, no inline SQL
	├── DomainLogic.<ext>    # Pure functions only
	└── Component.test.<ext> # Tests with mocked buses

## Architectural invariants (enforced by ` + "`lattice validate`" + `)

- ` + "`CROSS_FEATURE_IMPORT`" + ` — feature A's symbols cannot import
  anything from feature B's namespace.
- ` + "`FEATURE_NOT_COLOCATED`" + ` — all of a feature's code must live
  under one top-level directory.
- ` + "`FILE_LINE_CAP`" + ` (default 150) and ` + "`METHOD_LINE_CAP`" + ` (default
  25) — fight scope creep at lint time.
- ` + "`MIXED_COMMAND_QUERY`" + ` — every capability must classify itself
  as ` + "`command`" + ` (state writer) or ` + "`query`" + ` (state reader).
`

const amaArchitectureMD = `# AMA — AI-Agentic Modular Architecture

This project is configured for AMA: an opinionated, structurally
enforced design philosophy that treats the **AI Agent as the
primary consumer of the codebase**.

## Why

Traditional architectures assume a human (or a 1M-token LLM context)
can hold the whole graph in mind. AMA inverts that: every feature
is self-contained and ≤500 words of spec, so an ultra-small fast
agent can act on one slice in isolation.

## The four pillars

1. **Vertical Slices over Layered Horizons.** A folder per business
   capability holds *everything* needed to fulfil it.
2. **Isolated Micro-Scopes.** Each feature is a microservice in shape
   (explicit boundary, no shared mutable state) but compiles into one
   binary — no network overhead.
3. **Deterministic Data Contracts.** Inputs and outputs are immutable
   DTOs or primitives. No ORM entities crossing feature boundaries.
4. **Asynchronous, Decoupled Orchestration.** Features publish events;
   they never call each other directly.

## The five Lattice-enforced rules

| Code | Severity | What it catches |
|---|---|---|
| ` + "`CROSS_FEATURE_IMPORT`" + ` | error (ama_mode) | Direct cross-feature reference |
| ` + "`FEATURE_NOT_COLOCATED`" + ` | warning | Feature's code spans >1 top-level dir |
| ` + "`FILE_LINE_CAP`" + ` | warning | File > ` + "`config.architecture.file_line_cap`" + ` |
| ` + "`METHOD_LINE_CAP`" + ` | warning | Method > ` + "`config.architecture.method_line_cap`" + ` |
| ` + "`MIXED_COMMAND_QUERY`" + ` | warning (ama_mode) | Capability is ` + "`mixed`" + ` — pick command or query |

Run ` + "`lattice validate`" + ` to see them. Run ` + "`lattice feature spec <id>`" + `
to render the ` + "`.ai-spec.md`" + ` for one feature.

## The greenfield invariant

All new business requirements live under ` + "`src/Features/`" + ` as
autonomous vertical slices. ` + "`src/Core/`" + ` is for system-wide
contracts only — never business logic.
`

const amaConfigBlock = `# v0.7 — AMA architecture enforcement (lattice init --architecture ama)
architecture:
  ama_mode: true             # escalate CROSS_FEATURE_IMPORT to error, fire MIXED_COMMAND_QUERY
  file_line_cap: 150         # AMA spec §5
  method_line_cap: 25        # AMA spec §5
`
