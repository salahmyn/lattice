package cli

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/detect"
)

// newDetectCommand exposes the v0.5.0 auto-detection: scan the project
// root, print the language + framework guess, the suggested code
// roots, and the packages Lattice needs to index the stack. With
// --install, runs each install command in a subprocess.
//
// The detection itself never touches disk beyond reads — it's the
// `--install` flag that mutates the world. We keep them separate so
// `lattice detect` is safe to run in CI / agents.
func newDetectCommand(io *IO) *cobra.Command {
	var install, dryRun bool
	cmd := &cobra.Command{
		Use:   "detect [path]",
		Short: "Auto-detect language/framework and required SCIP packages",
		Long: `Inspects the project root for manifest files (composer.json, package.json,
go.mod, etc.) and signature paths (artisan, manage.py, etc.) to guess the
primary language and framework. Prints suggested code roots and the
packages Lattice needs to index this stack.

With --install, runs each package-manager install command in a subprocess.
Use --dry-run with --install to see what would be executed without
running it.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := io.Repo
			if len(args) == 1 {
				path = args[0]
			}
			det := detect.Detect(path)
			if io.JSON {
				return io.printJSON(det)
			}
			renderDetection(io, det)

			if install && len(det.RequiredPackages) > 0 {
				io.printf("\nInstalling required packages:\n")
				for _, p := range det.RequiredPackages {
					cmdLine := detect.InstallCommand(p)
					if cmdLine == nil {
						io.printf("  ? %s (unknown manager %q — install manually)\n", p.Name, p.Manager)
						continue
					}
					full := strings.Join(cmdLine, " ")
					if dryRun {
						io.printf("  [dry-run] %s\n", full)
						continue
					}
					io.printf("  > %s\n", full)
					if err := runInstall(cmd.Context(), cmdLine); err != nil {
						io.errorf("    FAILED: %v\n", err)
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&install, "install", false, "run the required package install commands")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "with --install: print the commands without executing")
	return cmd
}

func renderDetection(io *IO, d detect.Detection) {
	io.printf("Project root: %s\n", d.Root)
	if d.Confidence == "none" {
		io.printf("\nNo recognised language or framework. Configure manually.\n")
		return
	}
	io.printf("Language:     %s\n", d.Language)
	if d.Framework != "" {
		io.printf("Framework:    %s\n", d.Framework)
	}
	io.printf("Confidence:   %s\n", d.Confidence)
	if len(d.Evidence) > 0 {
		io.printf("\nEvidence:\n")
		for _, e := range d.Evidence {
			io.printf("  - %s\n", e)
		}
	}
	if len(d.CodeRoots) > 0 {
		io.printf("\nSuggested code roots (for lattice/workspace.yaml):\n")
		for _, r := range d.CodeRoots {
			io.printf("  - %s\n", r)
		}
	}
	if len(d.RequiredPackages) > 0 {
		io.printf("\nRequired packages (re-run with --install to fetch):\n")
		for _, p := range d.RequiredPackages {
			io.printf("  - %s (%s) — %s\n", p.Name, p.Manager, p.Reason)
		}
	}
}

// runInstall executes one install command with a tight timeout. We do
// NOT inherit the parent's working directory because package managers
// like `composer global` and `npm install -g` write to the user's home,
// not the project — running from the project root is incorrect.
func runInstall(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("empty install command")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
