package cli

import (
	"sort"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func newInitiativeCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "initiative",
		Short: "Inspect initiatives",
	}
	cmd.AddCommand(
		newInitiativeListCommand(io),
		newInitiativeShowCommand(io),
		newInitiativeKanbanCommand(io),
		newInitiativeCriticalPathCommand(io),
	)
	return cmd
}

func newInitiativeListCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List initiatives",
		RunE: func(cmd *cobra.Command, _ []string) error {
			kg, _, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(kg.Initiatives)
			}
			for _, in := range kg.Initiatives {
				io.printf("%-28s %s\n", in.ID, in.Status)
			}
			return nil
		},
	}
}

func newInitiativeShowCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show an initiative and its tasks",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kg, _, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			in := findInitiative(kg, args[0])
			if in == nil {
				return io.fail("INITIATIVE_NOT_FOUND", "initiative not found: "+args[0], nil)
			}
			tasks := tasksOf(kg, args[0])
			if io.JSON {
				return io.printJSON(map[string]interface{}{"initiative": in, "tasks": tasks})
			}
			io.printf("%s  [%s]\n%s\n\n", in.ID, in.Status, in.Motivation)
			io.printf("Streams: ")
			for _, s := range in.Streams {
				io.printf("%s ", s.ID)
			}
			io.printf("\n\nTasks:\n")
			for _, t := range tasks {
				io.printf("  %-10s %-12s %s\n", t.ID, t.Status, t.Title)
			}
			return nil
		},
	}
}

func newInitiativeKanbanCommand(io *IO) *cobra.Command {
	var stream string
	cmd := &cobra.Command{
		Use:   "kanban <id>",
		Short: "Show an initiative's tasks grouped by status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kg, _, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			columns := map[schema.TaskStatus][]schema.Task{}
			for _, t := range tasksOf(kg, args[0]) {
				if stream != "" && t.Stream != stream {
					continue
				}
				columns[t.Status] = append(columns[t.Status], t)
			}
			if io.JSON {
				return io.printJSON(columns)
			}
			order := []schema.TaskStatus{
				schema.TaskNotStarted, schema.TaskInProgress, schema.TaskBlocked,
				schema.TaskInReview, schema.TaskDone, schema.TaskCancelled,
			}
			for _, st := range order {
				ts := columns[st]
				if len(ts) == 0 {
					continue
				}
				io.printf("\n[%s]\n", st)
				for _, t := range ts {
					io.printf("  %-10s %s\n", t.ID, t.Title)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&stream, "stream", "", "limit to one stream")
	return cmd
}

func newInitiativeCriticalPathCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "critical-path <id>",
		Short: "Compute the critical path through an initiative's tasks",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kg, _, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			path := criticalPath(tasksOf(kg, args[0]))
			if io.JSON {
				return io.printJSON(map[string]interface{}{"critical_path": path})
			}
			if len(path) == 0 {
				io.printf("no tasks\n")
				return nil
			}
			io.printf("Critical path (%d task(s)):\n", len(path))
			for i, id := range path {
				io.printf("  %d. %s\n", i+1, id)
			}
			return nil
		},
	}
}

func findInitiative(kg schema.KnowledgeGraph, id string) *schema.Initiative {
	for i := range kg.Initiatives {
		if kg.Initiatives[i].ID == id {
			return &kg.Initiatives[i]
		}
	}
	return nil
}

func tasksOf(kg schema.KnowledgeGraph, initiativeID string) []schema.Task {
	var out []schema.Task
	for _, t := range kg.Tasks {
		if t.Initiative == initiativeID {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// criticalPath returns the longest task-dependency chain by task count.
func criticalPath(tasks []schema.Task) []string {
	deps := map[string][]string{}
	known := map[string]bool{}
	for _, t := range tasks {
		known[t.ID] = true
	}
	for _, t := range tasks {
		for _, d := range t.DependsOn {
			if d.Task != "" && known[d.Task] {
				deps[t.ID] = append(deps[t.ID], d.Task)
			}
		}
	}
	memo := map[string][]string{}
	var longest func(id string) []string
	longest = func(id string) []string {
		if p, ok := memo[id]; ok {
			return p
		}
		memo[id] = []string{id} // cycle guard
		best := []string{}
		for _, d := range deps[id] {
			if c := longest(d); len(c) > len(best) {
				best = c
			}
		}
		path := append(append([]string{}, best...), id)
		memo[id] = path
		return path
	}
	var overall []string
	for _, t := range tasks {
		if p := longest(t.ID); len(p) > len(overall) {
			overall = p
		}
	}
	return overall
}
