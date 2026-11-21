package postgres

// @module-feature adapters.postgres

import (
	"regexp"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// refOpts are the declared facts a statement's references are read with.
type refOpts struct {
	// settings are the declared setting:<name> origins, lower-cased.
	settings map[string]bool
	// secrets maps a Vault secret name to the route path its URL points at
	// (SetHTTPSecrets).
	secrets map[string]string
}

// SetHTTPOrigins declares the origins of workspace.yaml http.origins. The
// setting:<name> entries name a Postgres setting that holds an own origin: a
// URL that is exactly current_setting('<name>') and a literal path is
// path-only (see originPath). Call it before parsing.
func (a *Adapter) SetHTTPOrigins(origins []string) {
	a.opts.settings = nil
	for _, n := range ir.OriginNames(origins, "setting") {
		if a.opts.settings == nil {
			a.opts.settings = map[string]bool{}
		}
		a.opts.settings[strings.ToLower(n)] = true
	}
}

var (
	reNetHTTP  = regexp.MustCompile(`(?is)\bnet\s*\.\s*(http_post|http_get|http_request)\s*\(`)
	reNamedArg = regexp.MustCompile(`(?is)^([a-z_][a-z0-9_]*)\s*(?::=|=>)\s*(.*)$`)
	reSetting  = regexp.MustCompile(`(?is)^\(*\s*current_setting\s*\(\s*'([^']+)'\s*(?:,\s*(?:true|false)\s*)?\)\s*(?:::\s*[a-z_]+)?\s*\)*\s*(?:::\s*[a-z_]+)?$`)
)

// httpRefs reads the requests of pg_net a statement's text sends, as refs of
// kind http keyed by method and path (ir.HTTPKey):
//
//   - net.http_post(url := ...) is a POST and net.http_get(url := ...) a GET;
//     net.http_request takes its method from a literal method argument;
//   - the URL is a literal, or a concatenation that is one declared
//     current_setting origin followed by literals (see originPath), or a
//     variable set once from a declared Vault secret, alone or followed by
//     literals (see vaultURL).
//
// Any other URL, and a method that is not a literal, gives a ref with no name,
// which the core counts as dynamic. Comments, and string literals unless
// withStrings, are skipped, so a call quoted in a message makes nothing.
// @enforces reachability:INV-11
func (o refOpts) httpRefs(text string, line int, withStrings bool) []ir.Ref {
	code := maskSQL(text, withStrings)
	reads := vaultReads(text)
	var out []ir.Ref
	for _, m := range reNetHTTP.FindAllStringSubmatchIndex(code, -1) {
		fn := strings.ToLower(code[m[2]:m[3]])
		args := callArgs(text[m[1]-1:])
		named := map[string]string{}
		var positional []string
		for _, a := range args {
			if nm := reNamedArg.FindStringSubmatch(a); nm != nil {
				named[strings.ToLower(nm[1])] = nm[2]
			} else {
				positional = append(positional, a)
			}
		}
		pos := func(i int) string {
			if i < len(positional) {
				return positional[i]
			}
			return ""
		}
		var urlExpr, methodExpr string
		switch fn {
		case "http_post", "http_get":
			urlExpr = named["url"]
			if urlExpr == "" {
				urlExpr = pos(0)
			}
			methodExpr = map[string]string{"http_post": "'POST'", "http_get": "'GET'"}[fn]
		default:
			urlExpr, methodExpr = named["url"], named["method"]
			if urlExpr == "" {
				urlExpr = pos(1)
			}
			if methodExpr == "" {
				methodExpr = pos(0)
			}
		}
		ref := ir.Ref{Kind: ir.RefHTTP, Line: line + strings.Count(text[:m[0]], "\n")}
		method, mok := stringLiteral(methodExpr)
		if path, ok := o.originPath(urlExpr); ok && mok {
			ref.Name = ir.HTTPKey(method, path)
			ref.Qualifier = ir.HTTPOrigin(path)
		} else if path, secret, ok := o.vaultURL(urlExpr, m[0], reads); ok {
			if path != "" && mok {
				ref.Name = ir.HTTPKey(method, path)
			} else if path == "" {
				ref.Secret = secret // read whole from Vault and not declared: the finding names it
			}
		}
		out = append(out, ref)
	}
	return out
}

// originPath reads a URL expression into the URL or path it stands for: a
// string literal, literals joined by ||, or one declared current_setting
// origin followed by literals, whose URL is those literals joined. Any other
// part makes it computed.
// @enforces reachability:INV-11
func (o refOpts) originPath(expr string) (string, bool) {
	parts := splitConcat(expr)
	if len(parts) == 0 {
		return "", false
	}
	var b strings.Builder
	for i, p := range parts {
		if lit, ok := stringLiteral(p); ok {
			b.WriteString(lit)
			continue
		}
		if i == 0 && len(parts) > 1 && o.isSettingOrigin(p) {
			continue
		}
		return "", false
	}
	return b.String(), true
}

// isSettingOrigin reports whether p is current_setting('<name>'[, bool]) of a
// declared setting origin, under parentheses or a cast.
func (o refOpts) isSettingOrigin(p string) bool {
	m := reSetting.FindStringSubmatch(strings.TrimSpace(p))
	return m != nil && o.settings[strings.ToLower(m[1])]
}

// splitConcat splits an expression at its top-level || operators, honouring
// quotes, dollar bodies and parentheses.
func splitConcat(s string) []string {
	var parts []string
	depth, last, ln := 0, 0, 0
	for i := 0; i < len(s); {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case '|':
			if depth == 0 && i+1 < len(s) && s[i+1] == '|' {
				parts = append(parts, strings.TrimSpace(s[last:i]))
				last = i + 2
				i += 2
				continue
			}
		case '\'', '"', '$', 'E', 'e':
			i = skipToken(s, i, &ln)
			continue
		}
		i++
	}
	if t := strings.TrimSpace(s[last:]); t != "" || len(parts) > 0 {
		parts = append(parts, t)
	}
	return parts
}
