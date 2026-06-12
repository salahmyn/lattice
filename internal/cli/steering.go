package cli

import (
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/lease"
	"github.com/salahmyn/lattice/pkg/lattice/ledger"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/rtm"
)

// resultOfFrom adapts an ingested result Set to the (passed, known) shape
// rtm/validate expect. A skipped test reads as unknown — it is no
// demonstration evidence either way.
func resultOfFrom(set results.Set) func(string) (bool, bool) {
	return func(fqn string) (bool, bool) {
		o, ok := set.Lookup(fqn)
		if !ok || o == results.Skip {
			return false, false
		}
		return o == results.Pass, true
	}
}

// ---------------------------------------------------------------------------
// lattice results — ingest test-result reports (v0.8 γ)
// ---------------------------------------------------------------------------

func newResultsCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "results",
		Short: "Ingest test results so the RTM can say DEMONSTRATED, not just declared",
	}
	cmd.AddCommand(newResultsIngestCommand(io), newResultsShowCommand(io))
	return cmd
}

func newResultsIngestCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "ingest <junit.xml>",
		Short: "Parse a junit/pytest/phpunit XML report into lattice/.cache/results",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("WORKSPACE_FAILED", err.Error(), nil)
			}
			// The commit is best-effort: the graph carries it, but ingesting
			// shouldn't require a full extract, so we read it cheaply.
			kg, _, _ := graphFor(io, cmd, false)
			set, err := results.Ingest(ws.LatticeDir, args[0], kg.GeneratedFromCommit)
			if err != nil {
				return io.fail("INGEST_FAILED", err.Error(), nil)
			}
			pass, fail, skip := tallyResults(set)
			if io.JSON {
				return io.printJSON(map[string]interface{}{
					"commit": set.Commit, "total": len(set.Results),
					"pass": pass, "fail": fail, "skip": skip,
				})
			}
			io.printf("ingested %d test results (%d pass, %d fail, %d skip)\n",
				len(set.Results), pass, fail, skip)
			io.printf("run `lattice rtm` to see DECLARED criteria upgrade to DEMONSTRATED\n")
			return nil
		},
	}
}

func newResultsShowCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the currently-ingested result set",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("WORKSPACE_FAILED", err.Error(), nil)
			}
			set := results.Load(ws.LatticeDir)
			if io.JSON {
				return io.printJSON(set)
			}
			if len(set.Results) == 0 {
				io.printf("no results ingested — `lattice results ingest <junit.xml>`\n")
				return nil
			}
			pass, fail, skip := tallyResults(set)
			io.printf("commit %s: %d results (%d pass, %d fail, %d skip)\n",
				shortCommit(set.Commit), len(set.Results), pass, fail, skip)
			return nil
		},
	}
}

func tallyResults(set results.Set) (pass, fail, skip int) {
	for _, o := range set.Results {
		switch o {
		case results.Pass:
			pass++
		case results.Fail:
			fail++
		case results.Skip:
			skip++
		}
	}
	return
}

// ---------------------------------------------------------------------------
// lattice lease — claim a slice before editing it (v0.8 §5)
// ---------------------------------------------------------------------------

func newLeaseCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lease",
		Short: "Claim a slice of work so a fleet of agents doesn't collide",
	}
	cmd.AddCommand(newLeaseAcquireCommand(io), newLeaseReleaseCommand(io), newLeaseListCommand(io))
	return cmd
}

func newLeaseAcquireCommand(io *IO) *cobra.Command {
	var scope []string
	var ttl string
	cmd := &cobra.Command{
		Use:   "acquire <unit>",
		Short: "Acquire a lease on a feature/BRD/scenario unit",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("WORKSPACE_FAILED", err.Error(), nil)
			}
			dur, err := time.ParseDuration(ttl)
			if err != nil || dur <= 0 {
				dur = 2 * time.Hour
			}
			kg, _, _ := graphFor(io, cmd, false)
			l, err := lease.Acquire(ws.LatticeDir, args[0], io.actor(), kg.GeneratedFromCommit, dur, scope, time.Now())
			if err != nil {
				return io.fail("LEASE_CONFLICT", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(l)
			}
			io.printf("leased %q for %s until %s\n", l.Unit, l.Actor, l.Expires)
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&scope, "scope", nil, "repo-relative path prefixes the lease covers")
	cmd.Flags().StringVar(&ttl, "ttl", "2h", "lease lifetime before it expires")
	return cmd
}

func newLeaseReleaseCommand(io *IO) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "release <unit>",
		Short: "Release a lease you hold",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("WORKSPACE_FAILED", err.Error(), nil)
			}
			if err := lease.Release(ws.LatticeDir, args[0], io.actor(), force); err != nil {
				return io.fail("LEASE_RELEASE_FAILED", err.Error(), nil)
			}
			io.printf("released %q\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "release even if held by another actor")
	return cmd
}

func newLeaseListCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List active and stale leases",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("WORKSPACE_FAILED", err.Error(), nil)
			}
			all, err := lease.List(ws.LatticeDir)
			if err != nil {
				return io.fail("LEASE_LIST_FAILED", err.Error(), nil)
			}
			now := time.Now()
			if io.JSON {
				return io.printJSON(all)
			}
			if len(all) == 0 {
				io.printf("no active leases\n")
				return nil
			}
			for _, l := range all {
				state := "active"
				if !l.IsActive(now) {
					state = "stale"
				}
				io.printf("%-24s %-20s %-7s scope=%s\n", l.Unit, l.Actor, state, strings.Join(l.Scope, ","))
			}
			// Surface overlaps inline — the same signal validate reports.
			active, _ := lease.Active(ws.LatticeDir, now)
			for _, ov := range lease.Overlaps(active) {
				io.printf("  ! overlap: %q and %q both claim %s\n", ov.A.Unit, ov.B.Unit, ov.PathPrefix)
			}
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// lattice ledger — the attribution / soundability spine (v0.8 §6)
// ---------------------------------------------------------------------------

func newLedgerCommand(io *IO) *cobra.Command {
	var unit, actor string
	cmd := &cobra.Command{
		Use:   "ledger",
		Short: "Show the append-only ledger of truth-level transitions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("WORKSPACE_FAILED", err.Error(), nil)
			}
			entries, err := ledger.Load(ws.LatticeDir)
			if err != nil {
				return io.fail("LEDGER_FAILED", err.Error(), nil)
			}
			if unit != "" {
				entries = ledger.ByUnit(entries, unit)
			}
			if actor != "" {
				entries = ledger.ByActor(entries, actor)
			}
			if io.JSON {
				return io.printJSON(entries)
			}
			if len(entries) == 0 {
				io.printf("ledger is empty — `lattice ledger rebuild` to snapshot current truth-levels\n")
				return nil
			}
			for _, e := range entries {
				io.printf("%-10s %-22s %-26s %-22s %s\n",
					shortCommit(e.Commit), truncate(e.Actor, 22), e.Unit, e.Transition, e.Evidence)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&unit, "unit", "", "filter to one unit (e.g. brd.x:US-1)")
	cmd.Flags().StringVar(&actor, "actor-filter", "", "filter to one actor")
	cmd.AddCommand(newLedgerRebuildCommand(io))
	return cmd
}

func newLedgerRebuildCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "rebuild",
		Short: "Replay the current graph into a fresh truth-level snapshot",
		RunE: func(cmd *cobra.Command, _ []string) error {
			kg, ws, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			cfg, _ := config.Load(ws.LatticeDir)
			set := results.Load(ws.LatticeDir)
			m := rtm.Build(kg, rtm.Options{
				MutationThreshold: cfg.MutationTesting.Thresholds.Default,
				ResultOf:          resultOfFrom(set),
			})
			mode := cfg.Autonomy.DefaultMode
			if mode == "" {
				mode = "human-gated"
			}
			now := time.Now().UTC().Format(time.RFC3339)
			var entries []ledger.Entry
			for _, r := range m.Rows {
				entries = append(entries, ledger.Entry{
					At: now, Commit: kg.GeneratedFromCommit, Actor: io.actor(),
					Unit:       r.BRDID + ":" + r.CriterionID,
					Transition: "∅→" + string(r.Status),
					Evidence:   r.MapsTo, Mode: mode,
				})
			}
			for _, s := range m.Scenarios {
				entries = append(entries, ledger.Entry{
					At: now, Commit: kg.GeneratedFromCommit, Actor: io.actor(),
					Unit:       s.BRDID + ":" + s.ScenarioID,
					Transition: "∅→" + string(s.Status),
					Evidence:   strings.Join(s.Verifiers, ","), Mode: mode,
				})
			}
			if err := ledger.Truncate(ws.LatticeDir); err != nil {
				return io.fail("LEDGER_FAILED", err.Error(), nil)
			}
			if err := ledger.RecordAll(ws.LatticeDir, entries); err != nil {
				return io.fail("LEDGER_FAILED", err.Error(), nil)
			}
			io.printf("ledger rebuilt: %d transitions recorded under mode %q\n", len(entries), mode)
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// lattice next — the weakest-link affordance (v0.8 §4)
// ---------------------------------------------------------------------------

func newNextCommand(io *IO) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "next",
		Short: "Rank the highest-value next actions by weakest link",
		Long: `Reads the RTM and journey coverage and returns the units furthest
from demonstrated/correctly-meant, weakest first. Units under an active
lease held by another actor are withheld so a fleet doesn't collide.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			kg, ws, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			cfg, _ := config.Load(ws.LatticeDir)
			set := results.Load(ws.LatticeDir)
			m := rtm.Build(kg, rtm.Options{
				MutationThreshold: cfg.MutationTesting.Thresholds.Default,
				ResultOf:          resultOfFrom(set),
			})

			// Withhold leased units held by a different actor.
			leasedBy := map[string]string{}
			if active, _ := lease.Active(ws.LatticeDir, time.Now()); active != nil {
				for _, l := range active {
					leasedBy[l.Unit] = l.Actor
				}
			}
			me := io.actor()

			actions := rankActions(m, leasedBy, me)
			if limit > 0 && len(actions) > limit {
				actions = actions[:limit]
			}
			if io.JSON {
				return io.printJSON(map[string]interface{}{
					"journey_coverage": rtm.ComputeJourneyCoverage(m),
					"actions":          actions,
				})
			}
			jc := rtm.ComputeJourneyCoverage(m)
			io.printf("journey coverage: %d/%d scenarios reach a declared entry point\n\n",
				jc.ReachedScenarios, jc.TotalScenarios)
			if len(actions) == 0 {
				io.printf("nothing outstanding — every unit is demonstrated and correctly-meant\n")
				return nil
			}
			for i, a := range actions {
				io.printf("%d. [%s] %s\n   → %s\n   skill: %s\n",
					i+1, a.Level, a.Unit, a.Action, a.Skill)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 10, "max actions to return")
	return cmd
}

// nextAction is one ranked recommendation.
type nextAction struct {
	Unit     string `json:"unit"`
	Level    string `json:"level"`
	Severity int    `json:"-"`
	Action   string `json:"action"`
	Skill    string `json:"skill"`
}

// rankActions turns the matrix into a weakest-link-first action list. A
// unit that is demonstrated/verified contributes no action; everything
// below contributes one, ranked by status severity (worst first).
func rankActions(m rtm.Matrix, leasedBy map[string]string, me string) []nextAction {
	var out []nextAction
	add := func(unit string, st rtm.Status, action string) {
		if st == rtm.StatusDemonstrated || st == rtm.StatusVerified {
			return
		}
		if holder, ok := leasedBy[unit]; ok && holder != me {
			return // withheld — another agent holds it
		}
		out = append(out, nextAction{
			Unit: unit, Level: string(st), Severity: st.Severity(),
			Action: action, Skill: skillFor(st),
		})
	}
	for _, r := range m.Rows {
		add(r.BRDID+":"+r.CriterionID, r.Status, actionForCriterion(r))
	}
	for _, s := range m.Scenarios {
		add(s.BRDID+":"+s.ScenarioID, s.Status, actionForScenario(s))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Severity != out[j].Severity {
			return out[i].Severity < out[j].Severity
		}
		return out[i].Unit < out[j].Unit
	})
	return out
}

func actionForCriterion(r rtm.Row) string {
	switch r.Status {
	case rtm.StatusPhantom:
		return "fix maps_to_invariant — it resolves to nothing"
	case rtm.StatusUnenforced:
		return "add an @enforces guard for " + r.MapsTo
	case rtm.StatusFailing:
		return "the verifier is failing — fix the code or the test, then re-ingest results"
	case rtm.StatusUnverified:
		return "add a test that @verifies " + r.MapsTo
	case rtm.StatusUnmapped:
		return "map this criterion to an invariant (maps_to_invariant)"
	case rtm.StatusPartial:
		return "raise the mutation score on " + r.MapsTo + " above threshold"
	}
	return "advance toward demonstrated"
}

func actionForScenario(s rtm.ScenarioRow) string {
	switch s.Status {
	case rtm.StatusUnmapped:
		return "add verified_by: [<test-fqn>|<entry-point-id>] to make the scenario verifiable"
	case rtm.StatusUnverified:
		return "tag a test `@verifies " + s.BRDID + ":" + s.ScenarioID + "`"
	case rtm.StatusFailing:
		return "the scenario verifier is failing on the current commit"
	}
	if !s.TouchesEntryPoint {
		return "add the entry point this scenario is reached through, so it counts toward journey coverage"
	}
	return "ingest a passing result to demonstrate the scenario"
}

// skillFor points an agent at the goal skill for the work class.
func skillFor(st rtm.Status) string {
	switch st {
	case rtm.StatusUnmapped, rtm.StatusUnverified, rtm.StatusFailing:
		return "achieving-goals-with-lattice"
	default:
		return "verifying-meaning"
	}
}

func shortCommit(c string) string {
	if len(c) > 8 {
		return c[:8]
	}
	if c == "" {
		return "-"
	}
	return c
}
