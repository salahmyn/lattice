package cli

import (
	"github.com/spf13/cobra"
)

// NewRootCommand builds the full `lattice` command tree.
func NewRootCommand() *cobra.Command {
	io := defaultIO()

	root := &cobra.Command{
		Use:           "lattice",
		Short:         "A substrate for software meaning",
		Long:          "Lattice treats software meaning as a queryable, version-controlled substrate of a codebase.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVar(&io.Repo, "repo", ".", "target repository path")
	root.PersistentFlags().BoolVar(&io.JSON, "json", false, "emit machine-readable JSON output")

	root.AddCommand(
		newVersionCommand(io),
		newDoctorCommand(io),
		newInitCommand(io),
		newMigrateCommand(io),
		newAdaptersCommand(io),
		newExtractCommand(io),
		newImportCommand(io),
		newCoverageCommand(io),
		newValidateCommand(io),
		newPatchCommand(io),
		newAnalyzeCommand(io),
		newBlastRadiusCommand(io),
		newSymbolCommand(io),
		newMutationCommand(io),
		newStructuralChecksCommand(io),
		newAgentCommand(io),
		newViewCommand(io),
		newSkillsCommand(io),
		newNewCommand(io),
		newInitiativeCommand(io),
		newSearchCommand(io),
		newFeatureCommand(io),
		newTaskCommand(io),
		newServeCommand(io),
		newEPCommand(io),
		newBRDCommand(io),
		newDetectCommand(io),
	)

	return root
}

// Execute runs the root command and returns the process exit code.
func Execute() int {
	root := NewRootCommand()
	if err := root.Execute(); err != nil {
		if !IsExit(err) {
			root.PrintErrln("error:", err)
		}
		return 1
	}
	return 0
}
