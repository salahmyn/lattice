package cli

import (
	"io"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/agentic"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/views"
)

func newAgentCommand(io *IO) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "LLM-backed agentic capabilities (with deterministic fallbacks)",
	}
	cmd.AddCommand(
		newAgentSuggestAnnotationCommand(io),
		newAgentDraftProposalCommand(io),
		newAgentRecommendDecompositionCommand(io),
		newAgentNarrateCommand(io),
		newAgentContextCommand(io),
	)
	return cmd
}

func newAgentContextCommand(io *IO) *cobra.Command {
	var task string
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Assemble a self-contained agent context bundle",
		RunE: func(cmd *cobra.Command, _ []string) error {
			kg, ws, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}
			ac, err := views.BuildAgentContext(ws, kg, task)
			if err != nil {
				return io.fail("CONTEXT_FAILED", err.Error(), nil)
			}
			return io.printJSON(ac)
		},
	}
	cmd.Flags().StringVar(&task, "task", "", "task id to build context for")
	return cmd
}

// capabilities resolves the workspace and builds the agentic capabilities.
func capabilities(io *IO) (*agentic.Capabilities, error) {
	ws, err := openWorkspace(io)
	if err != nil {
		return nil, err
	}
	cfg, _ := config.Load(ws.LatticeDir)
	return agentic.New(ws, cfg), nil
}

// readAllStdin reads the entire standard input.
func readAllStdin() ([]byte, error) { return io.ReadAll(os.Stdin) }

func newAgentSuggestAnnotationCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "suggest-annotation <file> <line>",
		Short: "Suggest annotations for a code symbol",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			line, err := strconv.Atoi(args[1])
			if err != nil {
				return io.fail("BAD_LINE", "line must be an integer", nil)
			}
			caps, err := capabilities(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			res, err := caps.SuggestAnnotation(cmd.Context(), args[0], line)
			if err != nil {
				return io.fail("AGENT_FAILED", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(res)
			}
			io.printf("Annotation suggestions for %s:%d (%s)\n", res.File, res.Line, res.Mode)
			for _, s := range res.Suggestions {
				io.printf("  @%s %v  (confidence %.2f)\n", s.Annotation, s.Args, s.Confidence)
				if s.Rationale != "" {
					io.printf("      %s\n", s.Rationale)
				}
			}
			return nil
		},
	}
}

func newAgentDraftProposalCommand(io *IO) *cobra.Command {
	var proseFile, target string
	cmd := &cobra.Command{
		Use:   "draft-proposal",
		Short: "Draft a proposal manifest from a prose description",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if proseFile == "" {
				return io.fail("AGENT_NO_PROSE", "--prose <file> is required", nil)
			}
			var prose []byte
			var err error
			if proseFile == "-" {
				prose, err = readAllStdin()
			} else {
				prose, err = os.ReadFile(proseFile)
			}
			if err != nil {
				return io.fail("AGENT_PROSE_READ", err.Error(), nil)
			}
			caps, err := capabilities(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			res, err := caps.DraftProposal(cmd.Context(), string(prose), target)
			if err != nil {
				return io.fail("AGENT_FAILED", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(res)
			}
			io.printf("# Draft proposal (%s)\n\n%s\n", res.Mode, res.ManifestYAML)
			if len(res.OpenQuestions) > 0 {
				io.printf("\nOpen questions:\n")
				for _, q := range res.OpenQuestions {
					io.printf("  - %s\n", q)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&proseFile, "prose", "", "file containing the prose description")
	cmd.Flags().StringVar(&target, "target", "", "target feature id (optional)")
	return cmd
}

func newAgentRecommendDecompositionCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "recommend-decomposition <feature-id>",
		Short: "Recommend a sub-feature decomposition for an over-large feature",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			caps, err := capabilities(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			res, err := caps.RecommendDecomposition(cmd.Context(), args[0])
			if err != nil {
				return io.fail("AGENT_FAILED", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(res)
			}
			io.printf("Decomposition of %s (%s)\n", res.Feature, res.Mode)
			io.printf("  triggered: %v (%s)\n", res.Triggered, res.Reason)
			for _, sf := range res.SubFeatures {
				io.printf("  - %s: %s\n", sf.ID, sf.Purpose)
				if len(sf.Capabilities) > 0 {
					io.printf("      capabilities: %v\n", sf.Capabilities)
				}
				if len(sf.Invariants) > 0 {
					io.printf("      invariants:   %v\n", sf.Invariants)
				}
			}
			return nil
		},
	}
}

func newAgentNarrateCommand(io *IO) *cobra.Command {
	return &cobra.Command{
		Use:   "narrate [scope]",
		Short: "Generate a business-readable system narrative",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope := "repo"
			if len(args) == 1 {
				scope = args[0]
			}
			caps, err := capabilities(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			res, err := caps.Narrate(cmd.Context(), scope)
			if err != nil {
				return io.fail("AGENT_FAILED", err.Error(), nil)
			}
			if io.JSON {
				return io.printJSON(res)
			}
			io.printf("%s\n", res.Markdown)
			return nil
		},
	}
}
