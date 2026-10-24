package coverage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fixtures. Unless a test says otherwise, every file was written by the tool
// that names it, not by hand:
//
//   - examples/coverage-prototype/coverage/: Node 22 `node --test` LCOV and
//     JUnit (examples/coverage-prototype/regen.sh);
//   - testdata/c8/: c8 10.1.3 json, cobertura and clover reporters over the
//     same example (`c8 --all --include 'src/**' node --test tests/*.test.js`),
//     with the absolute checkout path rewritten to /work/coverage-prototype;
//   - testdata/python/coverage.json: coverage.py 7.10.6 with pytest 8.4.2,
//     dynamic_context = test_function (testdata/python/regen.sh);
//   - testdata/gomod/coverage/: `go test -coverprofile` of a fixture module
//     (testdata/gomod/regen.sh); testdata/go/ledger.cover: of this
//     repository's pkg/lattice/ledger.
const example = "../../../examples/coverage-prototype"

func read(t *testing.T, p string) []byte {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return data
}

func parse(t *testing.T, p string) Parsed {
	t.Helper()
	got, err := Parse(read(t, p), "")
	if err != nil {
		t.Fatalf("parse %s: %v", p, err)
	}
	return got
}

// Every format is told apart by content; test reports are not coverage.
// @verifies coverage:INV-1
func TestDetect(t *testing.T) {
	for p, want := range map[string]Format{
		filepath.Join(example, "coverage/all.lcov"):  FormatLCOV,
		"testdata/gomod/coverage/all.cover":          FormatGo,
		"testdata/go/ledger.cover":                   FormatGo,
		"testdata/c8/coverage-final.json":            FormatIstanbul,
		"testdata/c8/cobertura-coverage.xml":         FormatCobertura,
		"testdata/c8/clover.xml":                     FormatClover,
		"testdata/python/coverage.json":              FormatCoveragePy,
		"testdata/pg/calls.json":                     FormatPGCalls,
		"testdata/pg/calls.csv":                      FormatPGCalls,
		filepath.Join(example, "coverage/junit.xml"): "",
	} {
		if got := Detect(read(t, p)); got != want {
			t.Errorf("Detect(%s) = %q, want %q", p, got, want)
		}
	}
	// A TAP stream (pgTAP from pg_prove) and an empty file are test reports.
	for _, text := range []string{"ok 1 - a refund is recorded once\n1..1\n", "", "{}", "<testsuites/>"} {
		if got := Detect([]byte(text)); got != "" {
			t.Errorf("Detect(%q) = %q, want none", text, got)
		}
	}
}

// LCOV from node --test: lines, and function records at declaration lines.
func TestParseLCOV(t *testing.T) {
	r := parse(t, filepath.Join(example, "coverage/per-test/rejects-an-overdraft.lcov")).Report
	w := r.Files["src/wallet.js"]
	if w == nil {
		t.Fatal("src/wallet.js missing")
	}
	if w.Lines[8] != 1 || w.Lines[9] != 0 || w.Funcs[8] != 0 || w.Funcs[25] != 1 {
		t.Errorf("wallet.js: line 8 = %d, line 9 = %d, FN 8 = %d, FN 25 = %d; want 1 0 0 1",
			w.Lines[8], w.Lines[9], w.Funcs[8], w.Funcs[25])
	}
	if _, ok := r.Files["src/cli.js"]; ok {
		t.Error("node --test lists only loaded files; src/cli.js should be absent")
	}
	// LCOV 2 writes FN:<start>,<end>,<name>. Hand-written, the shape lcov 2.0 documents.
	r2, err := ParseLCOV([]byte("SF:a.js\nFN:3,9,f\nFNDA:2,f\nDA:4,2\nend_of_record\n"))
	if err != nil || r2.Files["a.js"].Funcs[3] != 2 {
		t.Errorf("lcov 2 FN record: %v %+v", err, r2.Files["a.js"])
	}
	if _, err := ParseLCOV([]byte("TN:\n")); err == nil {
		t.Error("an LCOV file with no SF record should fail")
	}
}

// Go blocks give their count to every line they span; files keep their
// import paths until the ingest joins them to the graph.
func TestParseGoProfile(t *testing.T) {
	r := parse(t, "testdata/gomod/coverage/per-test/testrejectsanoverdraft.cover").Report
	f := r.Files["example.com/shop/wallet/wallet.go"]
	if f == nil || !f.DeclEvidence {
		t.Fatalf("profile = %+v", r.Files)
	}
	if f.Lines[11] != 0 || f.Lines[30] != 1 || f.Lines[33] != 1 {
		t.Errorf("lines 11, 30, 33 = %d %d %d; want 0 1 1", f.Lines[11], f.Lines[30], f.Lines[33])
	}
	repo := parse(t, "testdata/go/ledger.cover").Report
	if len(repo.Files) == 0 {
		t.Fatal("no files in this repository's ledger profile")
	}
	for p := range repo.Files {
		if !strings.HasPrefix(p, "github.com/salahmyn/lattice/pkg/lattice/ledger/") {
			t.Errorf("unexpected file %s", p)
		}
	}
	if _, err := ParseGoProfile([]byte("mode: set\nnot a block\n")); err == nil {
		t.Error("a malformed block should fail")
	}
}

func TestParseIstanbul(t *testing.T) {
	r := parse(t, "testdata/c8/coverage-final.json").Report
	w := r.Files["/work/coverage-prototype/src/wallet.js"]
	if w == nil {
		t.Fatalf("files = %v", keys(r.Files))
	}
	// The suite never runs the guard (the only verifier stubs it); it runs
	// withdraw and deposit.
	if w.Funcs[8] != 0 || w.Funcs[25] != 1 || w.Funcs[30] != 1 {
		t.Errorf("fnMap counts at 8, 25, 30 = %d %d %d; want 0 1 1", w.Funcs[8], w.Funcs[25], w.Funcs[30])
	}
	if cli := r.Files["/work/coverage-prototype/src/cli.js"]; cli == nil || cli.Lines[9] != 0 {
		t.Errorf("c8 --all lists the unloaded src/cli.js with zero hits: %+v", cli)
	}
}

func TestParseCobertura(t *testing.T) {
	r := parse(t, "testdata/c8/cobertura-coverage.xml").Report
	w := r.Files["/work/coverage-prototype/src/wallet.js"]
	if w == nil {
		t.Fatalf("files = %v (filenames join the first <source>)", keys(r.Files))
	}
	if w.Funcs[8] != 0 || w.Funcs[25] != 1 || w.Lines[9] != 0 || w.Lines[26] != 1 {
		t.Errorf("wallet.js: FN 8 %d, FN 25 %d, line 9 %d, line 26 %d; want 0 1 0 1",
			w.Funcs[8], w.Funcs[25], w.Lines[9], w.Lines[26])
	}
}

func TestParseClover(t *testing.T) {
	r := parse(t, "testdata/c8/clover.xml").Report
	w := r.Files["/work/coverage-prototype/src/wallet.js"]
	if w == nil || w.Lines[9] != 0 || w.Lines[26] != 1 {
		t.Fatalf("wallet.js = %+v", w)
	}
	// c8 writes no method lines; PHPUnit does. Hand-written in PHPUnit's shape.
	php, err := ParseClover([]byte(`<coverage><project><file name="/app/src/Wallet.php">
<line num="8" type="method" name="assertFunds" count="0"/><line num="9" type="stmt" count="0"/>
<line num="14" type="method" name="deposit" count="3"/><line num="15" type="stmt" count="3"/>
</file></project></coverage>`))
	if err != nil {
		t.Fatal(err)
	}
	f := php.Files["/app/src/Wallet.php"]
	if f.Funcs[8] != 0 || f.Funcs[14] != 3 || f.Lines[15] != 3 {
		t.Errorf("phpunit clover = %+v", f)
	}
	if _, ok := f.Lines[14]; ok {
		t.Error("a method line is a function record, not a body line")
	}
}

// coverage.py: one run gives the suite and every test's own lines.
func TestParseCoveragePy(t *testing.T) {
	p := parse(t, "testdata/python/coverage.json")
	w := p.Report.Files["src/wallet.py"]
	if w == nil || w.Lines[9] != 0 || w.Lines[15] == 0 {
		t.Fatalf("suite wallet.py = %+v", w)
	}
	over := p.Contexts["test_wallet.test_rejects_an_overdraft"]
	if over.Files == nil {
		t.Fatalf("contexts = %v", keys2(p.Contexts))
	}
	if f := over.Files["src/wallet.py"]; f.Lines[20] != 1 || f.Lines[9] != 0 {
		t.Errorf("overdraft test ran line 20 (withdraw) but not 9 (the guard): %+v", f.Lines)
	}
	if _, ok := p.Contexts[""]; ok {
		t.Error("the empty context (imports) is no test")
	}
	// pytest-cov --cov-context=test names contexts <nodeid>|<phase>; only the
	// run phase counts. Hand-written in pytest-cov's shape.
	_, ctx, err := ParseCoveragePy([]byte(`{"meta":{"show_contexts":true},"files":{"a.py":{
"executed_lines":[1,2],"missing_lines":[3],
"contexts":{"2":["tests/test_a.py::test_x|run","tests/test_a.py::test_x|setup"]}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ctx) != 1 || ctx["tests/test_a.py::test_x"].Files["a.py"].Lines[2] != 1 {
		t.Errorf("pytest-cov contexts = %v", keys2(ctx))
	}
}

// Postgres call counts: hand-written (no Postgres server here), in the
// export shape docs/user-guide.md defines.
func TestParsePGCalls(t *testing.T) {
	j := parse(t, "testdata/pg/calls.json")
	c := parse(t, "testdata/pg/calls.csv")
	for name, p := range map[string]Parsed{"json": j, "csv": c} {
		if len(p.PGRuns) != 2 {
			t.Fatalf("%s: runs = %+v", name, p.PGRuns)
		}
		if p.PGRuns[0].Calls["sql.public.debit"] != 1 || p.PGRuns[0].Calls["sql.public.debit_v1"] != 0 {
			t.Errorf("%s: first run = %+v", name, p.PGRuns[0])
		}
		if p.Report.Symbols["sql.public.debit"] != 3 {
			t.Errorf("%s: suite debit calls = %d, want 3", name, p.Report.Symbols["sql.public.debit"])
		}
	}
	if _, err := ParsePGCallsCSV([]byte("schema,function,calls\npublic,f,1\n")); err == nil {
		t.Error("a CSV with no test or file column should fail")
	}
	if _, err := ParsePGCallsJSON([]byte(`{"format":"pg_stat_user_functions","runs":[{"functions":[]}]}`)); err == nil {
		t.Error("a run that names no test or file should fail")
	}
}

func keys(m map[string]*FileCov) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func keys2(m map[string]Report) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
