package cli

import (
	"encoding/json"
	"os"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/views"
)

func newViewCommand(io *IO) *cobra.Command {
	var out, task, format string
	cmd := &cobra.Command{
		Use:   "view <name>",
		Short: "Render a view: developer | product | business | c4 | agent_context | entry-points | flows",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kg, ws, err := graphFor(io, cmd, false)
			if err != nil {
				return io.fail("EXTRACT_FAILED", err.Error(), nil)
			}

			var rendered string
			switch args[0] {
			case "developer", "product":
				rendered, err = views.Render(ws.ViewsDir(), args[0], kg)
			case "c4":
				switch format {
				case "structurizr", "dsl":
					rendered = views.RenderC4Structurizr(ws, kg)
				default:
					rendered = views.RenderC4(ws, kg)
				}
			case "entry-points", "entry_points", "entrypoints":
				rendered = views.RenderEntryPoints(kg)
			case "flows", "flow":
				rendered = views.RenderFlows(kg, task)
			case "business":
				caps, nErr := capabilities(io)
				if nErr != nil {
					return io.fail("NO_WORKSPACE", nErr.Error(), nil)
				}
				res, nErr := caps.Narrate(cmd.Context(), "repo")
				if nErr != nil {
					return io.fail("VIEW_FAILED", nErr.Error(), nil)
				}
				rendered = res.Markdown
			case "agent_context":
				ac, acErr := views.BuildAgentContext(ws, kg, task)
				if acErr != nil {
					return io.fail("VIEW_FAILED", acErr.Error(), nil)
				}
				if io.JSON || out != "" {
					if out != "" {
						return writeJSONFile(out, ac)
					}
					return io.printJSON(ac)
				}
				return io.printJSON(ac)
			default:
				return io.fail("UNKNOWN_VIEW", "unknown view: "+args[0], nil)
			}
			if err != nil {
				return io.fail("VIEW_FAILED", err.Error(), nil)
			}

			if out != "" {
				if werr := os.WriteFile(out, []byte(rendered), 0o644); werr != nil {
					return io.fail("VIEW_WRITE_FAILED", werr.Error(), nil)
				}
				io.printf("wrote %s view to %s\n", args[0], out)
				return nil
			}
			if io.JSON {
				return io.printJSON(map[string]string{"view": args[0], "content": rendered})
			}
			io.printf("%s\n", rendered)
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "write the view to a file")
	cmd.Flags().StringVar(&task, "task", "", "task id (for agent_context)")
	cmd.Flags().StringVar(&format, "format", "mermaid", "c4 output: mermaid | structurizr")
	return cmd
}

// writeJSONFile writes v as indented JSON to path.
func writeJSONFile(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
