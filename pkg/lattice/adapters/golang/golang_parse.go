package golang

// @module-feature adapters.go

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	sitter "github.com/smacker/go-tree-sitter"
	gogrammar "github.com/smacker/go-tree-sitter/golang"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/astutil"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// tagKinds maps comment tag names to Lattice annotation kinds.
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

// parse turns Go source into an IR module.
func (a *Adapter) parse(ctx context.Context, file string, source []byte) (ir.Module, error) {
	mod := ir.Module{File: file, Language: a.Name()}

	parser := sitter.NewParser()
	parser.SetLanguage(gogrammar.GetLanguage())
	tree, err := parser.ParseCtx(ctx, nil, source)
	if err != nil {
		return mod, err
	}
	defer tree.Close()
	root := tree.RootNode()

	p := &goParser{
		src:      source,
		mod:      &mod,
		pkgPath:  packagePath(file, packageName(root, source)),
		testFile: isTestFile(file),
		subtests: map[string]int{},
	}
	p.walkList(root, p.handleTop)
	if p.testFile {
		mod.HasCode = false
	}
	return mod, nil
}

type goParser struct {
	src      []byte
	mod      *ir.Module
	pkgPath  string // FQN prefix: the package directory, dotted
	testFile bool
	subtests map[string]int // "<parent FQN>/<subtest>" -> uses, for go test's #NN suffixes
}

// packagePath is the FQN prefix of a file's symbols: its directory, dotted
// (pkg/lattice/results -> pkg.lattice.results). A file at the code root has
// no directory, so the package name stands in for it.
// @enforces adapters.go:INV-2
func packagePath(file, pkgName string) string {
	dir := path.Dir(strings.ReplaceAll(file, "\\", "/"))
	dir = strings.Trim(strings.TrimPrefix(dir, "./"), "/")
	if dir == "" || dir == "." {
		if pkgName == "" {
			return "main"
		}
		return pkgName
	}
	return strings.ReplaceAll(dir, "/", ".")
}

// packageName returns the file's package clause name, without the _test
// suffix of an external test package.
func packageName(root *sitter.Node, src []byte) string {
	for i := 0; i < int(root.NamedChildCount()); i++ {
		c := root.NamedChild(i)
		if c.Type() != "package_clause" {
			continue
		}
		for j := 0; j < int(c.NamedChildCount()); j++ {
			if id := c.NamedChild(j); id.Type() == "package_identifier" {
				return strings.TrimSuffix(id.Content(src), "_test")
			}
		}
	}
	return ""
}

// Why a tagged comment attached to nothing; see orphan.
const (
	orphanNoSymbol = iota
	orphanBlankLine
	orphanTrailing
	orphanInBody
	orphanInit
)

// walkList processes a node's children (the file, a grouped declaration, a
// block), handing each non-comment child the symbol annotations of the doc
// comment directly above it. A doc comment is the run of comment lines
// ending on the line before the child, as in go/doc: a blank line ends it,
// and a comment that trails code on the same line is no doc comment.
// Module tags are routed to the module wherever they appear. handle reports
// whether it recorded a symbol carrying the annotations; annotations it does
// not take are reported as orphaned.
// @enforces adapters.go:INV-1
func (p *goParser) walkList(parent *sitter.Node, handle func(n *sitter.Node, anns []ir.Annotation) bool) {
	if parent == nil {
		return
	}
	var pending []ir.Annotation
	groupEnd, prevEnd := -2, -2 // rows of the last comment and the last code child
	for i := 0; i < int(parent.NamedChildCount()); i++ {
		n := parent.NamedChild(i)
		start := int(n.StartPoint().Row)
		if n.Type() == "comment" {
			anns := p.parseComment(n)
			if start == prevEnd {
				p.orphan(anns, n, orphanTrailing)
				continue
			}
			if len(pending) > 0 && start > groupEnd+1 {
				p.orphan(pending, n, orphanBlankLine)
				pending = nil
			}
			pending = append(pending, anns...)
			groupEnd = int(n.EndPoint().Row)
			continue
		}
		var anns []ir.Annotation
		if len(pending) > 0 {
			if start == groupEnd+1 {
				anns = pending
			} else {
				p.orphan(pending, n, orphanBlankLine)
			}
		}
		pending = nil
		if !handle(n, anns) {
			p.orphan(anns, n, orphanNoSymbol)
		}
		prevEnd = int(n.EndPoint().Row)
	}
	p.orphan(pending, nil, orphanNoSymbol)
}

// orphan reports annotations that attach to no symbol, as the TypeScript
// adapter does: an @feature or @verifies the graph never sees must not look
// declared. at is the node that followed (or holds) the annotations; nil
// means the end of the block.
// @enforces adapters.go:INV-1
func (p *goParser) orphan(anns []ir.Annotation, at *sitter.Node, why int) {
	if len(anns) == 0 {
		return
	}
	kinds := make([]string, 0, len(anns))
	for _, a := range anns {
		kinds = append(kinds, "@"+a.Kind)
	}
	tags := strings.Join(kinds, ", ")
	what := "the end of the block"
	ln := anns[0].Line
	if at != nil {
		what = "a " + strings.ReplaceAll(at.Type(), "_", " ")
	}
	var msg string
	switch why {
	case orphanBlankLine:
		msg = fmt.Sprintf("%s attaches to no symbol: a blank line separates it from %s; tags belong in the doc comment directly above the declaration", tags, what)
	case orphanTrailing:
		msg = fmt.Sprintf("%s attaches to no symbol: it trails code on the same line; tags belong in the doc comment directly above the declaration", tags)
	case orphanInit:
		msg = fmt.Sprintf("%s attaches to no symbol: init is not addressable (a package may declare several); tag a named function, or the file with @module-* tags", tags)
	case orphanInBody:
		msg = fmt.Sprintf("%s attaches to no symbol: it sits inside a function body, where only t.Run subtests of a test take tags", tags)
	default:
		msg = fmt.Sprintf("%s attaches to no symbol: it is followed by %s, which declares no function, method, type or test", tags, what)
	}
	p.mod.Diagnostics = append(p.mod.Diagnostics, ir.Diagnostic{
		Line: ln, Code: schema.CodeAnnotationOrphaned, Message: msg,
	})
}

// handleTop records a top-level declaration.
func (p *goParser) handleTop(n *sitter.Node, anns []ir.Annotation) bool {
	switch n.Type() {
	case "function_declaration":
		return p.addFunc(n, anns)
	case "method_declaration":
		return p.addMethod(n, anns)
	case "type_declaration":
		return p.addTypes(n, anns)
	case "const_declaration", "var_declaration":
		return p.addValues(n, anns)
	}
	p.scanOrphans(n)
	return len(anns) == 0
}

// addFunc records a function. init (which may repeat in a package) and the
// blank function declare nothing addressable: they make the file runtime
// code instead.
func (p *goParser) addFunc(n *sitter.Node, anns []ir.Annotation) bool {
	name := p.fieldText(n, "name")
	body := n.ChildByFieldName("body")
	if name == "" || name == "init" || name == "_" {
		if name != "" {
			p.mod.HasCode = true
		}
		p.orphan(anns, n, orphanInit)
		p.scanOrphans(body)
		return true
	}
	idx := p.addSymbol(name, p.pkgPath+"."+name, ir.KindFunction, "", n, anns, isExported(name))
	p.walkFuncBody(body, idx, isGoTestName(name))
	return true
}

// addMethod records a method under its receiver type.
func (p *goParser) addMethod(n *sitter.Node, anns []ir.Annotation) bool {
	name := p.fieldText(n, "name")
	recv := receiverType(n.ChildByFieldName("receiver"), p.src)
	body := n.ChildByFieldName("body")
	if name == "" || recv == "" {
		p.scanOrphans(body)
		return len(anns) == 0
	}
	enclosing := p.pkgPath + "." + recv
	idx := p.addSymbol(name, enclosing+"."+name, ir.KindMethod, enclosing, n, anns,
		isExported(name) && isExported(recv))
	p.walkFuncBody(body, idx, false)
	return true
}

// addTypes records each type in a type declaration (`type T struct{}` or a
// `type ( ... )` group): interfaces as interfaces, every other named type
// or alias as a class. Tags above a group apply to each type in it.
func (p *goParser) addTypes(n *sitter.Node, anns []ir.Annotation) bool {
	added := false
	p.walkList(n, func(c *sitter.Node, inner []ir.Annotation) bool {
		if c.Type() != "type_spec" && c.Type() != "type_alias" {
			p.scanOrphans(c)
			return len(inner) == 0
		}
		name := p.fieldText(c, "name")
		if name == "" {
			return len(inner) == 0
		}
		kind := ir.KindClass
		if t := c.ChildByFieldName("type"); t != nil && t.Type() == "interface_type" {
			kind = ir.KindInterface
		}
		p.scanOrphans(c.ChildByFieldName("type"))
		p.addSymbol(name, p.pkgPath+"."+name, kind, "", c, joinAnns(anns, inner), isExported(name))
		added = true
		return true
	})
	return added || len(anns) == 0
}

// addValues handles a const or var declaration. Like a TypeScript value
// const, a package-level const or var is a symbol only when it is tagged;
// a var with an initializer is runtime code either way.
func (p *goParser) addValues(n *sitter.Node, anns []ir.Annotation) bool {
	added := false
	var visit func(list *sitter.Node, outer []ir.Annotation)
	visit = func(list *sitter.Node, outer []ir.Annotation) {
		p.walkList(list, func(c *sitter.Node, inner []ir.Annotation) bool {
			switch c.Type() {
			case "var_spec_list":
				visit(c, joinAnns(outer, inner))
				return true
			case "const_spec", "var_spec":
			default:
				p.scanOrphans(c)
				return len(inner) == 0
			}
			val := c.ChildByFieldName("value")
			if c.Type() == "var_spec" && val != nil {
				p.mod.HasCode = true
			}
			p.scanOrphans(val)
			all := joinAnns(outer, inner)
			if len(all) == 0 {
				return true
			}
			took := false
			for i := 0; i < int(c.ChildCount()); i++ {
				if c.FieldNameForChild(i) != "name" {
					continue
				}
				name := c.Child(i).Content(p.src)
				if name == "_" {
					continue
				}
				kind := ir.KindConst
				if val != nil && val.NamedChildCount() == 1 && val.NamedChild(0).Type() == "func_literal" {
					kind = ir.KindFunction
				}
				p.addSymbol(name, p.pkgPath+"."+name, kind, "", c, append([]ir.Annotation(nil), all...), isExported(name))
				took, added = true, true
			}
			return took
		})
	}
	visit(n, anns)
	return added || len(anns) == 0
}

// walkFuncBody looks inside a function body for tags. In a test file a
// test's t.Run calls are subtests (see walkTestBody); elsewhere a tag inside
// a body attaches to nothing.
func (p *goParser) walkFuncBody(body *sitter.Node, idx int, goTest bool) {
	if p.testFile {
		p.walkTestBody(body, idx, goTest)
		return
	}
	p.scanOrphans(body)
}

// walkTestBody records t.Run("name", ...) calls in a test body as subtest
// symbols named like go test names them (<test FQN>.<name>, spaces as
// underscores), so a tag above the call verifies that subtest alone.
// named is false when the enclosing test's run name is not known statically
// (a helper function, or a subtest named by a variable as in a table test);
// tags above a t.Run there, or above the for loop of a table test, then
// attach to the nearest enclosing symbol, which passes only when every
// subtest under it passes.
func (p *goParser) walkTestBody(block *sitter.Node, idx int, named bool) {
	if block == nil {
		return
	}
	p.walkList(block, func(n *sitter.Node, anns []ir.Annotation) bool {
		switch n.Type() {
		case "expression_statement":
			if call := subtestCall(n, p.src); call != nil {
				return p.addSubtest(call, anns, idx, named)
			}
		case "for_statement":
			p.mergeInto(idx, anns)
			p.walkTestBody(n.ChildByFieldName("body"), idx, named)
			return true
		case "block":
			p.walkTestBody(n, idx, named)
			return len(anns) == 0
		case "if_statement":
			for cur := n; cur != nil; {
				p.scanOrphans(cur.ChildByFieldName("initializer"))
				p.scanOrphans(cur.ChildByFieldName("condition"))
				p.walkTestBody(cur.ChildByFieldName("consequence"), idx, named)
				alt := cur.ChildByFieldName("alternative")
				if alt != nil && alt.Type() == "if_statement" {
					cur = alt
					continue
				}
				p.walkTestBody(alt, idx, named)
				cur = nil
			}
			return len(anns) == 0
		}
		p.scanOrphans(n)
		return len(anns) == 0
	})
}

// addSubtest records one t.Run call. It always takes the annotations.
// @enforces adapters.go:INV-2
func (p *goParser) addSubtest(call *sitter.Node, anns []ir.Annotation, idx int, named bool) bool {
	args := call.ChildByFieldName("arguments")
	title, literal := stringLiteral(args.NamedChild(0), p.src)
	var body *sitter.Node
	for i := 1; i < int(args.NamedChildCount()); i++ {
		a := args.NamedChild(i)
		if a.Type() == "func_literal" && body == nil {
			body = a.ChildByFieldName("body")
			continue
		}
		p.scanOrphans(a)
	}
	if !literal || !named || idx < 0 {
		p.scanOrphans(args.NamedChild(0))
		p.mergeInto(idx, anns)
		p.walkTestBody(body, idx, false)
		return true
	}
	parent := p.mod.Symbols[idx].FQN
	fqn := parent + "." + p.uniqueSubtest(parent, rewriteSubtest(title))
	sub := p.addSymbol(title, fqn, ir.KindFunction, parent, call, anns, false)
	p.walkTestBody(body, sub, true)
	return true
}

// uniqueSubtest mirrors go test's de-duplication of subtest names: a name
// used twice under one parent gets #01, #02, ...; an empty name is #00.
func (p *goParser) uniqueSubtest(parent, sub string) string {
	name := parent + "/" + sub
	empty := sub == ""
	for {
		next, exists := p.subtests[name]
		if !empty && !exists {
			p.subtests[name] = 1
			return strings.TrimPrefix(name, parent+"/")
		}
		p.subtests[name] = next + 1
		name = fmt.Sprintf("%s#%02d", name, next)
		empty = false
	}
}

// rewriteSubtest mirrors go test's rewrite of a subtest name: spaces become
// underscores and unprintable runes are escaped.
// @enforces adapters.go:INV-2
func rewriteSubtest(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			b.WriteByte('_')
		case !strconv.IsPrint(r):
			q := strconv.QuoteRune(r)
			b.WriteString(q[1 : len(q)-1])
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// mergeInto adds annotations to an existing symbol.
func (p *goParser) mergeInto(idx int, anns []ir.Annotation) {
	if len(anns) == 0 {
		return
	}
	if idx < 0 || idx >= len(p.mod.Symbols) {
		p.orphan(anns, nil, orphanInBody)
		return
	}
	s := &p.mod.Symbols[idx]
	s.Annotations = append(s.Annotations, anns...)
	s.IsTest = s.IsTest || hasVerify(anns)
}

// scanOrphans reports every symbol tag in comments under n, which no
// structural walk reaches (a function body, a composite literal). Module
// tags among them still reach the module.
func (p *goParser) scanOrphans(n *sitter.Node) {
	if n == nil {
		return
	}
	if n.Type() == "comment" {
		p.orphan(p.parseComment(n), n, orphanInBody)
		return
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		p.scanOrphans(n.NamedChild(i))
	}
}

// addSymbol appends one symbol and returns its index.
func (p *goParser) addSymbol(name, fqn string, kind ir.SymbolKind, enclosing string, n *sitter.Node, anns []ir.Annotation, exported bool) int {
	p.mod.Symbols = append(p.mod.Symbols, ir.Symbol{
		Name: name, FQN: fqn, Kind: kind,
		File: p.mod.File, Line: line(n), EndLine: endLine(n), EnclosingFQN: enclosing,
		Annotations: anns, IsTest: p.testFile || hasVerify(anns), Exported: exported,
	})
	return len(p.mod.Symbols) - 1
}

// parseComment turns a comment's tags into annotations. Module tags go
// straight to the module; symbol tags are returned for the declaration the
// comment documents. A tag starts a comment line (`// @feature x`); several
// may share one (`// @feature x @enforces x:INV-1`).
func (p *goParser) parseComment(n *sitter.Node) []ir.Annotation {
	text := n.Content(p.src)
	block := strings.HasPrefix(text, "/*")
	startLine := line(n)
	var symbolAnns []ir.Annotation
	for i, raw := range strings.Split(text, "\n") {
		ln := strings.TrimSpace(raw)
		if block {
			ln = strings.TrimPrefix(ln, "/*")
			ln = strings.TrimSuffix(ln, "*/")
			ln = strings.TrimPrefix(strings.TrimSpace(ln), "*")
		} else {
			ln = strings.TrimPrefix(ln, "//")
		}
		ln = strings.TrimSpace(ln)
		if !strings.HasPrefix(ln, "@") {
			continue
		}
		var lineAnns []ir.Annotation
		prose := false
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
			ann := ir.Annotation{Kind: kind, Line: startLine + i}
			if len(fields) == 2 {
				applyArgs(&ann, strings.TrimSpace(fields[1]))
			}
			if !tagLike(ann) {
				prose = true
				break
			}
			lineAnns = append(lineAnns, ann)
		}
		if prose {
			continue
		}
		for _, ann := range lineAnns {
			if strings.HasPrefix(ann.Kind, "module_") {
				p.mod.ModuleAnnotations = append(p.mod.ModuleAnnotations, ann)
			} else {
				symbolAnns = append(symbolAnns, ann)
			}
		}
	}
	return symbolAnns
}

// surfaceTypes are the interaction types an @surface tag names first.
var surfaceTypes = map[string]bool{
	"http": true, "event_emit": true, "event_consume": true, "webhook_receive": true, "scheduled": true,
}

// tagLike tells a tag from prose that a wrapped doc comment happens to
// start with "@" ("// @error annotations found in source are ..."). A tag's
// arguments are comma-separated single tokens (ids, invariant refs, error
// codes), except @surface, whose space-separated fields start with an
// interaction type, and a "reason:" text. A line that reads as prose is not
// parsed for tags at all.
// @enforces adapters.go:INV-4
func tagLike(ann ir.Annotation) bool {
	if ann.Kind == "surface" {
		if len(ann.Args) == 0 {
			return false
		}
		first, _ := ann.Args[0].(string)
		f := strings.Fields(first)
		return len(f) > 0 && surfaceTypes[f[0]]
	}
	for _, a := range ann.Args {
		if s, _ := a.(string); strings.ContainsAny(s, " \t") {
			return false
		}
	}
	return true
}

// applyArgs splits a tag's argument text into annotation args, as the
// TypeScript adapter does. A "reason:" segment becomes a reason kwarg (used
// by @suppresses).
func applyArgs(ann *ir.Annotation, text string) {
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

func (p *goParser) fieldText(n *sitter.Node, field string) string {
	if n == nil {
		return ""
	}
	if c := n.ChildByFieldName(field); c != nil {
		return c.Content(p.src)
	}
	return ""
}

// --- helpers ---

func line(n *sitter.Node) int { return int(n.StartPoint().Row) + 1 }

func endLine(n *sitter.Node) int { return int(n.EndPoint().Row) + 1 }

// receiverType returns the type name of a method receiver, unwrapping
// pointers, type parameters and parentheses: (s *Set[T]) -> Set.
func receiverType(recv *sitter.Node, src []byte) string {
	if recv == nil {
		return ""
	}
	for i := 0; i < int(recv.NamedChildCount()); i++ {
		param := recv.NamedChild(i)
		if param.Type() != "parameter_declaration" {
			continue
		}
		t := param.ChildByFieldName("type")
		for t != nil {
			switch t.Type() {
			case "type_identifier":
				return t.Content(src)
			case "generic_type":
				t = t.ChildByFieldName("type")
			case "pointer_type", "parenthesized_type":
				if t.NamedChildCount() == 0 {
					return ""
				}
				t = t.NamedChild(0)
			default:
				return ""
			}
		}
	}
	return ""
}

// subtestCall returns the call of a `x.Run(name, fn)` statement (t.Run,
// b.Run), or nil.
func subtestCall(stmt *sitter.Node, src []byte) *sitter.Node {
	if stmt.NamedChildCount() != 1 {
		return nil
	}
	call := stmt.NamedChild(0)
	if call.Type() != "call_expression" {
		return nil
	}
	fn := call.ChildByFieldName("function")
	if fn == nil || fn.Type() != "selector_expression" {
		return nil
	}
	if f := fn.ChildByFieldName("field"); f == nil || f.Type() != "field_identifier" || f.Content(src) != "Run" {
		return nil
	}
	args := call.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() < 2 {
		return nil
	}
	return call
}

// stringLiteral returns the value of a Go string literal node.
func stringLiteral(n *sitter.Node, src []byte) (string, bool) {
	if n == nil {
		return "", false
	}
	switch n.Type() {
	case "interpreted_string_literal", "raw_string_literal":
		s, err := strconv.Unquote(n.Content(src))
		return s, err == nil
	}
	return "", false
}

// joinAnns returns a fresh slice holding a then b.
func joinAnns(a, b []ir.Annotation) []ir.Annotation {
	if len(a)+len(b) == 0 {
		return nil
	}
	out := make([]ir.Annotation, 0, len(a)+len(b))
	return append(append(out, a...), b...)
}

// isExported applies Go's rule: a name is exported when it starts with an
// upper-case letter.
func isExported(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(r)
}

// isGoTestName reports whether a function name is one go test runs and
// reports by name: TestXxx, BenchmarkXxx, FuzzXxx, ExampleXxx, where Xxx
// does not start with a lower-case letter.
func isGoTestName(name string) bool {
	for _, prefix := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		rest := name[len(prefix):]
		if rest == "" {
			return true
		}
		r, _ := utf8.DecodeRuneInString(rest)
		return !unicode.IsLower(r)
	}
	return false
}

func hasVerify(anns []ir.Annotation) bool {
	for _, a := range anns {
		if a.Kind == "verifies" || a.Kind == "verifies_capability" {
			return true
		}
	}
	return false
}

// isTestFile reports whether a Go path is test code: a _test.go file, or a
// file under a test directory such as testdata/.
func isTestFile(file string) bool {
	return strings.HasSuffix(strings.ToLower(file), "_test.go") || astutil.IsTestPath(file)
}
