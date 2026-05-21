package cli

import (
	"path/filepath"
	"sort"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/graph"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/validate"
)

type validateReport struct {
	Violations []schema.Violation `json:"violations"`
	Errors     int                `json:"errors"`
	Warnings   int                `json:"warnings"`
	OK         bool               `json:"ok"`
}

func newValidateCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Extract and validate the repository",
		Long:  "Runs every validation rule and exits non-zero if any error-severity violation is found.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			kg, err := buildGraph(cmd.Context(), io.Repo, false)
			if err != nil {
				return io.fail("VALIDATE_FAILED", err.Error(), nil)
			}
			cfg, _ := config.Load(io.Repo)

			violations := validate.Validate(kg, cfg)
			kg.Violations = violations
			_ = graph.Write(filepath.Join(io.Repo, "lattice.json"), kg)

			report := validateReport{Violations: violations}
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
}

func renderValidate(io *IO, r validateReport) {
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
