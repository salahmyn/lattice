package typescript

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	tstsx "github.com/smacker/go-tree-sitter/typescript/tsx"
	tsts "github.com/smacker/go-tree-sitter/typescript/typescript"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/astutil"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
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
	parser.SetLanguage(grammarFor(path))
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

// grammarFor picks the tree-sitter grammar for a path. Only .ts/.mts/.cts use
// the plain TypeScript grammar, where <T>expr is a type assertion; every other
// extension may contain JSX, which that grammar cannot parse (a React
// component in a .tsx file would come out as an error tree with no symbols).
// The tsx grammar is a superset for JavaScript, so .js/.mjs/.cjs use it too.
func grammarFor(path string) *sitter.Language {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts", ".mts", ".cts":
		return tsts.GetLanguage()
	}
	return tstsx.GetLanguage()
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
			if decl := exportedDecl(n); decl == nil || !p.handleDecl(decl, pending, true) {
				p.orphan(pending, n)
			}
			pending = nil
		case "function_declaration", "class_declaration", "abstract_class_declaration",
			"lexical_declaration", "variable_declaration", "interface_declaration":
			if !p.handleDecl(n, pending, false) {
				p.orphan(pending, n)
			}
			pending = nil
		case "expression_statement":
			if call := innerCall(n); call == nil || !p.handleCall(call, pending) {
				p.orphan(pending, n)
			}
			pending = nil
		default:
			p.orphan(pending, n)
			pending = nil
		}
	}
	p.orphan(pending, nil)
}

// orphan reports annotations that attach to no symbol. They used to be
// dropped silently, so an @feature or @verifies above a construct the adapter
// does not model (an object literal, a bare statement, or a file the grammar
// could not parse) looked declared but never reached the graph. at is the
// node that followed the annotations; nil means the end of the block.
func (p *tsParser) orphan(anns []ir.Annotation, at *sitter.Node) {
	if len(anns) == 0 {
		return
	}
	ln, what := 0, "the end of the block"
	if at != nil {
		ln, what = line(at), "a "+strings.ReplaceAll(at.Type(), "_", " ")
	}
	kinds := make([]string, 0, len(anns))
	for _, a := range anns {
		kinds = append(kinds, "@"+a.Kind)
	}
	if ln == 0 {
		ln = anns[0].Line
	}
	p.mod.Diagnostics = append(p.mod.Diagnostics, ir.Diagnostic{
		Line: ln, Code: schema.CodeAnnotationOrphaned,
		Message: fmt.Sprintf("%s attaches to no symbol: it is followed by %s, which declares no function, class or test",
			strings.Join(kinds, ", "), what),
	})
}

// handleDecl records a declaration's symbol(s) with the given annotations.
// exported reports whether the declaration is part of the module's public API.
// It reports whether the declaration produced a symbol for the annotations.
func (p *tsParser) handleDecl(n *sitter.Node, anns []ir.Annotation, exported bool) bool {
	switch n.Type() {
	case "function_declaration":
		name := p.fieldText(n, "name")
		ok := p.addSymbol(name, ir.KindFunction, "", n, anns, exported)
		if ok {
			p.walkBody(n.ChildByFieldName("body"), p.modulePath+"."+name)
		}
		return ok
	case "class_declaration", "abstract_class_declaration":
		return p.addClass(n, anns, exported)
	case "interface_declaration":
		return p.addSymbol(p.fieldText(n, "name"), ir.KindInterface, "", n, anns, exported)
	case "lexical_declaration", "variable_declaration":
		added := false
		for i := 0; i < int(n.NamedChildCount()); i++ {
			d := n.NamedChild(i)
			if d.Type() != "variable_declarator" {
				continue
			}
			val := d.ChildByFieldName("value")
			if val == nil {
				continue
			}
			name := p.fieldText(d, "name")
			switch val.Type() {
			case "arrow_function", "function", "function_expression":
				if p.addSymbol(name, ir.KindFunction, "", d, anns, exported) {
					added = true
					p.walkBody(val.ChildByFieldName("body"), p.modulePath+"."+name)
				}
			default:
				// A value const (a client, a config object) is a symbol only
				// when something is said about it: annotations on the const,
				// or annotated methods in an object literal.
				if p.addConst(name, d, val, anns, exported) {
					added = true
				}
			}
		}
		return added
	}
	return false
}

// handleCall dispatches a bare call statement to test-symbol capture and
// HTTP-route auto-detection.
// It reports whether the call produced a symbol for the annotations.
func (p *tsParser) handleCall(call *sitter.Node, anns []ir.Annotation) bool {
	consumed := p.handleTestCall(call, anns)
	p.detectRoute(call)
	return consumed
}

// handleTestCall records a synthetic symbol for a Jest/Vitest-style test call
// and recurses into describe()/suite()/context() bodies for nested cases.
//
// To avoid double-counting, a test symbol is synthesized only when it would
// carry meaning: an it()/test() with an inline callback (the idiomatic case,
// where annotations attach to the call) or any test call carrying pending
// annotations. `it("desc", namedFn)` merely registers an already-captured
// named function and is not duplicated. It reports whether a symbol carrying
// the annotations was recorded.
func (p *tsParser) handleTestCall(call *sitter.Node, anns []ir.Annotation) bool {
	fn := call.ChildByFieldName("function")
	if fn == nil {
		return false
	}
	if fn.Type() == "call_expression" {
		return p.handleEachCall(call, fn, anns)
	}
	callee := calleeName(fn, p.src)
	if !testCallNames[callee] {
		return false
	}
	args := call.ChildByFieldName("arguments")
	kind := classifyTestCall(fn, p.src)
	if kind == notATest {
		return false
	}
	// A modifier call (test.skip, it.only, test.fail, ...) declares a test only
	// when its first argument is the title; forms such as test.skip(cond,
	// "reason") toggle the surrounding test and are not tests themselves.
	if kind == testCase && fn.Type() == "member_expression" && !denoTest(fn, p.src) &&
		!firstArgIsTitle(args) && len(anns) == 0 {
		return false
	}

	if kind == testGroup {
		// A grouping call is structure, not a test — record it only when it
		// carries annotations. Always recurse for nested cases.
		if len(anns) > 0 {
			p.addTestSymbol(p.testDesc(args, callee), call, anns)
		}
		if body := callbackBody(args); body != nil {
			p.walkStatements(body)
		}
		return len(anns) > 0
	}
	if hasInlineCallback(args) || len(anns) > 0 {
		p.addTestSymbol(p.testDesc(args, callee), call, anns)
		return true
	}
	return false
}

// handleEachCall handles a table-driven test, it.each(table)("title", fn) or
// describe.each(table)(...): call is the outer call, inner is it.each(table).
// The title is a template ("%s", "$name", "${x}" fill it per row), so the
// test is named by templateSlug and its results join every row's case.
func (p *tsParser) handleEachCall(call, inner *sitter.Node, anns []ir.Annotation) bool {
	fn := inner.ChildByFieldName("function")
	if fn == nil {
		return false
	}
	chain := memberChain(fn, p.src)
	if len(chain) < 2 || chain[len(chain)-1] != "each" || !testCallNames[chain[0]] {
		return false
	}
	args := call.ChildByFieldName("arguments")
	group := false
	for _, part := range chain[:len(chain)-1] {
		if part == "describe" || part == "suite" || part == "context" {
			group = true
		}
	}
	if group {
		if len(anns) > 0 {
			p.addTestSymbolSlug(p.firstStringArg(args), templateSlug(p.firstStringArg(args)), call, anns)
		}
		if body := callbackBody(args); body != nil {
			p.walkStatements(body)
		}
		return len(anns) > 0
	}
	if !hasInlineCallback(args) && len(anns) == 0 {
		return false
	}
	title := p.firstStringArg(args)
	p.addTestSymbolSlug(title, templateSlug(title), call, anns)
	return true
}

// eachPlaceholder matches the per-row placeholders of jest/vitest .each
// titles: printf verbs (%s %d %i %f %j %o %O %p %#) and $name / ${expr}.
var eachPlaceholder = regexp.MustCompile(`%[sdifjoOp#]|\$\{[^}]*\}|\$[A-Za-z_][A-Za-z0-9_.]*`)

// templateSlug slugs a table-test title, marking each placeholder with "_"
// (which slugify never emits), so the result join can match every row:
// "fails when the %s read fails" -> "fails-when-the-_-read-fails".
func templateSlug(title string) string {
	if !eachPlaceholder.MatchString(title) {
		return slugify(title)
	}
	var tokens []string
	for i, part := range eachPlaceholder.Split(title, -1) {
		if i > 0 {
			tokens = append(tokens, "_")
		}
		if hasAlnum(part) {
			tokens = append(tokens, slugify(part))
		}
	}
	return strings.Join(tokens, "-")
}

// hasAlnum reports whether s holds a letter or digit slugify would keep.
func hasAlnum(s string) bool {
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return true
		}
	}
	return false
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
func (p *tsParser) addClass(n *sitter.Node, anns []ir.Annotation, exported bool) bool {
	name := p.fieldText(n, "name")
	if name == "" {
		return false
	}
	fqn := p.modulePath + "." + name
	p.mod.Symbols = append(p.mod.Symbols, ir.Symbol{
		Name: name, FQN: fqn, Kind: ir.KindClass,
		File: p.mod.File, Line: line(n), EndLine: endLine(n), Annotations: anns,
		BaseClasses: p.extends(n), IsTest: p.testFile, Exported: exported,
	})

	body := n.ChildByFieldName("body")
	if body == nil {
		return true
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
			if !p.addSymbol(mname, ir.KindMethod, fqn, c, pending, methodExported) {
				p.orphan(pending, c)
			}
			pending = nil
		default:
			p.orphan(pending, c)
			pending = nil
		}
	}
	p.orphan(pending, nil)
	return true
}

// addConst records a non-function const that carries annotations, or whose
// object-literal value has annotated methods (export const store = { get() {} }).
// Each annotated method becomes a method symbol enclosed by the const.
func (p *tsParser) addConst(name string, decl, val *sitter.Node, anns []ir.Annotation, exported bool) bool {
	if name == "" {
		return false
	}
	fqn := p.modulePath + "." + name
	var members []func()
	if val.Type() == "object" {
		var pending []ir.Annotation
		for i := 0; i < int(val.NamedChildCount()); i++ {
			c := val.NamedChild(i)
			switch c.Type() {
			case "comment":
				if a := p.parseJSDoc(c); a != nil {
					pending = append(pending, a...)
				}
				continue
			case "method_definition":
				if len(pending) > 0 {
					mname, mnode, manns := p.fieldText(c, "name"), c, pending
					members = append(members, func() { p.addSymbol(mname, ir.KindMethod, fqn, mnode, manns, exported) })
				}
			case "pair":
				if v := c.ChildByFieldName("value"); v != nil && len(pending) > 0 {
					switch v.Type() {
					case "arrow_function", "function", "function_expression":
						mname, mnode, manns := p.fieldText(c, "key"), c, pending
						members = append(members, func() { p.addSymbol(mname, ir.KindMethod, fqn, mnode, manns, exported) })
					default:
						p.orphan(pending, c)
					}
				}
			default:
				p.orphan(pending, c)
			}
			pending = nil
		}
		p.orphan(pending, nil)
	}
	if len(anns) == 0 && len(members) == 0 {
		return false
	}
	p.addSymbol(name, ir.KindConst, "", decl, anns, exported)
	for _, add := range members {
		add()
	}
	return true
}

// walkBody records annotated functions declared inside a function body
// (an event handler inside a React component, a helper inside a factory) as
// symbols enclosed by that function. Unannotated inner functions stay out of
// the graph; an annotation inside the body that reaches no function is
// reported as orphaned.
func (p *tsParser) walkBody(body *sitter.Node, enclosingFQN string) {
	if body == nil || body.Type() != "statement_block" {
		return
	}
	var pending []ir.Annotation
	for i := 0; i < int(body.NamedChildCount()); i++ {
		n := body.NamedChild(i)
		switch n.Type() {
		case "comment":
			if a := p.parseJSDoc(n); a != nil {
				pending = append(pending, a...)
			}
			continue
		case "function_declaration":
			if len(pending) > 0 {
				name := p.fieldText(n, "name")
				if p.addSymbol(name, ir.KindFunction, enclosingFQN, n, pending, false) {
					p.walkBody(n.ChildByFieldName("body"), enclosingFQN+"."+name)
					pending = nil
				}
			}
		case "lexical_declaration", "variable_declaration":
			if len(pending) > 0 {
				for j := 0; j < int(n.NamedChildCount()); j++ {
					d := n.NamedChild(j)
					val := d.ChildByFieldName("value")
					if d.Type() != "variable_declarator" || val == nil {
						continue
					}
					switch val.Type() {
					case "arrow_function", "function", "function_expression":
						name := p.fieldText(d, "name")
						if p.addSymbol(name, ir.KindFunction, enclosingFQN, d, pending, false) {
							p.walkBody(val.ChildByFieldName("body"), enclosingFQN+"."+name)
							pending = nil
						}
					}
				}
			}
		}
		p.orphan(pending, n)
		pending = nil
	}
	p.orphan(pending, nil)
}

// addSymbol appends one symbol to the module and reports whether it did.
func (p *tsParser) addSymbol(name string, kind ir.SymbolKind, enclosingFQN string, n *sitter.Node, anns []ir.Annotation, exported bool) bool {
	if name == "" {
		return false
	}
	fqn := p.modulePath + "." + name
	if enclosingFQN != "" {
		fqn = enclosingFQN + "." + name
	}
	p.mod.Symbols = append(p.mod.Symbols, ir.Symbol{
		Name: name, FQN: fqn, Kind: kind,
		File: p.mod.File, Line: line(n), EndLine: endLine(n), EnclosingFQN: enclosingFQN,
		Annotations: anns, IsTest: p.testFile || hasVerify(anns), Exported: exported,
	})
	return true
}

// addTestSymbol records a synthetic symbol for a test-framework call. The test
// description becomes the symbol name; the FQN is slugified and de-duplicated.
func (p *tsParser) addTestSymbol(desc string, n *sitter.Node, anns []ir.Annotation) {
	p.addTestSymbolSlug(desc, slugify(desc), n, anns)
}

// addTestSymbolSlug is addTestSymbol with the FQN slug given.
func (p *tsParser) addTestSymbolSlug(desc, slug string, n *sitter.Node, anns []ir.Annotation) {
	if slug == "" {
		slug = "test"
	}
	base := p.modulePath + ".test." + slug
	fqn := base
	if c := p.testCounts[base]; c > 0 {
		fqn = fmt.Sprintf("%s_%d", base, c+1)
	}
	p.testCounts[base]++
	p.mod.Symbols = append(p.mod.Symbols, ir.Symbol{
		Name: desc, FQN: fqn, Kind: ir.KindFunction,
		File: p.mod.File, Line: line(n), EndLine: endLine(n), Annotations: anns,
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
	moduleOnly := false
	if strings.HasPrefix(text, "//") {
		// A one-line comment may carry module tags (// @module-feature x),
		// but never symbol tags: a stray line comment must not re-attribute
		// the declaration below it.
		if !strings.Contains(text, "@module-") {
			return nil
		}
		moduleOnly = true
		text = strings.TrimPrefix(text, "//")
	} else if !strings.HasPrefix(text, "/**") {
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
			if !ok || moduleOnly && !strings.HasPrefix(kind, "module_") {
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
		// A literal is split into string_fragment and escape_sequence nodes
		// ('the customer\'s cart' splits into several children); join them all, decoding simple escapes.
		var b strings.Builder
		for j := 0; j < int(c.NamedChildCount()); j++ {
			part := c.NamedChild(j)
			switch part.Type() {
			case "string_fragment":
				b.WriteString(part.Content(p.src))
			case "escape_sequence":
				if esc := part.Content(p.src); len(esc) == 2 {
					b.WriteString(esc[1:])
				}
			}
		}
		return b.String() // "" for an empty string literal
	}
	return ""
}

// --- helpers ---

func line(n *sitter.Node) int { return int(n.StartPoint().Row) + 1 }

func endLine(n *sitter.Node) int { return int(n.EndPoint().Row) + 1 }

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

// firstArgIsTitle reports whether a call's first argument is a string or
// template literal, the title slot of a test declaration.
func firstArgIsTitle(args *sitter.Node) bool {
	if args == nil || args.NamedChildCount() == 0 {
		return false
	}
	switch args.NamedChild(0).Type() {
	case "string", "template_string":
		return true
	}
	return false
}

type testCallKind int

const (
	notATest testCallKind = iota
	testCase
	testGroup
)

// testCallHelpers are members of a test function that are not test cases:
// hooks, fixtures, configuration and runtime helpers (Playwright's
// test.beforeEach / test.use / test.step / test.describe.configure, ...).
var testCallHelpers = map[string]bool{
	"beforeEach": true, "afterEach": true, "beforeAll": true, "afterAll": true,
	"use": true, "step": true, "extend": true, "info": true, "configure": true,
	"setTimeout": true, "slow": true, "expect": true, "each": true,
}

// classifyTestCall decides whether a call whose root callee is a test
// function declares a test case, a group of tests, or neither. Groups are
// describe()/suite()/context() and any chain through .describe, so
// Playwright's test.describe(...) and test.describe.serial(...) are walked
// for their nested tests instead of being read as one test.
func classifyTestCall(fn *sitter.Node, src []byte) testCallKind {
	if denoTest(fn, src) {
		return testCase
	}
	chain := memberChain(fn, src)
	if len(chain) == 0 {
		return notATest
	}
	for _, part := range chain[1:] {
		if testCallHelpers[part] {
			return notATest
		}
	}
	switch chain[0] {
	case "describe", "suite", "context":
		return testGroup
	}
	for _, part := range chain[1:] {
		if part == "describe" {
			return testGroup
		}
	}
	return testCase
}

// memberChain flattens a callee into its identifier path:
// test.describe.serial -> [test describe serial]. It returns nil for callees
// that are not plain identifier/member chains (calls, subscripts, ...).
func memberChain(n *sitter.Node, src []byte) []string {
	switch n.Type() {
	case "identifier":
		return []string{n.Content(src)}
	case "member_expression":
		obj, prop := n.ChildByFieldName("object"), n.ChildByFieldName("property")
		if obj == nil || prop == nil {
			return nil
		}
		head := memberChain(obj, src)
		if head == nil {
			return nil
		}
		return append(head, prop.Content(src))
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
		obj := fn.ChildByFieldName("object")
		if obj == nil {
			return ""
		}
		// Deno's runner: Deno.test(...) and Deno.test.ignore/only(...) are tests.
		if denoTest(fn, src) || denoTest(obj, src) {
			return "test"
		}
		if chain := memberChain(fn, src); len(chain) > 0 {
			return chain[0]
		}
	}
	return ""
}

// denoTest reports whether a member expression is exactly `Deno.test`.
func denoTest(n *sitter.Node, src []byte) bool {
	if n.Type() != "member_expression" {
		return false
	}
	obj, prop := n.ChildByFieldName("object"), n.ChildByFieldName("property")
	return obj != nil && prop != nil && obj.Type() == "identifier" &&
		obj.Content(src) == "Deno" && prop.Content(src) == "test"
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
	// Deno's convention: x_test.ts (also x.test.ts, handled below).
	if strings.HasSuffix(strings.TrimSuffix(base, filepath.Ext(base)), "_test") {
		return true
	}
	for _, suf := range []string{".test.ts", ".test.tsx", ".spec.ts", ".spec.tsx",
		".test.js", ".test.jsx", ".spec.js", ".spec.jsx",
		".test.mts", ".test.cts", ".spec.mts", ".spec.cts",
		".test.mjs", ".test.cjs", ".spec.mjs", ".spec.cjs"} {
		if strings.HasSuffix(base, suf) {
			return true
		}
	}
	return false
}
