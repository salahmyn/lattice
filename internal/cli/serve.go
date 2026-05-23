package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/ui"
)

// newServeCommand boots the Lattice v0.4.0 web UI against the current
// workspace. The same buildGraph used by every other CLI command feeds
// the UI on every request — so the UI never disagrees with `extract`
// or `validate`.
func newServeCommand(io *IO) *cobra.Command {
	var host, token string
	var port int
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the Lattice web UI on http://host:port",
		Long: `serve boots a local HTTP server that exposes the same KnowledgeGraph
the CLI produces — a navigation-first interface for non-CLI reviewers,
faster click-through for engineers, and a schema-aware editor for
operators.

Security: the default bind is 127.0.0.1 (loopback) with no token; any
non-loopback host requires --token <X> and rejects requests missing
the X-Lattice-Token header.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			srv := ui.New(ws, ui.Options{
				Host:  host,
				Port:  port,
				Token: token,
				GraphBuilder: func(ctx context.Context) (schema.KnowledgeGraph, error) {
					return buildGraph(ctx, ws, false)
				},
			})
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			io.printf("Lattice UI listening on %s\n", srv.Addr())
			if token != "" {
				io.printf("  token gate enabled — send X-Lattice-Token: %s on every request\n", token)
			} else if host != "" && host != "127.0.0.1" && host != "localhost" && host != "::1" {
				io.printf("  WARNING: bound to non-loopback host without --token\n")
			}
			io.printf("  press Ctrl-C to stop\n")

			if err := srv.ListenAndServe(ctx); err != nil {
				return io.fail("SERVE_FAILED", err.Error(), nil)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "host to bind (non-loopback requires --token)")
	cmd.Flags().IntVar(&port, "port", 7070, "port to bind")
	cmd.Flags().StringVar(&token, "token", "", "required pre-shared token when --host is non-loopback")
	return cmd
}
