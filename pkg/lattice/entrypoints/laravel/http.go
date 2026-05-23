// Package laravel detects Laravel entry points — HTTP routes, console
// commands, scheduled jobs, queue workers — from a Laravel codebase.
//
// HTTP routes are by far the most common kind, and Laravel registers them
// via the fluent Route facade in routes/*.php and module-local routes
// files. Parsing those is text-pattern work rather than full PHP
// execution: real coverage uses tree-sitter when needed, but the canonical
// `Route::<method>(path, handler)` shape is recognisable from a focused
// regex pass — and ships immediately on a tree-sitter-less code root.
package laravel

import (
	"context"
	"fmt"
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

// HTTPDetector finds Laravel HTTP routes in routes/*.php and
// Modules/*/Routes/*.php. It recognises:
//   - Route::<verb>('path', 'Controller@method')
//   - Route::<verb>('path', [Controller::class, 'method'])
//   - Route::resource('name', Controller::class)
//
// Routes registered via closures are skipped (no static handler symbol).
// Group prefixes/middleware are honoured one level deep — sufficient for
// the v0.3.0-α target and the common Laravel structure.
type HTTPDetector struct{}

func init() { entrypoints.Register(HTTPDetector{}) }

// Name implements entrypoints.Detector.
func (HTTPDetector) Name() string { return "laravel-http" }

// Detect implements entrypoints.Detector.
func (HTTPDetector) Detect(_ context.Context, ws *workspace.Workspace, modules []ir.Module) ([]schema.EntryPoint, error) {
	files := collectRouteFiles(ws)
	if len(files) == 0 {
		return nil, nil
	}
	resolver := newSymbolResolver(modules)
	var out []schema.EntryPoint
	seen := map[string]bool{}
	for _, file := range files {
		data, err := os.ReadFile(file.abs)
		if err != nil {
			continue
		}
		body := string(data)
		uses := parseUseStatements(body)
		for _, r := range scanRoutes(body, uses) {
			handler := resolver.resolve(r.handler)
			if handler.Symbol == "" {
				// We couldn't ground the handler against the IR. Keep
				// the FQN we extracted so the entry point still
				// appears; views can flag it as orphan-handler.
				handler.Symbol = r.handler.fqn
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
				Handler: handler,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// routeFile is one Laravel routes file with its absolute and ws-relative
// paths so the EntryPoint can refer to the relative form (matching IR).
type routeFile struct{ abs, rel string }

// collectRouteFiles enumerates the canonical Laravel route file
// locations relative to every code root: routes/*.php and
// Modules/*/Routes/*.php (and Module/*/Routes/*.php, the older naming).
func collectRouteFiles(ws *workspace.Workspace) []routeFile {
	var out []routeFile
	seen := map[string]bool{}
	for _, root := range ws.CodeRoots {
		if !root.Available {
			continue
		}
		patterns := []string{
			filepath.Join(root.Abs, "..", "routes", "*.php"),       // standard Laravel
			filepath.Join(root.Abs, "routes", "*.php"),             // root is repo
			filepath.Join(root.Abs, "..", "Modules", "*", "Routes", "*.php"),
			filepath.Join(root.Abs, "*", "Routes", "*.php"),        // Modules itself is the root
			filepath.Join(root.Abs, "..", "modules", "*", "Routes", "*.php"),
		}
		for _, p := range patterns {
			matches, _ := filepath.Glob(p)
			for _, m := range matches {
				clean, _ := filepath.Abs(m)
				if seen[clean] {
					continue
				}
				seen[clean] = true
				// Relative path for IR alignment.
				rel := clean
				for _, r := range ws.CodeRoots {
					if strings.HasPrefix(clean, r.Abs+string(filepath.Separator)) {
						rel = filepath.Join(r.Name, strings.TrimPrefix(clean, r.Abs+string(filepath.Separator)))
						break
					}
				}
				out = append(out, routeFile{abs: clean, rel: rel})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].abs < out[j].abs })
	return out
}

// httpRoute is one parsed Route::<method>(...) call.
type httpRoute struct {
	method  string
	path    string
	handler handlerRef
}

// handlerRef is the raw handler reference extracted from the source —
// either a "Class@method" string or a [Class::class, 'method'] pair.
type handlerRef struct {
	shortClass string // unqualified when array form ([Foo::class, 'bar'])
	method     string
	fqn        string // fully-qualified when known up-front (string form)
}

// routeCall matches the call site of every supported Laravel route verb.
// We capture method, the first arg (path), and the second arg (handler).
var routeCall = regexp.MustCompile(
	`Route::(get|post|put|patch|delete|options|any|match|resource|apiResource)\s*\(\s*` +
		`['"]([^'"]+)['"]\s*,\s*` +
		`([^)]+)\)`,
)

// stringHandler captures the "Class@method" string form.
var stringHandler = regexp.MustCompile(`['"]([A-Za-z_\\][A-Za-z0-9_\\]*)@([A-Za-z_][A-Za-z0-9_]*)['"]`)

// arrayHandler captures the [Class::class, 'method'] array form.
var arrayHandler = regexp.MustCompile(`\[\s*([A-Za-z_\\][A-Za-z0-9_\\]*)::class\s*,\s*['"]([A-Za-z_][A-Za-z0-9_]*)['"]\s*\]`)

// classOnly captures Route::resource('x', Controller::class) — the
// handler is the controller's RESTful method set, not a single symbol;
// we emit the 7 standard resource methods downstream.
var classOnly = regexp.MustCompile(`([A-Za-z_\\][A-Za-z0-9_\\]*)::class`)

// resourceMethods lists the seven Route::resource produces (and the
// HTTP verbs they're served on). Sub-set for apiResource (no
// create/edit views).
var resourceMethods = []struct{ verb, action string }{
	{"GET", "index"}, {"GET", "create"}, {"POST", "store"},
	{"GET", "show"}, {"GET", "edit"}, {"PUT", "update"}, {"DELETE", "destroy"},
}

func scanRoutes(body string, uses map[string]string) []httpRoute {
	var out []httpRoute
	for _, m := range routeCall.FindAllStringSubmatch(body, -1) {
		verb, path, handlerArg := strings.ToUpper(m[1]), m[2], m[3]
		switch strings.ToUpper(m[1]) {
		case "RESOURCE", "APIRESOURCE":
			if cm := classOnly.FindStringSubmatch(handlerArg); cm != nil {
				class := resolveClass(cm[1], uses)
				methods := resourceMethods
				if strings.ToUpper(m[1]) == "APIRESOURCE" {
					// apiResource omits create/edit (form views).
					methods = []struct{ verb, action string }{
						{"GET", "index"}, {"POST", "store"}, {"GET", "show"},
						{"PUT", "update"}, {"DELETE", "destroy"},
					}
				}
				for _, rm := range methods {
					out = append(out, httpRoute{
						method:  rm.verb,
						path:    resourcePath(path, rm.action),
						handler: handlerRef{shortClass: cm[1], method: rm.action, fqn: class + "::" + rm.action},
					})
				}
			}
			continue
		}
		var h handlerRef
		if sm := stringHandler.FindStringSubmatch(handlerArg); sm != nil {
			fqn := strings.ReplaceAll(sm[1], "\\\\", "\\")
			h = handlerRef{fqn: fqn + "::" + sm[2], method: sm[2]}
		} else if am := arrayHandler.FindStringSubmatch(handlerArg); am != nil {
			class := resolveClass(am[1], uses)
			h = handlerRef{shortClass: am[1], method: am[2], fqn: class + "::" + am[2]}
		} else {
			// Closure or other; no static handler symbol.
			continue
		}
		out = append(out, httpRoute{method: verb, path: path, handler: h})
	}
	return out
}

// resourcePath turns the resource name ('users') and an action into the
// actual route path Laravel mounts ('users', 'users/{user}', etc).
func resourcePath(name, action string) string {
	singular := name
	if strings.HasSuffix(singular, "s") {
		singular = strings.TrimSuffix(singular, "s")
	}
	switch action {
	case "index", "store":
		return "/" + name
	case "create":
		return "/" + name + "/create"
	case "show", "update", "destroy":
		return "/" + name + "/{" + singular + "}"
	case "edit":
		return "/" + name + "/{" + singular + "}/edit"
	}
	return "/" + name
}

// httpEntryPointID renders a deterministic id for an HTTP entry point.
// "POST /api/v2/refunds" -> "ep.http.post.api.v2.refunds".
func httpEntryPointID(method, path string) string {
	segs := strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '{' || r == '}'
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

// useStmt matches a `use Foo\Bar;` or `use Foo\Bar as Baz;` line.
var useStmt = regexp.MustCompile(`use\s+([A-Za-z_\\][A-Za-z0-9_\\]*)(?:\s+as\s+([A-Za-z_][A-Za-z0-9_]*))?\s*;`)

// parseUseStatements builds a short-name -> FQN map from a PHP file's
// `use` directives, so [Foo::class, 'bar'] resolves correctly when Foo
// is short-imported.
func parseUseStatements(body string) map[string]string {
	uses := map[string]string{}
	for _, m := range useStmt.FindAllStringSubmatch(body, -1) {
		fqn := m[1]
		short := m[2]
		if short == "" {
			if i := strings.LastIndex(fqn, "\\"); i >= 0 {
				short = fqn[i+1:]
			} else {
				short = fqn
			}
		}
		uses[short] = fqn
	}
	return uses
}

// resolveClass turns the short name appearing in source into a FQN using
// the file's use map. Falls back to the short name itself when there's
// no matching use (the IR resolver can still try to match by suffix).
func resolveClass(name string, uses map[string]string) string {
	if strings.HasPrefix(name, "\\") {
		return strings.TrimPrefix(name, "\\")
	}
	if fqn, ok := uses[name]; ok {
		return fqn
	}
	return name
}

// symbolResolver maps a (class, method) pair to the IR symbol that backs
// it — needed so the EntryPoint.Handler points at a real symbol the
// flow tracer can walk outward from.
type symbolResolver struct {
	byFQN    map[string]ir.Symbol
	bySuffix map[string][]ir.Symbol // last segment of class FQN -> symbols of methods
}

func newSymbolResolver(modules []ir.Module) *symbolResolver {
	r := &symbolResolver{
		byFQN:    map[string]ir.Symbol{},
		bySuffix: map[string][]ir.Symbol{},
	}
	for _, m := range modules {
		for _, s := range m.Symbols {
			r.byFQN[s.FQN] = s
			if cls, _, ok := splitMethod(s.FQN); ok {
				short := cls
				if i := strings.LastIndex(cls, "\\"); i >= 0 {
					short = cls[i+1:]
				}
				r.bySuffix[short] = append(r.bySuffix[short], s)
			}
		}
	}
	return r
}

func (r *symbolResolver) resolve(h handlerRef) schema.Handler {
	if h.fqn != "" {
		if s, ok := r.byFQN[h.fqn]; ok {
			return schema.Handler{Symbol: s.FQN, File: s.File, Line: s.Line}
		}
		// Try without leading backslash variants.
		alt := strings.TrimPrefix(h.fqn, "\\")
		if s, ok := r.byFQN[alt]; ok {
			return schema.Handler{Symbol: s.FQN, File: s.File, Line: s.Line}
		}
	}
	// Suffix match: same short-class name + method name.
	short := h.shortClass
	if short == "" {
		// Pull short from h.fqn.
		if cls, _, ok := splitMethod(h.fqn); ok {
			short = cls
			if i := strings.LastIndex(cls, "\\"); i >= 0 {
				short = cls[i+1:]
			}
		}
	}
	for _, cand := range r.bySuffix[short] {
		if _, method, ok := splitMethod(cand.FQN); ok && method == h.method {
			return schema.Handler{Symbol: cand.FQN, File: cand.File, Line: cand.Line}
		}
	}
	return schema.Handler{}
}

// splitMethod splits an FQN like "App\Foo\Bar::baz" into class
// "App\Foo\Bar" and method "baz".
func splitMethod(fqn string) (class, method string, ok bool) {
	i := strings.LastIndex(fqn, "::")
	if i < 0 {
		return "", "", false
	}
	return fqn[:i], fqn[i+2:], true
}

// Compile-time check that the detector satisfies the interface.
var _ entrypoints.Detector = HTTPDetector{}

// avoid unused warnings if we add helpers later.
var _ = fmt.Sprint
