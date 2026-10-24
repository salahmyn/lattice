package blocks

// @module-feature blocks

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

var optionalVar = regexp.MustCompile(`/?\{[^}]*\?\}`)

// CodeRoute is a route the code registers, found by extraction.
type CodeRoute struct {
	Method string // GET, POST, ..., ANY
	Path   string
	File   string
	Line   int
}

// Verification checks Target's route blocks against the routes Source's code registers.
type Verification struct {
	Source, Target string
	Routes         []CodeRoute
	Stamp          schema.SourceStamp // what Source's code was read at
}

// verifyRoutes marks a route block wired when the code registers its path with
// one of its methods. ANY on either side matches every method.
func (g *graph) verifyRoutes(vs []Verification, srcs []SourceInput) {
	for _, v := range vs {
		i := -1
		for j, s := range srcs {
			if s.Info.ID == v.Target {
				i = j
			}
		}
		if i < 0 {
			g.warnf("source %s verifies %q, which is not a source with a catalogue", v.Source, v.Target)
			continue
		}
		code := map[string]map[string]CodeRoute{} // path -> method -> first route
		for _, r := range v.Routes {
			paths := []string{normRoute(r.Path, nil)}
			if req := optionalVar.ReplaceAllString(r.Path, ""); req != r.Path { // /a/{x?} also answers /a
				paths = append(paths, normRoute(req, nil))
			}
			for _, p := range paths {
				if code[p] == nil {
					code[p] = map[string]CodeRoute{}
				}
				if _, ok := code[p][strings.ToUpper(r.Method)]; !ok {
					code[p][strings.ToUpper(r.Method)] = r
				}
			}
		}
		for _, k := range sortedKeys(g.sources[i].blocks) {
			b := g.blocks[k]
			if b == nil || b.Kind != "route" || b.Attrs["http"] == "" {
				continue
			}
			methods, path, _ := strings.Cut(b.Attrs["http"], " ")
			if r, ok := matchRoute(code[normRoute(path, nil)], strings.Split(strings.ToUpper(methods), "|")); ok {
				b.Truth = schema.TruthWired
				b.Attrs["wired_by"] = fmt.Sprintf("%s:%s:%d", v.Source, r.File, r.Line)
			}
		}
	}
}

func matchRoute(byMethod map[string]CodeRoute, methods []string) (CodeRoute, bool) {
	if r, ok := byMethod["ANY"]; ok {
		return r, true
	}
	for _, m := range methods {
		if m == "ANY" {
			for _, k := range sortedKeys(byMethod) {
				return byMethod[k], true
			}
		}
		if r, ok := byMethod[m]; ok {
			return r, true
		}
		if r, ok := byMethod["GET"]; ok && m == "HEAD" { // Laravel answers HEAD on every GET route
			return r, true
		}
	}
	return CodeRoute{}, false
}
