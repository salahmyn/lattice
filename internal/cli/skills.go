package cli

import (
	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/skills"
)

func newSkillsCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "List and export shipped agent skills",
	}
	cmd.AddCommand(newSkillsListCommand(io), newSkillsExportCommand(io))
	return cmd
}

func newSkillsListCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List shipped agent skills",
		RunE: func(_ *cobra.Command, _ []string) error {
			list := skills.List()
			if io.JSON {
				return io.printJSON(list)
			}
			for _, s := range list {
				io.printf("%-26s %s\n", s.ID, s.Description)
			}
			return nil
		},
	}
}

func newSkillsExportCommand(io *IO) *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "export <skill-id>",
		Short: "Copy a skill folder to a directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			dest := out
			if dest == "" {
				dest = "."
			}
			if err := skills.Export(args[0], dest); err != nil {
				return io.fail("SKILL_EXPORT_FAILED", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(map[string]string{"skill": args[0], "exported_to": dest})
			}
			io.printf("exported %s to %s\n", args[0], dest)
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "destination directory")
	return cmd
}
