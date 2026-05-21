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
	Checks []doctorCheck `json:"checks"`
	OK     bool          `json:"ok"`
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
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check installed prerequisites",
		Long:  "Reports which optional tools (SCIP indexers, mutation runners, LLM config) are available.",
		RunE: func(_ *cobra.Command, _ []string) error {
			report := runDoctor(io.Repo)
			if io.JSON {
				return io.printJSON(report)
			}
			renderDoctor(io, report)
			return nil
		},
	}
}

func runDoctor(repo string) doctorReport {
	var checks []doctorCheck

	adCfg, _ := config.LoadAdapters(repo)
	reg := all.Registry(adCfg)

	for _, a := range reg.All() {
		// SCIP indexer.
		if cmd, err := a.SCIPIndexerCommand(repo); err == nil && len(cmd) > 0 {
			checks = append(checks, toolCheck("SCIP indexer ("+a.Name()+")", "scip", cmd[0], repo))
		}
		// Mutation runner.
		if cmd, err := a.MutationRunnerCommand(repo, []string{"_probe_"}); err == nil && len(cmd) > 0 {
			tool := cmd[0]
			if tool == "npx" && len(cmd) > 1 {
				tool = cmd[1]
			}
			checks = append(checks, toolCheck("Mutation runner ("+a.Name()+")", "mutation", tool, repo))
		}
	}

	// LLM provider configuration.
	cfg, _ := config.Load(repo)
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
}
