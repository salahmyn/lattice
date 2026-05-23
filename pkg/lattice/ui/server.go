// Package ui is the Lattice v0.4.0 web UI — a Go-served, embedded-asset
// SPA that consumes the same KnowledgeGraph the CLI produces, with no
// build pipeline and no parallel runtime. See
// docs/v0.4.0-ui-proposal.md for the design.
package ui

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

//go:embed assets/templates/*.html assets/static/*
var assetsFS embed.FS

// Options configures a Server.
type Options struct {
	// Host the listener binds to. Empty means "127.0.0.1" (loopback).
	Host string
	// Port the listener binds to. Zero means 7070.
	Port int
	// Token, when set, is required as the X-Lattice-Token header on
	// every request when binding to a non-loopback host. Required by
	// the v0.4.0 security model: localhost is open, anything else needs
	// a pre-shared token.
	Token string
	// GraphBuilder is the source of the in-memory KnowledgeGraph. The
	// UI rebuilds on every request so a user edit to a manifest is
	// reflected on the next page load — no daemon-staleness surprises.
	// In tests this can be swapped for a fixture.
	GraphBuilder func(ctx context.Context) (schema.KnowledgeGraph, error)
}

// Server is the HTTP entry point. It is safe for concurrent use.
type Server struct {
	ws   *workspace.Workspace
	opts Options
	// tpl holds one isolated parsed tree per page so that each page's
	// {{define "body"}} block is scoped to its own page; without the
	// per-page isolation, Go templates flatten the namespace and the
	// last-parsed body wins for every page.
	tpl     map[string]*template.Template
	tplOnce sync.Once
	tplErr  error
	static  fs.FS
}

// New constructs a Server. Templates and static assets are loaded lazily
// on the first request so a misconfigured asset doesn't crash startup.
func New(ws *workspace.Workspace, opts Options) *Server {
	if opts.Port == 0 {
		opts.Port = 7070
	}
	if opts.Host == "" {
		opts.Host = "127.0.0.1"
	}
	static, _ := fs.Sub(assetsFS, "assets/static")
	return &Server{ws: ws, opts: opts, static: static}
}

// ListenAndServe binds the configured host/port and serves until ctx is
// cancelled. Returns once the server stops.
func (s *Server) ListenAndServe(ctx context.Context) error {
	if err := s.enforceTokenPolicy(); err != nil {
		return err
	}
	addr := fmt.Sprintf("%s:%d", s.opts.Host, s.opts.Port)
	mux := s.routes()
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.withMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()
	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}

// Addr returns the bound address ("host:port") so callers can print
// "open http://127.0.0.1:7070" without recomputing the parts.
func (s *Server) Addr() string {
	return fmt.Sprintf("http://%s:%d", s.opts.Host, s.opts.Port)
}

// enforceTokenPolicy implements the security model from the design doc:
// non-loopback binds must carry a pre-shared token. The token itself
// isn't validated here — that happens in middleware — but a missing
// token on a non-loopback host is rejected before listening.
func (s *Server) enforceTokenPolicy() error {
	if isLoopback(s.opts.Host) {
		return nil
	}
	if strings.TrimSpace(s.opts.Token) == "" {
		return fmt.Errorf("refusing to bind to non-loopback host %q without --token", s.opts.Host)
	}
	return nil
}

func isLoopback(host string) bool {
	switch host {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
}

// routes wires the HTTP mux. Pages render HTML; the /api/v1/* tree
// returns JSON. Static files come straight off the embedded FS.
func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()

	// Static assets — CSS, vendored JS — served straight from embed.
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(s.static))))

	// Pages (server-rendered HTML). Use {$} so "GET /" matches exactly
	// the root path — Go 1.22 rejects an unscoped "GET /" against the
	// "/static/" prefix handler as a conflict.
	mux.HandleFunc("GET /{$}", s.pageOverview)
	mux.HandleFunc("GET /features", s.pageFeatures)
	mux.HandleFunc("GET /features/{id}", s.pageFeature)
	mux.HandleFunc("GET /entry-points", s.pageEntryPoints)
	mux.HandleFunc("GET /entry-points/{id}", s.pageEntryPoint)
	mux.HandleFunc("GET /flows/{id}", s.pageFlow)
	mux.HandleFunc("GET /coverage", s.pageCoverage)
	mux.HandleFunc("GET /validation", s.pageValidation)
	mux.HandleFunc("GET /search", s.pageSearch)
	mux.HandleFunc("GET /import", s.pageImport)
	mux.HandleFunc("GET /import/{id}", s.pageImportCandidate)
	mux.HandleFunc("GET /config", s.pageConfig)

	// JSON API — same shapes as the CLI --json output.
	mux.HandleFunc("GET /api/v1/overview", s.apiOverview)
	mux.HandleFunc("GET /api/v1/features", s.apiFeatures)
	mux.HandleFunc("GET /api/v1/features/{id}", s.apiFeature)
	mux.HandleFunc("GET /api/v1/entry-points", s.apiEntryPoints)
	mux.HandleFunc("GET /api/v1/entry-points/{id}", s.apiEntryPoint)
	mux.HandleFunc("GET /api/v1/coverage", s.apiCoverage)
	mux.HandleFunc("GET /api/v1/validation", s.apiValidation)
	mux.HandleFunc("GET /api/v1/search", s.apiSearch)
	mux.HandleFunc("GET /api/v1/import/candidates", s.apiImportCandidates)
	mux.HandleFunc("GET /api/v1/import/candidates/{id}", s.apiImportCandidate)
	mux.HandleFunc("POST /api/v1/import/decisions", s.apiImportDecisions)
	mux.HandleFunc("GET /api/v1/config", s.apiConfig)
	mux.HandleFunc("PUT /api/v1/config", s.apiConfigPut)
	mux.HandleFunc("GET /api/v1/config/schema", s.apiConfigSchema)
	mux.HandleFunc("PUT /api/v1/config/fields", s.apiConfigFields)

	return mux
}

// withMiddleware wraps the mux with: panic recovery, token gate (when
// non-loopback), and a tiny request log line for diagnostics.
func (s *Server) withMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				http.Error(w, fmt.Sprintf("internal error: %v", rec), http.StatusInternalServerError)
			}
		}()
		if s.opts.Token != "" {
			provided := r.Header.Get("X-Lattice-Token")
			if provided == "" {
				provided = r.URL.Query().Get("token") // for the initial browser load
			}
			if provided != s.opts.Token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

// graph rebuilds the KnowledgeGraph from disk on every call. Cheap on
// small repos; for larger repos the next iteration will add a short
// in-memory cache invalidated by file mtime.
func (s *Server) graph(ctx context.Context) (schema.KnowledgeGraph, error) {
	if s.opts.GraphBuilder == nil {
		return schema.KnowledgeGraph{}, errors.New("server misconfigured: no GraphBuilder")
	}
	return s.opts.GraphBuilder(ctx)
}

// tplFuncs are template helpers used across pages.
var tplFuncs = template.FuncMap{
	"dict": func(args ...interface{}) map[string]interface{} {
		// dict turns ("Key" "Value" "Key2" "Value2") into a map so
		// templates can pass keyword-style arguments to sub-templates.
		out := map[string]interface{}{}
		for i := 0; i+1 < len(args); i += 2 {
			out[fmt.Sprint(args[i])] = args[i+1]
		}
		return out
	},
	"percent": func(ratio float64) string {
		return fmt.Sprintf("%.1f", ratio*100)
	},
	"shortFQN": func(s string) string {
		// Trim verbose namespaces to the last two segments for table
		// readability without losing identity.
		cls, method := s, ""
		if i := strings.LastIndex(s, "::"); i >= 0 {
			cls, method = s[:i], s[i:]
		}
		for _, sep := range []string{"\\", "/", "."} {
			segs := strings.Split(cls, sep)
			if len(segs) > 2 {
				cls = strings.Join(segs[len(segs)-2:], sep)
				break
			}
		}
		return cls + method
	},
	"len": func(v interface{}) int {
		switch x := v.(type) {
		case []schema.Manifest:
			return len(x)
		case []schema.EntryPoint:
			return len(x)
		case []string:
			return len(x)
		case []schema.GraphSymbol:
			return len(x)
		case []schema.Capability:
			return len(x)
		case []schema.Invariant:
			return len(x)
		case []schema.Implementation:
			return len(x)
		case []schema.FlowStep:
			return len(x)
		case []schema.SideEffect:
			return len(x)
		}
		return 0
	},
}

// templates loads the embedded HTML once and reuses it. Each page lives in
// its own parsed tree (a clone of layout.html + that one page), which
// isolates the page's {{define "body"}} block from every other page's body —
// without this isolation Go's flat template namespace makes the last-parsed
// body win for every page.
func (s *Server) templates() (map[string]*template.Template, error) {
	s.tplOnce.Do(func() {
		entries, err := assetsFS.ReadDir("assets/templates")
		if err != nil {
			s.tplErr = err
			return
		}
		// 1. Parse layout.html into the base set.
		layoutData, err := assetsFS.ReadFile("assets/templates/layout.html")
		if err != nil {
			s.tplErr = err
			return
		}
		base, err := template.New("").Funcs(tplFuncs).Parse(string(layoutData))
		if err != nil {
			s.tplErr = fmt.Errorf("template layout.html: %w", err)
			return
		}
		// 2. For every other page, clone the base and parse the page on top.
		pages := map[string]*template.Template{}
		for _, e := range entries {
			if e.Name() == "layout.html" {
				continue
			}
			data, err := assetsFS.ReadFile(filepath.Join("assets/templates", e.Name()))
			if err != nil {
				s.tplErr = err
				return
			}
			clone, err := base.Clone()
			if err != nil {
				s.tplErr = fmt.Errorf("clone base for %s: %w", e.Name(), err)
				return
			}
			if _, err := clone.Parse(string(data)); err != nil {
				s.tplErr = fmt.Errorf("template %s: %w", e.Name(), err)
				return
			}
			pages[e.Name()] = clone
		}
		s.tpl = pages
	})
	return s.tpl, s.tplErr
}
