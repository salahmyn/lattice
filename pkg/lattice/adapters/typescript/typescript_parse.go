package typescript

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	tsts "github.com/smacker/go-tree-sitter/typescript/typescript"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/astutil"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// tagKinds maps JSDoc tag names to Lattice annotation kinds.
var tagKinds = map[string]string{
	"feature":                   "feature",
	"capability":                "capability",
	"enforces":                  "enforces_invariant",
	"verifies":                  "verifies",
	"verifies-capability":       "verifies_capability",
	"depends-on-feature":        "depends_on_feature",
	"role":                      "role",
	"suppresses":                "suppresses_invariant",
	"surface":                   "surface",
	"error":                     "error",
	"module-feature":            "module_feature",
	"module-enforces":           "module_enforces_invariant",
	"module-depends-on-feature": "module_depends_on_feature",
}

// httpRouteMethods are the router method names auto-detected as HTTP surfaces
// (e.g. app.get("/x", h) / router.post("/y", h)).
var httpRouteMethods = map[string]bool{
	"get": true, "post": true, "put": true, "patch": true,
	"delete": true, "options": true, "head": true, "all": true,
}

// testCallNames are the call expressions treated as test symbols, so JSDoc
// annotations (notably @verifies) on idiomatic Jest/Vitest tests are captured.
var testCallNames = map[string]bool{
	"describe": true,
	"it":       true,
	"test":     true,
	"suite":    true,
	"context":  true,
}

// parse turns TypeScript/JavaScript source into an IR module.
func (a *Adapter) parse(ctx context.Context, path string, source []byte) (ir.Module, error) {
	mod := ir.Module{File: path, Language: a.Name()}

	parser := sitter.NewParser()
	parser.SetLanguage(tsts.GetLanguage())
	tree, err := parser.ParseCtx(ctx, nil, source)
	if err != nil {
		return mod, err
	}
	defer tree.Close()

	p := &tsParser{
		src:        source,
		mod:        &mod,
		modulePath: astutil.ModulePath(path),
		testFile:   isTestFile(path),
		testCounts: map[string]int{},
	}
	p.walkStatements(tree.RootNode())
	return mod, nil
}

type tsParser struct {
	src        []byte
	mod        *ir.Module
	modulePath string
	testFile   bool
	testCounts map[string]int // base FQN -> count, for synthetic test symbols
}

// walkStatements processes a statement list (the file root or a block),
// threading pending JSDoc annotations onto the next declaration or test call.
func (p *tsParser) walkStatements(parent *sitter.Node) {
	var pending []ir.Annotation
	for i := 0; i < int(parent.NamedChildCount()); i++ {
		n := parent.NamedChild(i)
		switch n.Type() {
		case "comment":
			if anns := p.parseJSDoc(n); anns != nil {
				pending = append(pending, anns...)
			}
		case "import_statement":
			// imports may sit between the module-header JSDoc and code;
			// keep pending annotations alive across them.
		case "export_statement":
			if decl := exportedDecl(n); decl != nil {
				p.handleDecl(decl, pending, true)
			}
			pending = nil
		case "function_declaration", "class_declaration", "abstract_class_declaration",
			"lexical_declaration", "variable_declaration", "interface_declaration":
			p.handleDecl(n, pending, false)
			pending = nil
		case "expression_statement":
			if call := innerCall(n); call != nil {
				p.handleCall(call, pending)
			}
			pending = nil
		default:
			pending = nil
		}
	}
}

// handleDecl records a declaration's symbol(s) with the given annotations.
// exported reports whether the declaration is part of the module's public API.
func (p *tsParser) handleDecl(n *sitter.Node, anns []ir.Annotation, exported bool) {
	switch n.Type() {
	case "function_declaration":
		p.addSymbol(p.fieldText(n, "name"), ir.KindFunction, "", n, anns, exported)
	case "class_declaration", "abstract_class_declaration":
		p.addClass(n, anns, exported)
	case "interface_declaration":
		p.addSymbol(p.fieldText(n, "name"), ir.KindInterface, "", n, anns, exported)
	case "lexical_declaration", "variable_declaration":
		for i := 0; i < int(n.NamedChildCount()); i++ {
			d := n.NamedChild(i)
			if d.Type() != "variable_declarator" {
				continue
			}
			val := d.ChildByFieldName("value")
			if val == nil {
				continue
			}
			switch val.Type() {
			case "arrow_function", "function", "function_expression":
				p.addSymbol(p.fieldText(d, "name"), ir.KindFunction, "", d, anns, exported)
			}
		}
	}
}

// handleCall dispatches a bare call statement to test-symbol capture and
// HTTP-route auto-detection.
func (p *tsParser) handleCall(call *sitter.Node, anns []ir.Annotation) {
	p.handleTestCall(call, anns)
	p.detectRoute(call)
}

// handleTestCall records a synthetic symbol for a Jest/Vitest-style test call
// and recurses into describe()/suite()/context() bodies for nested cases.
//
// To avoid double-counting, a test symbol is synthesized only when it would
// carry meaning: an it()/test() with an inline callback (the idiomatic case,
// where annotations attach to the call) or any test call carrying pending
// annotations. `it("desc", namedFn)` merely registers an already-captured
// named function and is not duplicated.
func (p *tsParser) handleTestCall(call *sitter.Node, anns []ir.Annotation) {
	fn := call.ChildByFieldName("function")
	if fn == nil {
		return
	}
	callee := calleeName(fn, p.src)
	if !testCallNames[callee] {
		return
	}
	args := call.ChildByFieldName("arguments")

	if callee == "describe" || callee == "suite" || callee == "context" {
		// A grouping call is structure, not a test — record it only when it
		// carries annotations. Always recurse for nested cases.
		if len(anns) > 0 {
			p.addTestSymbol(p.testDesc(args, callee), call, anns)
		}
		if body := callbackBody(args); body != nil {
			p.walkStatements(body)
		}
		return
	}
	if hasInlineCallback(args) || len(anns) > 0 {
		p.addTestSymbol(p.testDesc(args, callee), call, anns)
	}
}

// testDesc returns the first string argument of a test call, falling back to
// the callee name.
func (p *tsParser) testDesc(args *sitter.Node, callee string) string {
	if d := p.firstStringArg(args); d != "" {
		return d
	}
	return callee
}

// detectRoute records an HTTP surface for a framework route registration such
// as app.get("/path", handler) or router.post("/path", handler).
func (p *tsParser) detectRoute(call *sitter.Node) {
	fn := call.ChildByFieldName("function")
	if fn == nil || fn.Type() != "member_expression" {
		return
	}
	prop := fn.ChildByFieldName("property")
	if prop == nil {
		return
	}
	method := strings.ToLower(prop.Content(p.src))
	if !httpRouteMethods[method] {
		return
	}
	args := call.ChildByFieldName("arguments")
	path := p.firstStringArg(args)
	if !strings.HasPrefix(path, "/") {
		return // not a route path — avoid false positives like cache.get("k")
	}
	p.mod.Surfaces = append(p.mod.Surfaces, ir.Surface{
		Type: "http", Method: strings.ToUpper(method), Path: path,
		Line: line(call), Detected: true,
	})
}

// addClass records a class symbol and its method symbols.
func (p *tsParser) addClass(n *sitter.Node, anns []ir.Annotation, exported bool) {
	name := p.fieldText(n, "name")
	if name == "" {
		return
	}
	fqn := p.modulePath + "." + name
	p.mod.Symbols = append(p.mod.Symbols, ir.Symbol{
		Name: name, FQN: fqn, Kind: ir.KindClass,
		File: p.mod.File, Line: line(n), Annotations: anns,
		BaseClasses: p.extends(n), IsTest: p.testFile, Exported: exported,
	})

	body := n.ChildByFieldName("body")
	if body == nil {
		return
	}
	var pending []ir.Annotation
	for i := 0; i < int(body.NamedChildCount()); i++ {
		c := body.NamedChild(i)
		switch c.Type() {
		case "comment":
			if a := p.parseJSDoc(c); a != nil {
				pending = append(pending, a...)
			}
		case "method_definition":
			mname := p.fieldText(c, "name")
			// A method is part of the public surface only when its class is
			// and it carries no private/protected accessibility modifier.
			methodExported := exported && !hasPrivateModifier(c, p.src)
			p.addSymbol(mname, ir.KindMethod, fqn, c, pending, methodExported)
			pending = nil
		default:
			pending = nil
		}
	}
}

// addSymbol appends one symbol to the module.
func (p *tsParser) addSymbol(name string, kind ir.SymbolKind, enclosingFQN string, n *sitter.Node, anns []ir.Annotation, exported bool) {
	if name == "" {
		return
	}
	fqn := p.modulePath + "." + name
	if enclosingFQN != "" {
		fqn = enclosingFQN + "." + name
	}
	p.mod.Symbols = append(p.mod.Symbols, ir.Symbol{
		Name: name, FQN: fqn, Kind: kind,
		File: p.mod.File, Line: line(n), EnclosingFQN: enclosingFQN,
		Annotations: anns, IsTest: p.testFile || hasVerify(anns), Exported: exported,
	})
}

// addTestSymbol records a synthetic symbol for a test-framework call. The test
// description becomes the symbol name; the FQN is slugified and de-duplicated.
func (p *tsParser) addTestSymbol(desc string, n *sitter.Node, anns []ir.Annotation) {
	base := p.modulePath + ".test." + slugify(desc)
	fqn := base
	if c := p.testCounts[base]; c > 0 {
		fqn = fmt.Sprintf("%s_%d", base, c+1)
	}
	p.testCounts[base]++
	p.mod.Symbols = append(p.mod.Symbols, ir.Symbol{
		Name: desc, FQN: fqn, Kind: ir.KindFunction,
		File: p.mod.File, Line: line(n), Annotations: anns,
		IsTest: true, Exported: false,
	})
}

// extends returns the base classes from a class heritage clause.
func (p *tsParser) extends(n *sitter.Node) []string {
	var bases []string
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if c.Type() != "class_heritage" {
			continue
		}
		for j := 0; j < int(c.NamedChildCount()); j++ {
			ec := c.NamedChild(j)
			if ec.Type() == "extends_clause" {
				for k := 0; k < int(ec.NamedChildCount()); k++ {
					id := ec.NamedChild(k)
					if t := id.Type(); t == "identifier" || t == "member_expression" {
						bases = append(bases, p.modulePath+"."+id.Content(p.src))
					}
				}
			}
		}
	}
	return bases
}

// parseJSDoc turns a /** ... */ comment into annotations. Module-level tags
// are routed straight to the module; the rest are returned for the next
// declaration.
func (p *tsParser) parseJSDoc(n *sitter.Node) []ir.Annotation {
	text := n.Content(p.src)
	if !strings.HasPrefix(text, "/**") {
		return nil
	}
	startLine := line(n)
	var symbolAnns []ir.Annotation
	for _, raw := range strings.Split(text, "\n") {
		// Normalize a comment line: drop the /** , /* , * openers and the */
		// closer so tags are found whether the comment is one line or many.
		ln := strings.TrimSpace(raw)
		ln = strings.TrimPrefix(ln, "/**")
		ln = strings.TrimPrefix(ln, "/*")
		ln = strings.TrimPrefix(ln, "*")
		ln = strings.TrimSuffix(ln, "*/")
		ln = strings.TrimSpace(ln)
		if !strings.HasPrefix(ln, "@") {
			continue
		}
		// One line may carry several tags (e.g. `@feature x @enforces INV-1`).
		for _, seg := range strings.Split(ln, "@") {
			seg = strings.TrimSpace(seg)
			if seg == "" {
				continue
			}
			fields := strings.SplitN(seg, " ", 2)
			kind, ok := tagKinds[fields[0]]
			if !ok {
				continue
			}
			ann := ir.Annotation{Kind: kind, Line: startLine}
			if len(fields) == 2 {
				p.applyArgs(&ann, strings.TrimSpace(fields[1]))
			}
			if strings.HasPrefix(kind, "module_") {
				p.mod.ModuleAnnotations = append(p.mod.ModuleAnnotations, ann)
			} else {
				symbolAnns = append(symbolAnns, ann)
			}
		}
	}
	return symbolAnns
}

// applyArgs splits a JSDoc tag's argument text into annotation args. A
// "reason:" segment becomes a reason kwarg (used by @suppresses).
func (p *tsParser) applyArgs(ann *ir.Annotation, text string) {
	if idx := strings.Index(text, "reason:"); idx >= 0 {
		reason := strings.TrimSpace(text[idx+len("reason:"):])
		text = strings.TrimSpace(text[:idx])
		ann.Kwargs = map[string]interface{}{"reason": reason}
	}
	for _, part := range strings.Split(text, ",") {
		if v := strings.TrimSpace(part); v != "" {
			ann.Args = append(ann.Args, v)
		}
	}
}

func (p *tsParser) fieldText(n *sitter.Node, field string) string {
	if c := n.ChildByFieldName(field); c != nil {
		return c.Content(p.src)
	}
	return ""
}

// firstStringArg returns the unquoted text of the first string argument in an
// arguments node, or "" if there is none.
func (p *tsParser) firstStringArg(args *sitter.Node) string {
	if args == nil {
		return ""
	}
	for i := 0; i < int(args.NamedChildCount()); i++ {
		c := args.NamedChild(i)
		if c.Type() != "string" {
			continue
		}
		for j := 0; j < int(c.NamedChildCount()); j++ {
			if frag := c.NamedChild(j); frag.Type() == "string_fragment" {
				return frag.Content(p.src)
			}
		}
		return "" // empty string literal
	}
	return ""
}

// --- helpers ---

func line(n *sitter.Node) int { return int(n.StartPoint().Row) + 1 }

// exportedDecl unwraps the declaration inside an export_statement.
func exportedDecl(n *sitter.Node) *sitter.Node {
	if d := n.ChildByFieldName("declaration"); d != nil {
		return d
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		switch c.Type() {
		case "function_declaration", "class_declaration", "abstract_class_declaration",
			"lexical_declaration", "variable_declaration", "interface_declaration":
			return c
		}
	}
	return nil
}

// innerCall returns the call_expression directly inside an expression_statement,
// or nil if the statement is not a bare call.
func innerCall(stmt *sitter.Node) *sitter.Node {
	for i := 0; i < int(stmt.NamedChildCount()); i++ {
		if c := stmt.NamedChild(i); c.Type() == "call_expression" {
			return c
		}
	}
	return nil
}

// calleeName resolves the callee of a call expression to a bare name,
// unwrapping member access so describe.only / it.skip still resolve.
func calleeName(fn *sitter.Node, src []byte) string {
	switch fn.Type() {
	case "identifier":
		return fn.Content(src)
	case "member_expression":
		if obj := fn.ChildByFieldName("object"); obj != nil && obj.Type() == "identifier" {
			return obj.Content(src)
		}
	}
	return ""
}

// hasInlineCallback reports whether an arguments node contains an inline
// function or arrow expression (as opposed to a bare identifier reference).
func hasInlineCallback(args *sitter.Node) bool {
	if args == nil {
		return false
	}
	for i := 0; i < int(args.NamedChildCount()); i++ {
		switch args.NamedChild(i).Type() {
		case "arrow_function", "function", "function_expression", "generator_function":
			return true
		}
	}
	return false
}

// callbackBody returns the statement block of the first function/arrow
// argument in an arguments node, or nil.
func callbackBody(args *sitter.Node) *sitter.Node {
	if args == nil {
		return nil
	}
	for i := 0; i < int(args.NamedChildCount()); i++ {
		c := args.NamedChild(i)
		switch c.Type() {
		case "arrow_function", "function", "function_expression", "generator_function":
			if body := c.ChildByFieldName("body"); body != nil && body.Type() == "statement_block" {
				return body
			}
		}
	}
	return nil
}

// hasPrivateModifier reports whether a method carries a private/protected
// accessibility modifier.
func hasPrivateModifier(method *sitter.Node, src []byte) bool {
	for i := 0; i < int(method.NamedChildCount()); i++ {
		c := method.NamedChild(i)
		if c.Type() == "accessibility_modifier" {
			switch c.Content(src) {
			case "private", "protected":
				return true
			}
		}
	}
	return false
}

// slugify turns a free-text test description into an FQN-safe slug.
func slugify(s string) string {
	var b strings.Builder
	lastDash := true // suppress a leading dash
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "test"
	}
	return out
}

func hasVerify(anns []ir.Annotation) bool {
	for _, a := range anns {
		if a.Kind == "verifies" || a.Kind == "verifies_capability" {
			return true
		}
	}
	return false
}

// isTestFile reports whether a TS/JS path is test code.
func isTestFile(path string) bool {
	if astutil.IsTestPath(path) {
		return true
	}
	base := filepath.Base(path)
	for _, suf := range []string{".test.ts", ".test.tsx", ".spec.ts", ".spec.tsx",
		".test.js", ".test.jsx", ".spec.js", ".spec.jsx"} {
		if strings.HasSuffix(base, suf) {
			return true
		}
	}
	return false
}
