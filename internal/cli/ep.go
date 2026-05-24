package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/entrypoints"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// newEPCommand is the v0.3.2 review-loop surface for entry points.
// Reframing: EPs are auto-persisted as `status: proposal` by
// `lattice extract`, then a reviewer accepts (status -> production)
// or rejects (file -> .rejected/). No parallel CandidatesFile model
// for the invocation axis — the on-disk YAML is the queue.
func newEPCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ep",
		Short: "List, inspect, accept, or reject entry-point manifests",
	}
	cmd.AddCommand(
		newEPListCommand(io),
		newEPShowCommand(io),
		newEPAcceptCommand(io),
		newEPRejectCommand(io),
	)
	return cmd
}

func newEPListCommand(io *IO) *cobra.Command {
	var statusFilter string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every entry-point manifest with status and trigger",
		RunE: func(_ *cobra.Command, _ []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			eps, err := entrypoints.LoadEntryPoints(ws.EntryPointsDir())
			if err != nil {
				return io.fail("EP_LIST_FAILED", err.Error(), nil)
			}
			filtered := eps[:0:0]
			for _, ep := range eps {
				if statusFilter != "" && string(ep.Status) != statusFilter {
					continue
				}
				filtered = append(filtered, ep)
			}
			sort.Slice(filtered, func(i, j int) bool { return filtered[i].ID < filtered[j].ID })
			if io.JSON {
				return io.printJSON(filtered)
			}
			io.printf("Entry points (%d", len(filtered))
			if statusFilter != "" {
				io.printf(", status=%s", statusFilter)
			}
			io.printf("):\n")
			for _, ep := range filtered {
				io.printf("  %-50s %-12s %-12s %s\n",
					ep.ID, ep.Kind, ep.Status, triggerSummaryCLI(ep))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&statusFilter, "status", "",
		"filter by status (proposal | production | deprecated)")
	return cmd
}

func newEPShowCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "show <ep-id>",
		Short: "Print one entry-point manifest in full",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			ep, err := loadOneEP(ws.EntryPointsDir(), args[0])
			if err != nil {
				return io.fail("EP_NOT_FOUND", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(ep)
			}
			io.printf("%s  [%s]  v%d\n", ep.ID, ep.Status, ep.Version)
			if ep.Purpose != "" {
				io.printf("%s\n", ep.Purpose)
			}
			io.printf("\n  kind:       %s\n", ep.Kind)
			io.printf("  trigger:    %s\n", triggerSummaryCLI(ep))
			io.printf("  handler:    %s\n", ep.Handler.Symbol)
			if len(ep.Flow) > 0 {
				io.printf("\n  flow:\n")
				for _, s := range ep.Flow {
					cap := ""
					if s.Capability != "" {
						cap = " (cap: " + s.Capability + ")"
					}
					io.printf("    -> %s%s\n", s.Feature, cap)
				}
			}
			return nil
		},
	}
}

func newEPAcceptCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "accept <ep-id>",
		Short: "Promote an entry point from proposal to production",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runEPDecision(io, args[0], entrypoints.DecisionAcceptEP)
		},
	}
}

func newEPRejectCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "reject <ep-id>",
		Short: "Move an entry point to .rejected/ (reversible)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runEPDecision(io, args[0], entrypoints.DecisionRejectEP)
		},
	}
}

// runEPDecision is the shared driver for accept/reject — keeps the
// JSON / text shapes identical between the two verbs.
func runEPDecision(io *IO, id, decision string) error {
	ws, err := openWorkspace(io)
	if err != nil {
		return io.fail("NO_WORKSPACE", err.Error(), nil)
	}
	res, err := entrypoints.Decide(ws.EntryPointsDir(), id, decision)
	if err != nil {
		return io.fail("EP_DECIDE_FAILED", err.Error(), nil)
	}
	if io.JSON {
		return io.printJSON(res)
	}
	if decision == entrypoints.DecisionAcceptEP {
		io.printf("accepted %s -> status=%s (%s)\n", id, res.NewStatus, res.Path)
	} else {
		io.printf("rejected %s -> archived at %s\n", id, res.ArchivedAt)
		io.printf("  (recover with: mv %s %s)\n", res.ArchivedAt,
			strings.Replace(res.ArchivedAt, ".rejected/", "", 1))
	}
	return nil
}

func loadOneEP(dir, id string) (schema.EntryPoint, error) {
	eps, err := entrypoints.LoadEntryPoints(dir)
	if err != nil {
		return schema.EntryPoint{}, err
	}
	for _, ep := range eps {
		if ep.ID == id {
			return ep, nil
		}
	}
	return schema.EntryPoint{}, fmt.Errorf("no entry-point with id %q", id)
}

// triggerSummaryCLI returns a one-line trigger description for the
// list/show CLI views — separate from the views package's mermaid-
// safe label so CLI prose stays prose.
func triggerSummaryCLI(ep schema.EntryPoint) string {
	switch ep.Kind {
	case schema.EntryPointKindHTTP:
		return ep.Trigger.Method + " " + ep.Trigger.Path
	case schema.EntryPointKindCLI:
		return ep.Trigger.Command
	case schema.EntryPointKindCron:
		return "cron " + ep.Trigger.Schedule
	case schema.EntryPointKindQueue:
		return "queue " + ep.Trigger.Queue
	case schema.EntryPointKindEventConsumer:
		return "event " + ep.Trigger.Event
	}
	return ep.ID
}

// unused import suppressor (os is used transitively via gopkg.in/yaml.v3
// in the helpers; this exists to keep imports tidy if a future helper
// is added).
var _ = os.PathSeparator
var _ = yaml.Marshal
