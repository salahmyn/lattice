// Package fastapi detects FastAPI entry points — @app.method and
// @router.method decorators in Python source — proving the v0.3.0
// detector framework is genuinely cross-framework, not Laravel-only.
package fastapi

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/entrypoints"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// HTTPDetector finds FastAPI HTTP routes. Recognises @app.<verb>("path")
// and @router.<verb>("path") immediately above an `async? def name(...)`
// declaration. The function symbol is the handler; the path/verb on
// the decorator is the trigger.
//
// Mounting routers under custom names (e.g. `@accounts.get`) is also
// matched because the decorator-target regex is "anything ending in
// .<verb>(...)" — false positives are filtered out by requiring the
// matched function to exist in the Python IR.
type HTTPDetector struct{}

func init() { entrypoints.Register(HTTPDetector{}) }

// Name implements entrypoints.Detector.
func (HTTPDetector) Name() string { return "fastapi" }

// Detect implements entrypoints.Detector.
func (HTTPDetector) Detect(_ context.Context, ws *workspace.Workspace, modules []ir.Module) ([]schema.EntryPoint, error) {
	pythonModules := pythonOnly(modules)
	if len(pythonModules) == 0 {
		return nil, nil
	}
	// Index functions by (file, name) so a decorator match can resolve
	// to a real symbol FQN.
	type key struct{ file, name string }
	byFunc := map[key]ir.Symbol{}
	for _, m := range pythonModules {
		for _, s := range m.Symbols {
			if s.Kind != ir.KindFunction && s.Kind != ir.KindMethod {
				continue
			}
			byFunc[key{m.File, lastSegment(s.FQN)}] = s
		}
	}

	var out []schema.EntryPoint
	seen := map[string]bool{}
	for _, m := range pythonModules {
		body := readModuleSource(ws, m.File)
		if body == "" {
			continue
		}
		for _, r := range scanRoutes(body) {
			sym, ok := byFunc[key{m.File, r.handler}]
			if !ok {
				continue // probably a closure / type alias / false positive
			}
			id := httpEntryPointID(r.method, r.path)
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, schema.EntryPoint{
				ID:      id,
				Version: 1,
				Status:  schema.StatusProposal,
				Kind:    schema.EntryPointKindHTTP,
				Trigger: schema.Trigger{Method: r.method, Path: r.path},
				Handler: schema.Handler{Symbol: sym.FQN, File: sym.File, Line: sym.Line},
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// fastapiRoute is one decorator+def pair we matched.
type fastapiRoute struct {
	method, path, handler string
}

// routeDecorator captures @<expr>.<verb>(path[, ...]) immediately
// preceding a function definition.
//
// Two subtleties: (1) the decorator args can span multiple lines for
// response_model= / dependencies= etc — (?s) lets `.` cross newlines.
// (2) Decorator args can contain nested parens (e.g. dependencies=
// [Depends(auth)]) — a simple `[^)]*\)` stops at the first inner
// paren and truncates the match. Instead we anchor the END of the
// pattern on `\s*\n\s*(?:async\s+)?def`, then let `.*?` walk the args
// (lazy, so the trailing structure dictates how much is consumed).
var routeDecorator = regexp.MustCompile(
	`(?s)@([A-Za-z_][A-Za-z0-9_]*)\.(get|post|put|patch|delete|options|head|websocket)\(\s*['"]([^'"]+)['"].*?\)\s*\n\s*(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)`,
)

func scanRoutes(body string) []fastapiRoute {
	var out []fastapiRoute
	for _, m := range routeDecorator.FindAllStringSubmatch(body, -1) {
		_ = m[1] // decorator subject (app/router/etc) — informational only
		out = append(out, fastapiRoute{
			method:  strings.ToUpper(m[2]),
			path:    m[3],
			handler: m[4],
		})
	}
	return out
}

// httpEntryPointID renders the same dotted-id shape Laravel uses so
// the two detectors don't collide on identical method+path triggers
// across frameworks.
func httpEntryPointID(method, path string) string {
	segs := strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '{' || r == '}' || r == ':'
	})
	for i, s := range segs {
		segs[i] = strings.ToLower(s)
	}
	id := "ep.http." + strings.ToLower(method)
	if len(segs) > 0 {
		id += "." + strings.Join(segs, ".")
	}
	return id
}

// readModuleSource is fastapi's analogue of laravel.fileReader; kept
// inline so the two detectors don't share a private helper across
// package boundaries.
func readModuleSource(ws *workspace.Workspace, relFile string) string {
	if ws == nil {
		return ""
	}
	for _, root := range ws.CodeRoots {
		if !root.Available {
			continue
		}
		var abs string
		if strings.HasPrefix(relFile, root.Name+"/") {
			abs = filepath.Join(root.Abs, strings.TrimPrefix(relFile, root.Name+"/"))
		} else {
			abs = filepath.Join(root.Abs, relFile)
		}
		if data, err := os.ReadFile(abs); err == nil {
			return string(data)
		}
	}
	return ""
}

func pythonOnly(modules []ir.Module) []ir.Module {
	out := modules[:0:0]
	for _, m := range modules {
		if m.Language == "python" {
			out = append(out, m)
		}
	}
	return out
}

// lastSegment returns the dotted-tail of a Python FQN — "pkg.mod.fn"
// -> "fn". For methods FQN is typically "pkg.mod.Class.method", and
// the decorator-target match needs the method name only.
func lastSegment(fqn string) string {
	if i := strings.LastIndex(fqn, "."); i >= 0 {
		return fqn[i+1:]
	}
	return fqn
}

var _ entrypoints.Detector = HTTPDetector{}
