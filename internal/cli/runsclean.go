package cli

import (
	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/ledger"
	"github.com/salahmyn/lattice/pkg/lattice/runsclean"
)

// newRunsCleanCommand exposes the V0 runs-clean gate ("gate zero"): from
// a clean state the app installs, builds, boots, and answers its smoke
// probes. A green test suite atop an app that won't start is a critical
// finding, not progress — run this at the end of every slice and before
// reporting anything demonstrated.
func newRunsCleanCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "runs-clean",
		Aliases: []string{"v0"},
		Short:   "Gate zero: clean install → build → boot → smoke probes (V0)",
		Long: `Executes the workspace's runtime: config block in order:

  clean_install   restore dependencies from the lockfile in a fresh state
  build           compile the application
  boot            start the app; it must stay up through the boot window
  probes          HTTP smoke checks against the booted app

Nothing in a workspace is demonstrated while V0 fails. Exit code 1 on
any failing step; configure the block in lattice/config.yaml:

  runtime:
    clean_install: "npm ci"
    build: "npm run build"
    boot: "npm start"
    boot_wait_ms: 5000
    probes:
      - { url: "http://localhost:3000/health", expect_status: 200 }`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("WORKSPACE_FAILED", err.Error(), nil)
			}
			cfg, err := config.Load(ws.LatticeDir)
			if err != nil {
				return io.fail("CONFIG_FAILED", err.Error(), nil)
			}
			if !cfg.Runtime.Configured() {
				return io.fail("RUNTIME_UNCONFIGURED",
					"no runtime: block in lattice/config.yaml — V0 needs the project's own install/build/boot commands",
					map[string]interface{}{
						"next_action": "add a runtime: block (see lattice runs-clean --help)",
					})
			}

			rep := runsclean.Run(cmd.Context(), io.Repo, cfg.Runtime)
			verdict := "pass"
			if !rep.Pass {
				verdict = "fail"
			}
			appendLedgerEvent(io, ws, ledger.EventCheckRun, "workspace", "runs-clean (V0): "+verdict)
			if io.JSON {
				if err := io.printJSON(rep); err != nil {
					return err
				}
			} else {
				io.printf("V0 runs-clean @ %s\n", io.Repo)
				for _, s := range rep.Steps {
					status := "PASS"
					switch {
					case s.Skipped:
						status = "skip"
					case !s.OK:
						status = "FAIL"
					}
					io.printf("  %-8s %-5s %s\n", s.Step, status, s.Command)
					if s.Detail != "" && !s.OK && !s.Skipped {
						io.printf("           %s\n", s.Detail)
					}
				}
			}
			if !rep.Pass {
				return io.fail("V0_FAILED",
					"the application does not run clean — nothing is demonstrated until it does", nil)
			}
			return nil
		},
	}
	return cmd
}
