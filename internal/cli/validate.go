package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/flags"
	"github.com/salahmyn/lattice/pkg/lattice/lease"
	"github.com/salahmyn/lattice/pkg/lattice/ledger"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/validate"
)

type validateReport struct {
	Violations  []schema.Violation  `json:"violations"`
	Errors      int                 `json:"errors"`
	Warnings    int                 `json:"warnings"`
	Infos       int                 `json:"infos"`
	Allowlisted []validate.Accepted `json:"allowlisted,omitempty"`
	FailOn      schema.Severity     `json:"fail_on"`
	ReviewMode  bool                `json:"review_mode"`
	OK          bool                `json:"ok"`
}

func newValidateCommand(io *IO) *cobra.Command {
	var review bool
	var failOn, requireLevel string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Extract and validate the workspace",
		Long: "Runs every validation rule and exits non-zero if any finding at or above the fail-on\n" +
			"severity is open (default error; config.yaml validate.fail_on or --fail-on).\n" +
			"Findings accepted by the allowlist (lattice/allowlist.yaml, each entry with a reason)\n" +
			"are reported apart and never fail the run; an entry that matches nothing is stale\n" +
			"and fails it, so the list only shrinks.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("VALIDATE_FAILED", err.Error(), nil)
			}
			if review {
				ws.Review = true
			}
			kg, err := buildGraph(cmd.Context(), ws, false)
			if err != nil {
				return io.fail("VALIDATE_FAILED", err.Error(), nil)
			}
			cfg, _ := config.Load(ws.LatticeDir)
			if requireLevel != "" {
				cfg.Validate.RequireLevel = requireLevel
			}

			// v0.8 — fold ingested results (γ) and active leases (§5) into
			// the run so validation agrees with rtm/coverage and flags
			// concurrent-edit collisions. v0.8.1 adds the ledger (V8
			// author separation) and open meaning flags.
			set := results.Load(ws.LatticeDir)
			activeLeases, _ := lease.Active(ws.LatticeDir, time.Now())
			entries, _ := ledger.Load(ws.LatticeDir)

			violations := validate.Validate(kg, cfg, validate.Options{
				ReviewMode:    ws.Review,
				ResultOf:      resultOfFrom(set),
				Leases:        activeLeases,
				LedgerEntries: entries,
				FlagsOf:       flagReasons(flags.OpenByUnit(ws.LatticeDir)),
			})
			violations = append(violations, ambiguousResults(kg, set)...)
			kg.Violations = violations
			_, _ = writeGraph(ws, cfg, kg)

			if failOn == "" {
				failOn = cfg.Validate.FailOn
			}
			threshold, err := validate.FailOn(failOn)
			if err != nil {
				return io.fail("VALIDATE_FAILED", err.Error(), nil)
			}
			allow, bad := validate.LoadAllowlist(ws.LatticeDir, cfg.Validate)
			open, accepted := allow.Apply(append(violations, bad...))

			report := validateReport{Violations: open, Allowlisted: accepted, FailOn: threshold,
				ReviewMode: ws.Review, OK: true}
			for _, v := range open {
				switch v.Severity {
				case schema.SeverityError:
					report.Errors++
				case schema.SeverityWarning:
					report.Warnings++
				default:
					report.Infos++
				}
				if validate.Fails(v, threshold) {
					report.OK = false
				}
			}

			// Ledger the check run — script-free claims are not evidence;
			// the ledger line is.
			appendLedgerEvent(io, ws, ledger.EventCheckRun, "workspace",
				fmt.Sprintf("validate: %d error(s), %d warning(s), %d info, %d allowlisted",
					report.Errors, report.Warnings, report.Infos, len(report.Allowlisted)))

			if io.JSON {
				_ = io.printJSON(report)
			} else {
				renderValidate(io, report)
			}
			if !report.OK {
				return errExit
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&review, "review", false, "manifest-only mode: skip code-coupled checks")
	cmd.Flags().StringVar(&failOn, "fail-on", "", "lowest severity that fails the run: error (default), warning or info")
	cmd.Flags().StringVar(&requireLevel, "require-level", "", "truth level every BRD criterion and scenario must reach: verified or demonstrated")
	return cmd
}

func renderValidate(io *IO, r validateReport) {
	if r.ReviewMode {
		io.printf("review mode: no code accessible — annotation and verification checks deferred to CI\n")
	}
	if len(r.Violations) == 0 {
		io.printf("validate: clean (0 open findings")
		if n := len(r.Allowlisted); n > 0 {
			io.printf(", %d allowlisted", n)
		}
		io.printf(")\n")
		return
	}
	byFile := map[string][]schema.Violation{}
	for _, v := range r.Violations {
		f := "(corpus)"
		if v.Location != nil && v.Location.File != "" {
			f = v.Location.File
		}
		byFile[f] = append(byFile[f], v)
	}
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)

	for _, f := range files {
		io.printf("\n%s\n", f)
		for _, v := range byFile[f] {
			mark := "error"
			switch v.Severity {
			case schema.SeverityWarning:
				mark = "warn "
			case schema.SeverityInfo:
				mark = "info "
			}
			line := ""
			if v.Location != nil && v.Location.Line > 0 {
				line = ":" + strconv.Itoa(v.Location.Line)
			}
			io.printf("  %s %-32s %s%s\n", mark, v.Code, v.Message, line)
			if v.NextAction != nil && v.NextAction.Kind != "" {
				io.printf("        -> next: %s\n", describeNextAction(v.NextAction))
			}
		}
	}
	io.printf("\n%d error(s), %d warning(s), %d info", r.Errors, r.Warnings, r.Infos)
	if n := len(r.Allowlisted); n > 0 {
		io.printf(", %d allowlisted", n)
	}
	if r.FailOn != "" && r.FailOn != schema.SeverityError {
		io.printf(" (fails on %s)", r.FailOn)
	}
	io.printf("\n")
}

func describeNextAction(na *schema.NextAction) string {
	s := na.Kind
	if na.Annotation != "" {
		s += " " + na.Annotation
	}
	if na.Ref != "" {
		s += " " + na.Ref
	}
	if na.Field != "" {
		s += " field=" + na.Field
	}
	if na.Detail != "" {
		s += " (" + na.Detail + ")"
	}
	return s
}

// ambiguousResults reports verifier tests whose ingested result cannot be
// told apart from another case's: the lookup refuses them, so they count as
// not demonstrated, and the finding says which cases compete.
func ambiguousResults(kg schema.KnowledgeGraph, set results.Set) []schema.Violation {
	var v []schema.Violation
	for _, t := range kg.Tests {
		if len(t.Verifies) == 0 {
			continue
		}
		cases := set.Ambiguous(t.FQN)
		if len(cases) == 0 {
			continue
		}
		v = append(v, schema.Violation{
			Code: schema.CodeResultAmbiguous, Severity: schema.SeverityWarning,
			Subject: t.FQN,
			Message: fmt.Sprintf("verifier %s matches %d report cases equally (%s); its result is not counted",
				t.FQN, len(cases), strings.Join(cases, ", ")),
			Location: &schema.Location{File: t.File, Line: t.Line},
			NextAction: &schema.NextAction{Kind: "rename_test",
				Detail: "give the test a name unique in the report, or ingest the report whose classname matches its file"},
		})
	}
	return v
}
