package cli

import (
	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func newTaskCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Inspect tasks",
	}
	cmd.AddCommand(newTaskListCommand(io), newTaskPickNextCommand(io))
	return cmd
}

func newTaskListCommand(io *IO) *cobra.Command {
	var initiative string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		RunE: func(cmd *cobra.Command, _ []string) error {
			kg, err := buildGraph(cmd.Context(), io.Repo, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			var tasks []schema.Task
			for _, t := range kg.Tasks {
				if initiative == "" || t.Initiative == initiative {
					tasks = append(tasks, t)
				}
			}
			if io.JSON {
				return io.printJSON(tasks)
			}
			for _, t := range tasks {
				io.printf("%-10s %-12s %-18s %s\n", t.ID, t.Status, t.Initiative, t.Title)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&initiative, "initiative", "", "limit to one initiative")
	return cmd
}

func newTaskPickNextCommand(io *IO) *cobra.Command {
	var initiative string
	cmd := &cobra.Command{
		Use:   "pick-next",
		Short: "Pick the next actionable task (dependencies satisfied)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			kg, err := buildGraph(cmd.Context(), io.Repo, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			done := map[string]bool{}
			for _, t := range kg.Tasks {
				if t.Status == schema.TaskDone {
					done[t.ID] = true
				}
			}
			for _, t := range kg.Tasks {
				if initiative != "" && t.Initiative != initiative {
					continue
				}
				if t.Status != schema.TaskNotStarted {
					continue
				}
				if !dependenciesSatisfied(t, done) {
					continue
				}
				if io.JSON {
					return io.printJSON(t)
				}
				io.printf("Next task: %s — %s (initiative %s, stream %s)\n",
					t.ID, t.Title, t.Initiative, t.Stream)
				return nil
			}
			if io.JSON {
				return io.printJSON(map[string]interface{}{"task": nil})
			}
			io.printf("no actionable task found\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&initiative, "initiative", "", "limit to one initiative")
	return cmd
}

// dependenciesSatisfied reports whether every task dependency is done.
func dependenciesSatisfied(t schema.Task, done map[string]bool) bool {
	for _, d := range t.DependsOn {
		if d.Task != "" && !done[d.Task] {
			return false
		}
	}
	return true
}
