package cli

import (
	"encoding/json"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/patch"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func newPatchCommand(io *IO) *cobra.Command {
	var fromFile string
	var doPreview, doApply bool

	cmd := &cobra.Command{
		Use:   "patch [target]",
		Short: "Apply a typed patch to a manifest, initiative, or task",
		Long:  "Reads a patch JSON and previews or applies it. Use --from-file - to read from stdin.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if fromFile == "" {
				return io.fail("PATCH_NO_INPUT", "a patch file is required (--from-file)", nil)
			}
			p, err := readPatch(fromFile)
			if err != nil {
				return io.fail("PATCH_PARSE_FAILED", err.Error(), nil)
			}

			engine := patch.New(io.Repo)
			if doApply {
				result, err := engine.Apply(cmd.Context(), p)
				if err != nil {
					return io.fail("PATCH_APPLY_FAILED", err.Error(), nil)
				}
				if io.JSON {
					return io.printJSON(result)
				}
				renderPatchResult(io, result)
				if !result.Applied {
					return errExit
				}
				return nil
			}

			// Default: preview.
			preview, err := engine.Preview(cmd.Context(), p)
			if err != nil {
				return io.fail("PATCH_PREVIEW_FAILED", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(preview)
			}
			renderPatchPreview(io, preview)
			return nil
		},
	}
	cmd.Flags().StringVar(&fromFile, "from-file", "", "patch JSON file, or - for stdin")
	cmd.Flags().BoolVar(&doPreview, "preview", false, "preview the patch (default)")
	cmd.Flags().BoolVar(&doApply, "apply", false, "apply the patch")
	return cmd
}

// readPatch loads a patch from a file path or stdin ("-").
func readPatch(path string) (schema.Patch, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return schema.Patch{}, err
	}
	var p schema.Patch
	if err := json.Unmarshal(data, &p); err != nil {
		return schema.Patch{}, err
	}
	return p, nil
}

func renderPatchPreview(io *IO, p schema.PatchPreview) {
	io.printf("PATCH PREVIEW\n\n")
	if p.Diff == "" {
		io.printf("(no change)\n")
	} else {
		io.printf("%s\n", p.Diff)
	}
	if len(p.ResolvedViolations) > 0 {
		io.printf("Resolves %d violation(s):\n", len(p.ResolvedViolations))
		for _, v := range p.ResolvedViolations {
			io.printf("  - %s %s\n", v.Code, v.Message)
		}
	}
	if len(p.IntroducedViolations) > 0 {
		io.printf("Introduces %d violation(s):\n", len(p.IntroducedViolations))
		for _, v := range p.IntroducedViolations {
			io.printf("  + %s %s\n", v.Code, v.Message)
		}
	}
	if p.IsAcceptable() {
		io.printf("\nVerdict: acceptable (no new errors). Re-run with --apply to write.\n")
	} else {
		io.printf("\nVerdict: would introduce errors; apply will be refused.\n")
	}
}

func renderPatchResult(io *IO, r schema.PatchResult) {
	if r.Applied {
		io.printf("%s\n", r.Message)
		return
	}
	io.errorf("%s\n", r.Message)
	for _, v := range r.Violations {
		io.errorf("  + %s %s\n", v.Code, v.Message)
	}
}
