package cli

import (
	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/plugins"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func newStructuralChecksCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "structural-checks",
		Short: "Manage structural invariant checks",
	}
	cmd.AddCommand(newStructuralListCommand(io), newStructuralRunCommand(io))
	return cmd
}

func newStructuralListCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List declared structural checks",
		RunE: func(cmd *cobra.Command, _ []string) error {
			kg, _, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(kg.StructuralChecks)
			}
			if len(kg.StructuralChecks) == 0 {
				io.printf("no structural checks declared\n")
				return nil
			}
			for _, c := range kg.StructuralChecks {
				io.printf("  %-24s feature=%s verifies=%v\n", c.ID, c.Feature, c.VerifiesInvariants)
			}
			return nil
		},
	}
}

func newStructuralRunCommand(io *IO) *cobra.Command {
	var scopeModule string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run structural checks as subprocesses",
		RunE: func(cmd *cobra.Command, _ []string) error {
			kg, ws, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			cfg, _ := config.Load(ws.LatticeDir)

			checks := kg.StructuralChecks
			if scopeModule != "" {
				checks = filterChecksByModule(checks, scopeModule)
			}
			results := plugins.RunAll(cmd.Context(), ws.PrimaryCodeRoot().Abs, checks, cfg.Subprocess.DefaultTimeoutDuration())

			var violations []schema.Violation
			for _, r := range results {
				violations = append(violations, r.ToViolations()...)
			}
			if io.JSON {
				return io.printJSON(map[string]interface{}{"results": results, "violations": violations})
			}
			for _, r := range results {
				switch {
				case r.Error != "":
					io.printf("  %-24s ERROR %s\n", r.CheckID, r.Error)
				case len(r.Violations) > 0:
					io.printf("  %-24s %d violation(s)\n", r.CheckID, len(r.Violations))
					for _, v := range r.Violations {
						io.printf("      %s:%d %s\n", v.File, v.Line, v.Message)
					}
				default:
					io.printf("  %-24s ok\n", r.CheckID)
				}
			}
			if len(violations) > 0 {
				return errExit
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&scopeModule, "scope", "", "limit to checks scoped to a module path")
	return cmd
}

func filterChecksByModule(checks []schema.GraphStructuralCheck, module string) []schema.GraphStructuralCheck {
	var out []schema.GraphStructuralCheck
	for _, c := range checks {
		for _, m := range c.Scope.Modules {
			if m == module {
				out = append(out, c)
				break
			}
		}
	}
	return out
}
