package typescript

// @module-feature adapters.typescript

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tsgrammar "github.com/tree-sitter/tree-sitter-typescript/bindings/go"

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

	tree, err := astutil.Parse(ctx, grammarFor(path), source)
	if err != nil {
		return mod, err
	}
	defer tree.Close()

	p := &tsParser{
		src:           source,
		mod:           &mod,
		modulePath:    astutil.ModulePath(path),
		testFile:      isTestFile(path),
		testCounts:    map[string]int{},
		boundRequires: map[uint32]bool{},
		wrappers:      a.wrappers,
	}
	p.walkStatements(tree.RootNode())
	mod.HasCode = !p.testFile && hasTopLevelCode(tree.RootNode())
	p.collectRefs(tree.RootNode())
	return mod, nil
}

// typeOnlyNodes are top-level statements that run nothing: imports, comments
// and type-level declarations.
var typeOnlyNodes = map[string]bool{
	"comment": true, "import_statement": true, "empty_statement": true, "hash_bang_line": true,
	"type_alias_declaration": true, "interface_declaration": true, "ambient_declaration": true,
}

// hasTopLevelCode reports whether a file has top-level runtime code: anything
// but imports, comments, type declarations, directives ('use client') and
// re-exports (export { x } from, export * from). An entry file whose only
// statement is a render() call, or a module of plain value consts, has code
// even though it declares no function or class.
// @enforces adapters.typescript:INV-5
func hasTopLevelCode(root *sitter.Node) bool {
	for i := 0; i < int(root.NamedChildCount()); i++ {
		n := root.NamedChild(uint(i))
		switch {
		case typeOnlyNodes[n.Kind()]:
		case n.Kind() == "expression_statement" && n.NamedChildCount() == 1 && n.NamedChild(0).Kind() == "string":
			// a directive
		case n.Kind() == "export_statement":
			if d := n.ChildByFieldName("declaration"); d != nil {
				if !typeOnlyNodes[d.Kind()] {
					return true
				}
			} else if n.ChildByFieldName("value") != nil {
				return true // export default <expression>
			}
		default:
			return true
		}
	}
	return false
}

// The grammars are static C data and need no Close.
var (
	tsLanguage  = sitter.NewLanguage(tsgrammar.LanguageTypescript())
	tsxLanguage = sitter.NewLanguage(tsgrammar.LanguageTSX())
)

// grammarFor picks the tree-sitter grammar for a path. Only .ts/.mts/.cts use
// the plain TypeScript grammar, where <T>expr is a type assertion; every other
// extension may contain JSX, which that grammar cannot parse (a React
// component in a .tsx file would come out as an error tree with no symbols).
// The tsx grammar is a superset for JavaScript, so .js/.mjs/.cjs use it too.
func grammarFor(path string) *sitter.Language {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts", ".mts", ".cts":
		return tsLanguage
	}
	return tsxLanguage
}

type tsParser struct {
	src        []byte
	mod        *ir.Module
	modulePath string
	testFile   bool
	testCounts map[string]int // base FQN -> count, for synthetic test symbols
	// owners is the span of each symbol, for the reference pass
	// (typescript_refs.go).
	owners astutil.Owners
	// boundRequires are the require() calls a top-level binding
	// (const x = require('./x')) records as an import.
	boundRequires map[uint32]bool
	// wrappers are the declared queue and event wrappers.
	wrappers []ir.ChannelWrapper
}

// track records the span of the symbol just appended.
func (p *tsParser) track(n *sitter.Node) {
	p.owners.Add(uint32(n.StartByte()), uint32(n.EndByte()), len(p.mod.Symbols)-1)
}

// walkStatements processes a statement list (the file root or a block),
// threading pending JSDoc annotations onto the next declaration or test call.
func (p *tsParser) walkStatements(parent *sitter.Node) {
	var pending []ir.Annotation
	for i := 0; i < int(parent.NamedChildCount()); i++ {
		n := parent.NamedChild(uint(i))
		switch n.Kind() {
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
		case "for_statement", "for_in_statement":
			p.orphan(pending, n)
			pending = nil
			p.walkLoop(n)
		default:
			p.orphan(pending, n)
			pending = nil
		}
	}
	p.orphan(pending, nil)
}

// walkLoop reads the test calls in a for, for...of or for...in loop body of
// a test file, the usual way to run one test per language, viewport or
// fixture. Each call site is one test, named by its title template. Only test
// calls (and the groups and loops nested in them) are read here: a
// declaration or route in a loop body is not a module-level symbol. Loops in
// other files are not read.
// @enforces adapters.typescript:INV-6
func (p *tsParser) walkLoop(loop *sitter.Node) {
	if p.testFile {
		p.walkLoopBody(loop.ChildByFieldName("body"))
	}
}

// walkLoopBody walks a loop body or a forEach callback body: a statement
// block, or the single statement of a brace-less loop.
func (p *tsParser) walkLoopBody(body *sitter.Node) {
	if body == nil {
		return
	}
	stmts := []*sitter.Node{body}
	if body.Kind() == "statement_block" {
		stmts = stmts[:0]
		for i := 0; i < int(body.NamedChildCount()); i++ {
			stmts = append(stmts, body.NamedChild(uint(i)))
		}
	}
	var pending []ir.Annotation
	for _, n := range stmts {
		switch n.Kind() {
		case "comment":
			if anns := p.parseJSDoc(n); anns != nil {
				pending = append(pending, anns...)
			}
			continue
		case "expression_statement":
			if call := innerCall(n); call == nil || !p.handleTestCall(call, pending) {
				p.orphan(pending, n)
			}
		case "for_statement", "for_in_statement":
			p.orphan(pending, n)
			p.walkLoop(n)
		default:
			p.orphan(pending, n)
		}
		pending = nil
	}
	p.orphan(pending, nil)
}

// handleForEach reads the test calls in a forEach callback
// (CASES.forEach((c) => it(`rounds ${c.in}`, ...))). A concise arrow whose
// body is the test call takes the annotations above the statement; a block
// body is walked like a loop body. As with loops, only a test file's
// forEach callbacks are read.
// @enforces adapters.typescript:INV-6
func (p *tsParser) handleForEach(call *sitter.Node, anns []ir.Annotation) bool {
	args := call.ChildByFieldName("arguments")
	if args == nil || !p.testFile {
		return false
	}
	for i := 0; i < int(args.NamedChildCount()); i++ {
		cb := args.NamedChild(uint(i))
		switch cb.Kind() {
		case "arrow_function", "function", "function_expression":
		default:
			continue
		}
		body := cb.ChildByFieldName("body")
		if body == nil {
			return false
		}
		if body.Kind() == "call_expression" {
			return p.handleTestCall(body, anns)
		}
		p.walkLoopBody(body)
		return false
	}
	return false
}

// orphan reports annotations that attach to no symbol. They used to be
// dropped silently, so an @feature or @verifies above a construct the adapter
// does not model (an object literal, a bare statement, or a file the grammar
// could not parse) looked declared but never reached the graph. at is the
// node that followed the annotations; nil means the end of the block.
// @enforces adapters.typescript:INV-1
func (p *tsParser) orphan(anns []ir.Annotation, at *sitter.Node) {
	if len(anns) == 0 {
		return
	}
	ln, what := 0, "the end of the block"
	if at != nil {
		ln, what = line(at), "a "+strings.ReplaceAll(at.Kind(), "_", " ")
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
	switch n.Kind() {
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
			d := n.NamedChild(uint(i))
			if d.Kind() != "variable_declarator" {
				continue
			}
			val := d.ChildByFieldName("value")
			if val == nil {
				continue
			}
			name := p.fieldText(d, "name")
			switch val.Kind() {
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
// @enforces adapters.typescript:INV-3
func (p *tsParser) handleTestCall(call *sitter.Node, anns []ir.Annotation) bool {
	fn := call.ChildByFieldName("function")
	if fn == nil {
		return false
	}
	if fn.Kind() == "call_expression" {
		return p.handleEachCall(call, fn, anns)
	}
	if isForEach(fn, p.src) {
		return p.handleForEach(call, anns)
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
	if kind == testCase && fn.Kind() == "member_expression" && !denoTest(fn, p.src) &&
		!firstArgIsTitle(args) && len(anns) == 0 {
		return false
	}

	if kind == testGroup {
		// A grouping call is structure, not a test — record it only when it
		// carries annotations. Always recurse for nested cases.
		if len(anns) > 0 {
			p.addTestTitle(p.testTitle(args, callee), call, anns)
		}
		if body := callbackBody(args); body != nil {
			p.walkStatements(body)
		}
		return len(anns) > 0
	}
	if hasInlineCallback(args) || len(anns) > 0 {
		p.addTestTitle(p.testTitle(args, callee), call, anns)
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
	title := eachTitle(p.titleArg(args))
	if group {
		if len(anns) > 0 {
			p.addTestTitle(title, call, anns)
		}
		if body := callbackBody(args); body != nil {
			p.walkStatements(body)
		}
		return len(anns) > 0
	}
	if !hasInlineCallback(args) && len(anns) == 0 {
		return false
	}
	p.addTestTitle(title, call, anns)
	return true
}

// eachPlaceholder matches the per-row placeholders of jest/vitest .each
// titles: printf verbs (%s %d %i %f %j %o %O %p %#) and $name / ${expr}.
var eachPlaceholder = regexp.MustCompile(`%[sdifjoOp#]|\$\{[^}]*\}|\$[A-Za-z_][A-Za-z0-9_.]*`)

// titlePart is one piece of a test title as written: literal text, or a
// placeholder the runner fills in per row or iteration ("${view.name}",
// "%s", "$a"), kept as its source text.
type titlePart struct {
	text string
	hole bool
}

// testTitleParts is a test title split into literal text and placeholders.
type testTitleParts []titlePart

// text is the title as written, placeholders included.
func (t testTitleParts) text() string {
	var b strings.Builder
	for _, part := range t {
		b.WriteString(part.text)
	}
	return b.String()
}

// parameterized reports whether the title has a placeholder.
func (t testTitleParts) parameterized() bool {
	for _, part := range t {
		if part.hole {
			return true
		}
	}
	return false
}

// slug is the title's FQN slug. A placeholder becomes a "_" token (which
// slugify never emits), so the result join can match every expansion:
// "fails when the %s read fails" -> "fails-when-the-_-read-fails".
func (t testTitleParts) slug() string {
	if !t.parameterized() {
		return slugify(t.text())
	}
	var tokens []string
	for _, part := range t {
		switch {
		case part.hole:
			tokens = append(tokens, "_")
		case hasAlnum(part.text):
			tokens = append(tokens, slugify(part.text))
		}
	}
	return strings.Join(tokens, "-")
}

// eachTitle marks the per-row placeholders of a .each title (printf verbs,
// $name, ${expr}) in its literal parts.
func eachTitle(t testTitleParts) testTitleParts {
	var out testTitleParts
	for _, part := range t {
		if part.hole {
			out = append(out, part)
			continue
		}
		rest, last := part.text, 0
		for _, loc := range eachPlaceholder.FindAllStringIndex(rest, -1) {
			if loc[0] > last {
				out = append(out, titlePart{text: rest[last:loc[0]]})
			}
			out = append(out, titlePart{text: rest[loc[0]:loc[1]], hole: true})
			last = loc[1]
		}
		if last < len(rest) {
			out = append(out, titlePart{text: rest[last:]})
		}
	}
	return out
}

// templateSlug slugs a table-test title, marking each placeholder with "_",
// so the result join can match every row.
// @enforces adapters.typescript:INV-4
func templateSlug(title string) string {
	return eachTitle(testTitleParts{{text: title}}).slug()
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

// testTitle returns the title of a test call: its first string or template
// literal argument, falling back to the callee name.
func (p *tsParser) testTitle(args *sitter.Node, callee string) testTitleParts {
	if t := p.titleArg(args); t.text() != "" {
		return t
	}
	return testTitleParts{{text: callee}}
}

// titleArg returns the first string or template literal argument of a call,
// split into literal text and placeholders. A template literal's
// substitutions are placeholders, kept as written
// (`fits on ${view.name}`); escapes in the literal text are decoded.
// @enforces adapters.typescript:INV-6
func (p *tsParser) titleArg(args *sitter.Node) testTitleParts {
	if args == nil {
		return nil
	}
	for i := 0; i < int(args.NamedChildCount()); i++ {
		c := args.NamedChild(uint(i))
		switch c.Kind() {
		case "string":
			return testTitleParts{{text: p.literalText(c)}}
		case "template_string":
			return p.templateParts(c)
		}
	}
	return nil
}

// templateParts splits a template literal into its text and substitutions.
func (p *tsParser) templateParts(n *sitter.Node) testTitleParts {
	var out testTitleParts
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			out = append(out, titlePart{text: lit.String()})
			lit.Reset()
		}
	}
	// The text between children is literal; walk by byte offset so text the
	// grammar leaves unnamed is kept too.
	start, end := n.StartByte()+1, n.EndByte()-1 // inside the backticks
	pos := start
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(uint(i))
		if c.StartByte() > pos {
			lit.Write(p.src[pos:c.StartByte()])
		}
		switch c.Kind() {
		case "template_substitution":
			flush()
			out = append(out, titlePart{text: c.Utf8Text(p.src), hole: true})
		case "escape_sequence":
			if esc := c.Utf8Text(p.src); len(esc) == 2 {
				lit.WriteString(esc[1:])
			} else {
				lit.WriteString(esc)
			}
		default:
			lit.WriteString(c.Utf8Text(p.src))
		}
		pos = c.EndByte()
	}
	if end > pos {
		lit.Write(p.src[pos:end])
	}
	flush()
	return out
}

// detectRoute records an HTTP surface for a framework route registration such
// as app.get("/path", handler) or router.post("/path", handler).
func (p *tsParser) detectRoute(call *sitter.Node) {
	fn := call.ChildByFieldName("function")
	if fn == nil || fn.Kind() != "member_expression" {
		return
	}
	prop := fn.ChildByFieldName("property")
	if prop == nil {
		return
	}
	method := strings.ToLower(prop.Utf8Text(p.src))
	if !httpRouteMethods[method] {
		return
	}
	if obj := fn.ChildByFieldName("object"); obj != nil && obj.Kind() == "identifier" && obj.Utf8Text(p.src) == "axios" {
		return // axios.get('/path') sends a request; it registers no route
	}
	args := call.ChildByFieldName("arguments")
	path := p.firstStringArg(args)
	if !strings.HasPrefix(path, "/") {
		return // not a route path — avoid false positives like cache.get("k")
	}
	p.mod.Surfaces = append(p.mod.Surfaces, ir.Surface{
		Type: "http", Method: strings.ToUpper(method), Path: path,
		Symbol: p.routeHandler(args),
		Line:   line(call), Detected: true,
	})
}

// routeHandler names the symbol a route registration hands its request to
// when the last argument is a plain identifier (app.get("/x", listX)): the
// module's own symbol of that name. Whether the module has one is for the
// reader of the graph to check; an inline function or a member expression
// names none, and "" is returned.
func (p *tsParser) routeHandler(args *sitter.Node) string {
	if args == nil || args.NamedChildCount() == 0 {
		return ""
	}
	last := args.NamedChild(args.NamedChildCount() - 1)
	if last.Kind() != "identifier" {
		return ""
	}
	return p.modulePath + "." + last.Utf8Text(p.src)
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
	p.track(n)

	body := n.ChildByFieldName("body")
	if body == nil {
		return true
	}
	var pending []ir.Annotation
	for i := 0; i < int(body.NamedChildCount()); i++ {
		c := body.NamedChild(uint(i))
		switch c.Kind() {
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
	if val.Kind() == "object" {
		var pending []ir.Annotation
		for i := 0; i < int(val.NamedChildCount()); i++ {
			c := val.NamedChild(uint(i))
			switch c.Kind() {
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
					switch v.Kind() {
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
	if body == nil || body.Kind() != "statement_block" {
		return
	}
	var pending []ir.Annotation
	for i := 0; i < int(body.NamedChildCount()); i++ {
		n := body.NamedChild(uint(i))
		switch n.Kind() {
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
					d := n.NamedChild(uint(j))
					val := d.ChildByFieldName("value")
					if d.Kind() != "variable_declarator" || val == nil {
						continue
					}
					switch val.Kind() {
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
	p.track(n)
	return true
}

// addTestTitle records a synthetic symbol for a test-framework call. The
// title as written becomes the symbol name; the FQN is its slug,
// de-duplicated. A title with placeholders is a parameterized test.
func (p *tsParser) addTestTitle(t testTitleParts, n *sitter.Node, anns []ir.Annotation) {
	p.addTestSymbolSlug(t.text(), t.slug(), t.parameterized(), n, anns)
}

// addTestSymbolSlug records a test symbol with the FQN slug given. A second
// call site with the same slug in the file gets a _2, _3, ... suffix.
func (p *tsParser) addTestSymbolSlug(desc, slug string, parameterized bool, n *sitter.Node, anns []ir.Annotation) {
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
		IsTest: true, Exported: false, Parameterized: parameterized,
	})
	p.track(n)
}

// extends returns the base classes from a class heritage clause.
func (p *tsParser) extends(n *sitter.Node) []string {
	var bases []string
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(uint(i))
		if c.Kind() != "class_heritage" {
			continue
		}
		for j := 0; j < int(c.NamedChildCount()); j++ {
			ec := c.NamedChild(uint(j))
			if ec.Kind() == "extends_clause" {
				for k := 0; k < int(ec.NamedChildCount()); k++ {
					id := ec.NamedChild(uint(k))
					if t := id.Kind(); t == "identifier" || t == "member_expression" {
						bases = append(bases, p.modulePath+"."+id.Utf8Text(p.src))
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
// @enforces adapters.typescript:INV-2
func (p *tsParser) parseJSDoc(n *sitter.Node) []ir.Annotation {
	text := n.Utf8Text(p.src)
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
		return c.Utf8Text(p.src)
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
		if c := args.NamedChild(uint(i)); c.Kind() == "string" {
			return p.literalText(c)
		}
	}
	return ""
}

// literalText returns the unquoted text of a string literal. A literal is
// split into string_fragment and escape_sequence nodes ('the customer\'s
// cart' splits into several children); join them all, decoding simple
// escapes. It is "" for an empty string literal.
func (p *tsParser) literalText(c *sitter.Node) string {
	var b strings.Builder
	for j := 0; j < int(c.NamedChildCount()); j++ {
		part := c.NamedChild(uint(j))
		switch part.Kind() {
		case "string_fragment":
			b.WriteString(part.Utf8Text(p.src))
		case "escape_sequence":
			if esc := part.Utf8Text(p.src); len(esc) == 2 {
				b.WriteString(esc[1:])
			}
		}
	}
	return b.String()
}

// --- helpers ---

func line(n *sitter.Node) int { return int(n.StartPosition().Row) + 1 }

func endLine(n *sitter.Node) int { return int(n.EndPosition().Row) + 1 }

// exportedDecl unwraps the declaration inside an export_statement.
func exportedDecl(n *sitter.Node) *sitter.Node {
	if d := n.ChildByFieldName("declaration"); d != nil {
		return d
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(uint(i))
		switch c.Kind() {
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
		if c := stmt.NamedChild(uint(i)); c.Kind() == "call_expression" {
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
	switch args.NamedChild(0).Kind() {
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
	switch n.Kind() {
	case "identifier":
		return []string{n.Utf8Text(src)}
	case "member_expression":
		obj, prop := n.ChildByFieldName("object"), n.ChildByFieldName("property")
		if obj == nil || prop == nil {
			return nil
		}
		head := memberChain(obj, src)
		if head == nil {
			return nil
		}
		return append(head, prop.Utf8Text(src))
	}
	return nil
}

// calleeName resolves the callee of a call expression to a bare name,
// unwrapping member access so describe.only / it.skip still resolve.
func calleeName(fn *sitter.Node, src []byte) string {
	switch fn.Kind() {
	case "identifier":
		return fn.Utf8Text(src)
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

// isForEach reports whether a callee is a .forEach member call
// (CASES.forEach, [1, 2].forEach, Object.entries(x).forEach).
func isForEach(fn *sitter.Node, src []byte) bool {
	if fn.Kind() != "member_expression" {
		return false
	}
	prop := fn.ChildByFieldName("property")
	return prop != nil && prop.Utf8Text(src) == "forEach"
}

// denoTest reports whether a member expression is exactly `Deno.test`.
func denoTest(n *sitter.Node, src []byte) bool {
	if n.Kind() != "member_expression" {
		return false
	}
	obj, prop := n.ChildByFieldName("object"), n.ChildByFieldName("property")
	return obj != nil && prop != nil && obj.Kind() == "identifier" &&
		obj.Utf8Text(src) == "Deno" && prop.Utf8Text(src) == "test"
}

// hasInlineCallback reports whether an arguments node contains an inline
// function or arrow expression (as opposed to a bare identifier reference).
func hasInlineCallback(args *sitter.Node) bool {
	if args == nil {
		return false
	}
	for i := 0; i < int(args.NamedChildCount()); i++ {
		switch args.NamedChild(uint(i)).Kind() {
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
		c := args.NamedChild(uint(i))
		switch c.Kind() {
		case "arrow_function", "function", "function_expression", "generator_function":
			if body := c.ChildByFieldName("body"); body != nil && body.Kind() == "statement_block" {
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
		c := method.NamedChild(uint(i))
		if c.Kind() == "accessibility_modifier" {
			switch c.Utf8Text(src) {
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
