package coverage

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// The body and hit lines of a symbol come back sorted, for the line-level
// audit; the declaration line is not among them.
// @verifies coverage:INV-2
func TestSymbolLinesAreExposed(t *testing.T) {
	rep := NewReport(FormatLCOV)
	f := rep.File("a.js")
	f.Lines[9], f.Lines[2], f.Lines[3], f.Lines[4] = 1, 1, 0, 2
	c := Of(rep, schema.GraphSymbol{FQN: "f", File: "a.js", Line: 1, EndLine: 5}, nil)
	if !reflect.DeepEqual(c.BodyLines(), []int{2, 3, 4}) || !reflect.DeepEqual(c.HitLines(), []int{2, 4}) {
		t.Errorf("body %v hit %v", c.BodyLines(), c.HitLines())
	}
}

// The suite reports a workspace names are read, mapped onto the graph's files
// and merged; a report that is missing or not coverage is named, never read as
// a report with nothing covered.
func TestSuiteReport(t *testing.T) {
	base := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(base, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.lcov", "SF:app/a.js\nDA:2,1\nend_of_record\n")
	write("b.lcov", "SF:a.js\nDA:2,1\nDA:3,0\nend_of_record\n")
	write("junk.txt", "not coverage")
	kg := schema.KnowledgeGraph{Symbols: []schema.GraphSymbol{{FQN: "x", File: "app/a.js", Line: 1, EndLine: 4}}}
	rep, _, problems := SuiteReport(base, kg, config.Coverage{Reports: []config.CoverageReport{
		{Path: "a.lcov"}, {Path: "b.lcov", Root: "app"}, {Path: "junk.txt"}, {Path: "absent.lcov"}}})
	f := rep.Files["app/a.js"]
	if f == nil || f.Lines[2] != 2 || f.Lines[3] != 0 {
		t.Fatalf("merged report = %+v", rep.Files)
	}
	if len(problems) != 2 {
		t.Errorf("problems = %v, want the junk file and the missing one", problems)
	}
}
