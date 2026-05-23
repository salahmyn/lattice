package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/all"
	"github.com/salahmyn/lattice/pkg/lattice/config"
)

type doctorCheck struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Found       bool   `json:"found"`
	Detail      string `json:"detail"`
	InstallHint string `json:"install_hint,omitempty"`
}

type doctorReport struct {
	Checks   []doctorCheck `json:"checks"`
	OK       bool          `json:"ok"`
	LLMProbe *llmProbe     `json:"llm_probe,omitempty"`
}

// llmProbe is the v0.2.1 #7 addition: the verbatim result of a single
// tiny LLM round-trip. Lets a user diagnose a misconfigured provider
// once, instead of meeting the same error per-candidate during a 50-
// minute draft pass.
type llmProbe struct {
	OK          bool   `json:"ok"`
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	BaseURL     string `json:"base_url,omitempty"`
	Reply       string `json:"reply,omitempty"`
	Tokens      int    `json:"tokens_used,omitempty"`
	ElapsedMS   int64  `json:"elapsed_ms,omitempty"`
	Error       string `json:"error,omitempty"`
	Suggestion  string `json:"suggestion,omitempty"`
}

// installHints maps an optional tool to its install instruction.
var installHints = map[string]string{
	"scip-python":     "pip install scip-python",
	"scip-typescript": "npm install -g @sourcegraph/scip-typescript",
	"scip-php":        "composer global require sourcegraph/scip-php",
	"mutmut":          "pip install mutmut",
	"stryker":         "npm install --save-dev @stryker-mutator/core",
	"infection":       "composer require --dev infection/infection",
}

func newDoctorCommand(io *IO) *cobra.Command {
	var probeLLM bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check installed prerequisites",
		Long: `Reports which optional tools (SCIP indexers, mutation runners, LLM config)
are available. With --probe-llm, also sends a tiny test request to the
configured LLM and reports the verbatim provider response so a setup
problem (upgrade_required / unsupported_model / DNS failure) surfaces
once, not 55 times during a draft run.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report := runDoctor(io)
			if probeLLM {
				report.LLMProbe = probeLLMProvider(cmd.Context(), io)
			}
			if io.JSON {
				return io.printJSON(report)
			}
			renderDoctor(io, report)
			return nil
		},
	}
	cmd.Flags().BoolVar(&probeLLM, "probe-llm", false,
		"send a tiny test request via the configured LLM provider and report the actual response")
	return cmd
}

func runDoctor(io *IO) doctorReport {
	var checks []doctorCheck

	// Doctor works before `lattice init`: fall back to defaults if there is
	// no workspace yet.
	adCfg := config.DefaultAdapters()
	cfg := config.Default()
	codeRoot := io.Repo
	if ws, err := openWorkspace(io); err == nil {
		adCfg, _ = config.LoadAdapters(ws.LatticeDir)
		cfg, _ = config.Load(ws.LatticeDir)
		codeRoot = ws.PrimaryCodeRoot().Abs
	}
	reg := all.Registry(adCfg)

	for _, a := range reg.All() {
		// SCIP indexer.
		if cmd, err := a.SCIPIndexerCommand(codeRoot, ""); err == nil && len(cmd) > 0 {
			checks = append(checks, toolCheck("SCIP indexer ("+a.Name()+")", "scip", cmd[0], codeRoot))
		}
		// Mutation runner.
		if cmd, err := a.MutationRunnerCommand(codeRoot, []string{"_probe_"}); err == nil && len(cmd) > 0 {
			tool := cmd[0]
			if tool == "npx" && len(cmd) > 1 {
				tool = cmd[1]
			}
			checks = append(checks, toolCheck("Mutation runner ("+a.Name()+")", "mutation", tool, codeRoot))
		}
	}

	// LLM provider configuration.
	llm := cfg.Agentic.LLM
	llmCheck := doctorCheck{Name: "LLM provider", Category: "agentic"}
	switch {
	case !llm.Enabled:
		llmCheck.Found = true
		llmCheck.Detail = "disabled (agentic capabilities use deterministic fallbacks)"
	case os.Getenv(llm.APIKeyEnv) != "":
		llmCheck.Found = true
		llmCheck.Detail = "enabled: provider=" + llm.Provider + ", key from $" + llm.APIKeyEnv
	default:
		llmCheck.Found = false
		llmCheck.Detail = "enabled but $" + llm.APIKeyEnv + " is not set"
		llmCheck.InstallHint = "export " + llm.APIKeyEnv + "=<your-api-key>"
	}
	checks = append(checks, llmCheck)

	sort.SliceStable(checks, func(i, j int) bool {
		if checks[i].Category != checks[j].Category {
			return checks[i].Category < checks[j].Category
		}
		return checks[i].Name < checks[j].Name
	})

	ok := true
	for _, c := range checks {
		if !c.Found && c.Category == "agentic" {
			ok = false
		}
	}
	return doctorReport{Checks: checks, OK: ok}
}

// toolCheck resolves a tool by PATH lookup, or by repo-relative path when the
// tool name contains a separator (e.g. vendor/bin/infection).
func toolCheck(name, category, tool, repo string) doctorCheck {
	c := doctorCheck{Name: name, Category: category, InstallHint: installHints[filepath.Base(tool)]}
	if filepath.Base(tool) != tool {
		p := filepath.Join(repo, tool)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			c.Found = true
			c.Detail = "found: " + p
			return c
		}
		c.Detail = "not found at " + p
		return c
	}
	if path, err := exec.LookPath(tool); err == nil {
		c.Found = true
		c.Detail = "found: " + path
		return c
	}
	c.Detail = tool + " not on PATH"
	return c
}

func renderDoctor(io *IO, report doctorReport) {
	io.printf("Lattice environment check\n\n")
	for _, c := range report.Checks {
		mark := "x"
		if c.Found {
			mark = "ok"
		}
		io.printf("[%-2s] %-28s %s\n", mark, c.Name, c.Detail)
		if !c.Found && c.InstallHint != "" {
			io.printf("       install: %s\n", c.InstallHint)
		}
	}
	io.printf("\nOptional tools missing above are only needed for the features that use them.\n")
	if report.LLMProbe != nil {
		io.printf("\nLLM probe — %s / %s\n", report.LLMProbe.Provider, report.LLMProbe.Model)
		if report.LLMProbe.OK {
			io.printf("  [ok] reply in %dms (%d tokens): %s\n",
				report.LLMProbe.ElapsedMS, report.LLMProbe.Tokens,
				truncateString(report.LLMProbe.Reply, 80))
		} else {
			io.printf("  [x ] %s\n", report.LLMProbe.Error)
			if report.LLMProbe.Suggestion != "" {
				io.printf("       %s\n", report.LLMProbe.Suggestion)
			}
		}
	}
}

func truncateString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
