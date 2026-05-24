package cli

import (
	"context"
	"os"
	"os/signal"
	"strings"
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
	var host, token, basicAuth string
	var port int
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the Lattice web UI on http://host:port",
		Long: `serve boots a local HTTP server that exposes the same KnowledgeGraph
the CLI produces — a navigation-first interface for non-CLI reviewers,
faster click-through for engineers, and a schema-aware editor for
operators.

Security: the default bind is 127.0.0.1 (loopback) and is open. Any
non-loopback host requires EITHER --token <X> (X-Lattice-Token header)
OR --basic-auth user:pass (standard HTTP Basic). Both can be set; the
gate passes if either credential matches.

OIDC and similar SSO flows remain out of scope; for those, put a
reverse proxy in front and run Lattice with --host 127.0.0.1.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := openWorkspace(io)
			if err != nil {
				return io.fail("NO_WORKSPACE", err.Error(), nil)
			}
			user, pass, err := splitBasicAuth(basicAuth)
			if err != nil {
				return io.fail("BAD_BASIC_AUTH", err.Error(), nil)
			}
			srv := ui.New(ws, ui.Options{
				Host:          host,
				Port:          port,
				Token:         token,
				BasicAuthUser: user,
				BasicAuthPass: pass,
				GraphBuilder: func(ctx context.Context) (schema.KnowledgeGraph, error) {
					return buildGraph(ctx, ws, false)
				},
			})
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			io.printf("Lattice UI listening on %s\n", srv.Addr())
			if token != "" {
				io.printf("  token gate enabled — send X-Lattice-Token: %s on every request\n", token)
			}
			if user != "" {
				io.printf("  basic-auth gate enabled — user=%q\n", user)
			}
			io.printf("  press Ctrl-C to stop\n")

			if err := srv.ListenAndServe(ctx); err != nil {
				return io.fail("SERVE_FAILED", err.Error(), nil)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "host to bind (non-loopback requires --token or --basic-auth)")
	cmd.Flags().IntVar(&port, "port", 7070, "port to bind")
	cmd.Flags().StringVar(&token, "token", "", "pre-shared token (X-Lattice-Token header) for non-loopback bind")
	cmd.Flags().StringVar(&basicAuth, "basic-auth", "", "HTTP Basic credentials in user:pass form (alternative to --token)")
	return cmd
}

// splitBasicAuth parses "user:pass" into the two halves. Empty input
// returns empty strings with no error. A missing ":" is an error so a
// typo doesn't silently disable the gate.
func splitBasicAuth(spec string) (user, pass string, err error) {
	if spec == "" {
		return "", "", nil
	}
	i := strings.Index(spec, ":")
	if i < 0 {
		return "", "", errExit
	}
	user = strings.TrimSpace(spec[:i])
	pass = spec[i+1:]
	if user == "" || pass == "" {
		return "", "", errExit
	}
	return user, pass, nil
}
