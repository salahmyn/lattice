package cli

import (
	"sort"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/validate"
)

type validateReport struct {
	Violations []schema.Violation `json:"violations"`
	Errors     int                `json:"errors"`
	Warnings   int                `json:"warnings"`
	ReviewMode bool               `json:"review_mode"`
	OK         bool               `json:"ok"`
}

func newValidateCommand(io *IO) *cobra.Command {
	var review bool
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Extract and validate the workspace",
		Long:  "Runs every validation rule and exits non-zero if any error-severity violation is found.",
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

			violations := validate.Validate(kg, cfg, validate.Options{ReviewMode: ws.Review})
			kg.Violations = violations
			_, _ = writeGraph(ws, cfg, kg)

			report := validateReport{Violations: violations, ReviewMode: ws.Review}
			for _, v := range violations {
				if v.IsError() {
					report.Errors++
				} else {
					report.Warnings++
				}
			}
			report.OK = report.Errors == 0

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
	return cmd
}

func renderValidate(io *IO, r validateReport) {
	if r.ReviewMode {
		io.printf("review mode: no code accessible — annotation and verification checks deferred to CI\n")
	}
	if len(r.Violations) == 0 {
		io.printf("validate: clean (0 violations)\n")
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
			if !v.IsError() {
				mark = "warn "
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
	io.printf("\n%d error(s), %d warning(s)\n", r.Errors, r.Warnings)
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
