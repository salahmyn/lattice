package graph

import (
	"sort"
	"strconv"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// buildErrors fuses the error contracts feature manifests declare with the
// @error annotations found in source into one error inventory. Each entry
// records whether it is declared, whether code raises it, and where.
func buildErrors(manifests []schema.Manifest, modules []ir.Module, res *resolver) []schema.GraphError {
	byCode := map[string]*schema.GraphError{}

	get := func(code string) *schema.GraphError {
		ge := byCode[code]
		if ge == nil {
			ge = &schema.GraphError{Code: code}
			byCode[code] = ge
		}
		return ge
	}

	// Declared error contracts from feature manifests.
	for _, m := range manifests {
		for _, e := range m.Errors {
			ge := get(e.Code)
			ge.Declared = true
			if ge.Feature == "" {
				ge.Feature = m.ID
			}
			if ge.Status == 0 {
				ge.Status = e.Status
			}
			if ge.Description == "" {
				ge.Description = e.Description
			}
		}
	}

	// @error annotations found in source.
	for mi := range modules {
		mod := &modules[mi]
		for si := range mod.Symbols {
			sym := &mod.Symbols[si]
			for _, a := range sym.Annotations {
				if a.Kind != "error" {
					continue
				}
				code, status, ok := errorFromAnnotation(a)
				if !ok {
					continue
				}
				ge := get(code)
				ge.Implemented = true
				if ge.Status == 0 {
					ge.Status = status
				}
				if ge.Feature == "" {
					ge.Feature = res.resolveSymbol(sym, mod).feature
				}
				ge.RaisedBy = append(ge.RaisedBy, schema.SurfaceImpl{
					File: sym.File, Line: a.Line, Symbol: sym.FQN,
				})
			}
		}
	}

	out := make([]schema.GraphError, 0, len(byCode))
	for _, ge := range byCode {
		sort.Slice(ge.RaisedBy, func(i, j int) bool {
			a, b := ge.RaisedBy[i], ge.RaisedBy[j]
			if a.File != b.File {
				return a.File < b.File
			}
			return a.Line < b.Line
		})
		out = append(out, *ge)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// errorFromAnnotation parses an @error annotation into a code and optional
// status. Arguments may arrive space-joined (TypeScript JSDoc) or as separate
// string/number values (Python/PHP).
func errorFromAnnotation(a ir.Annotation) (code string, status int, ok bool) {
	var fields []string
	for _, arg := range a.Args {
		switch v := arg.(type) {
		case string:
			fields = append(fields, strings.Fields(v)...)
		case int:
			fields = append(fields, strconv.Itoa(v))
		case int64:
			fields = append(fields, strconv.FormatInt(v, 10))
		case float64:
			fields = append(fields, strconv.Itoa(int(v)))
		}
	}
	if len(fields) == 0 {
		return "", 0, false
	}
	code = fields[0]
	for _, f := range fields[1:] {
		if n, err := strconv.Atoi(f); err == nil {
			status = n
			break
		}
	}
	return code, status, true
}
