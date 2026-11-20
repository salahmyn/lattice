package results

// @module-feature results

import (
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Selector is one test with the arguments each test runner takes to run it
// alone. Lattice never runs tests; CI loops over these (in parallel) to
// collect per-test coverage, writing each report under the test's Slug.
type Selector struct {
	Test     string   `json:"test"`
	Name     string   `json:"name"`
	Language string   `json:"language"`
	File     string   `json:"file"`
	Slug     string   `json:"slug"`
	Package  string   `json:"package,omitempty"` // go: the package to `go test`
	Verifies []string `json:"verifies,omitempty"`
	// Parameterized marks a test named by a title template; its filters
	// match every name the runner expands the template to.
	Parameterized bool `json:"parameterized,omitempty"`
	// Filters maps a runner to the arguments that select this test:
	// go (-run), node (--test-name-pattern), vitest and jest (-t), deno
	// (--filter), pytest (a node id), pytest-k (-k), phpunit (--filter),
	// pg_prove (the test file: pgTAP runs a file as one unit).
	Filters map[string][]string `json:"filters"`
}

// Runners lists the runner names a Selector's Filters may hold, per language.
var Runners = map[string][]string{
	"go":         {"go"},
	"typescript": {"node", "vitest", "jest", "deno"},
	"javascript": {"node", "vitest", "jest", "deno"},
	"python":     {"pytest", "pytest-k"},
	"php":        {"phpunit"},
	"postgres":   {"pg_prove"},
}

// ReportSlugs names the per-test coverage report of each test: the slug of
// the last segment of its FQN when no other test shares it, else the slug of
// the whole FQN. The coverage ingest joins a report to its test by this name.
func ReportSlugs(tests []schema.GraphSymbol) map[string]string {
	leafCount := map[string]int{}
	for _, t := range tests {
		leafCount[slugify(leaf(t.FQN))]++
	}
	out := make(map[string]string, len(tests))
	for _, t := range tests {
		s := slugify(leaf(t.FQN))
		if leafCount[s] > 1 {
			s = slugify(t.FQN)
		}
		out[t.FQN] = s
	}
	return out
}

func leaf(fqn string) string {
	if i := strings.LastIndex(fqn, "."); i >= 0 {
		return fqn[i+1:]
	}
	return fqn
}

// Selectors lists the graph's tests (only @verifies tests when
// verifiersOnly) with the filter each runner of their language takes,
// sorted by language then FQN.
// @enforces results:INV-5
func Selectors(kg schema.KnowledgeGraph, verifiersOnly bool) []Selector {
	slugs := ReportSlugs(kg.Tests)
	var out []Selector
	for _, t := range kg.Tests {
		if verifiersOnly && len(t.Verifies) == 0 {
			continue
		}
		s := Selector{Test: t.FQN, Name: t.Name, Language: t.Language, File: t.File,
			Slug: slugs[t.FQN], Verifies: t.Verifies, Parameterized: t.Parameterized,
			Filters: map[string][]string{}}
		re := s.TitlePattern()
		switch t.Language {
		case "go":
			dir := path.Dir(t.File)
			s.Package = "./" + dir
			if dir == "." {
				s.Package = "."
			}
			s.Filters["go"] = []string{"-run", goRunPattern(t.FQN, dir)}
		case "typescript", "javascript":
			s.Filters["node"] = []string{"--test-name-pattern", "^" + re + "$"}
			s.Filters["vitest"] = []string{"-t", re + "$"}
			s.Filters["jest"] = []string{"-t", re + "$"}
			s.Filters["deno"] = []string{"--filter", "/^" + re + "$/"}
		case "python":
			s.Filters["pytest"] = []string{pytestNodeID(t.FQN, t.File)}
			s.Filters["pytest-k"] = []string{"-k", leaf(t.FQN)}
		case "php":
			s.Filters["phpunit"] = []string{"--filter", "/::" + regexp.QuoteMeta(leaf(t.FQN)) + "$/"}
		case "postgres":
			s.Filters["pg_prove"] = []string{t.File}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Language != out[j].Language {
			return out[i].Language < out[j].Language
		}
		return out[i].Test < out[j].Test
	})
	return out
}

// titlePlaceholder matches the placeholders of a parameterized test's title,
// as the TypeScript adapter keeps them: template substitutions (${expr}) and
// the .each verbs (%s %d %i %f %j %o %O %p %#, $name).
var titlePlaceholder = regexp.MustCompile(`%[sdifjoOp#]|\$\{[^}]*\}|\$[A-Za-z_][A-Za-z0-9_.]*`)

// TitlePattern is the unanchored regular expression a name-filtering runner
// (vitest and jest -t, node --test-name-pattern, Playwright -g, deno
// --filter) takes to select this test by its title. A plain title matches as
// written; in a parameterized test's title each placeholder matches any
// non-empty text, so the filter selects every expanded name.
// @enforces results:INV-5
func (s Selector) TitlePattern() string {
	title := s.Name
	if title == "" {
		title = leaf(s.Test)
	}
	if !s.Parameterized {
		return regexp.QuoteMeta(title)
	}
	var b strings.Builder
	last := 0
	for _, loc := range titlePlaceholder.FindAllStringIndex(title, -1) {
		b.WriteString(regexp.QuoteMeta(title[last:loc[0]]))
		b.WriteString(".+?")
		last = loc[1]
	}
	b.WriteString(regexp.QuoteMeta(title[last:]))
	return b.String()
}

// goRunPattern turns a Go test FQN (<package dir dotted>.TestX[.subtest])
// into a -run pattern anchored per level: ^TestX$/^subtest$.
func goRunPattern(fqn, dir string) string {
	rest := fqn
	if dir != "." && dir != "" {
		rest = strings.TrimPrefix(fqn, strings.ReplaceAll(dir, "/", ".")+".")
	}
	parts := strings.Split(rest, ".")
	for i, p := range parts {
		parts[i] = "^" + regexp.QuoteMeta(p) + "$"
	}
	return strings.Join(parts, "/")
}

// pytestNodeID turns a Python test FQN (<module dotted>.[Class.]test_x) into
// a pytest node id: tests/test_x.py::Class::test_x.
func pytestNodeID(fqn, file string) string {
	module := strings.ReplaceAll(strings.TrimSuffix(file, ".py"), "/", ".")
	rest := strings.TrimPrefix(fqn, module+".")
	if rest == fqn {
		return file + "::" + leaf(fqn)
	}
	return file + "::" + strings.ReplaceAll(rest, ".", "::")
}
