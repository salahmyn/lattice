// Package coverage ingests test coverage reports and maps them onto the
// knowledge graph's symbols by line extent (docs/rnd/0009).
//
// A report is file -> line -> hits, plus function records where the format
// has them. `lattice results ingest` reads whole-suite and per-test reports,
// reduces them to per-symbol facts and per-test executed-symbol sets, and
// stores the result in lattice/.cache/results/coverage.json beside the test
// results. The RTM reads it to lift a demonstrated criterion to exercised,
// and validate reads it for the coverage findings. Lattice ingests what CI
// produced; it never runs tests.
package coverage

// @module-feature coverage

// Format names a coverage report format.
type Format string

const (
	FormatLCOV       Format = "lcov"       // LCOV tracefile: c8, nyc, vitest, Jest, node --test, Deno, coverage.py lcov
	FormatGo         Format = "go"         // go test -coverprofile
	FormatIstanbul   Format = "istanbul"   // istanbul coverage-final.json (nyc, c8, vitest, Jest)
	FormatCoveragePy Format = "coveragepy" // coverage.py json, with --show-contexts for per-test data
	FormatCobertura  Format = "cobertura"  // cobertura XML
	FormatClover     Format = "clover"     // clover XML
	FormatPGCalls    Format = "pg_stat_functions"
)

// Formats lists every format a report may name in config.yaml.
var Formats = []Format{FormatLCOV, FormatGo, FormatIstanbul, FormatCoveragePy,
	FormatCobertura, FormatClover, FormatPGCalls}

// FileCov is the coverage of one source file in one run.
type FileCov struct {
	// Lines maps a 1-based line to its hit count. A line absent from the map
	// was not instrumented (blank, comment, declaration only).
	Lines map[int]int
	// Funcs maps a function's declaration line to its call count (LCOV
	// FN/FNDA, istanbul fnMap, clover method lines). Empty for formats
	// without function records.
	Funcs map[int]int
	// DeclEvidence is true for formats that never mark a declaration line
	// as run on module load (Go blocks), so a one-line symbol's own line is
	// evidence. V8, istanbul and coverage.py mark declarations when the
	// module loads, so there it is not.
	DeclEvidence bool
}

// Report is the coverage of one run, keyed by slash-separated file path.
type Report struct {
	Format Format
	Files  map[string]*FileCov
	// Symbols holds symbol-level evidence from formats that have no lines,
	// such as Postgres function call counts: symbol FQN -> calls.
	Symbols map[string]int
	// Universe, when set, is every file the run measured: a file in it but
	// absent from Files ran no line in this run. coverage.py contexts share
	// one run, so a test that never touched a measured file ran none of it.
	Universe map[string]*FileCov
}

// NewReport returns an empty report of the given format.
func NewReport(f Format) Report {
	return Report{Format: f, Files: map[string]*FileCov{}, Symbols: map[string]int{}}
}

// File returns the coverage of path, adding it when absent.
func (r Report) File(path string) *FileCov {
	f := r.Files[path]
	if f == nil {
		f = &FileCov{Lines: map[int]int{}, Funcs: map[int]int{}, DeclEvidence: r.Format == FormatGo}
		r.Files[path] = f
	}
	return f
}

// Empty reports whether the report holds no file and no symbol.
func (r Report) Empty() bool { return len(r.Files) == 0 && len(r.Symbols) == 0 }

// Merge adds other's line, function and symbol hits into r: the union of
// two runs.
func (r Report) Merge(other Report) {
	for p, o := range other.Files {
		f := r.File(p)
		f.DeclEvidence = f.DeclEvidence || o.DeclEvidence
		for l, h := range o.Lines {
			f.Lines[l] += h
		}
		for l, h := range o.Funcs {
			f.Funcs[l] += h
		}
	}
	for s, n := range other.Symbols {
		r.Symbols[s] += n
	}
}

// remap rewrites the report's file keys through resolve (report path ->
// graph path), merging files that resolve to the same path.
func (r Report) remap(resolve func(string) string) Report {
	out := NewReport(r.Format)
	for p, f := range r.Files {
		g := out.File(resolve(p))
		g.DeclEvidence = g.DeclEvidence || f.DeclEvidence
		for l, h := range f.Lines {
			g.Lines[l] += h
		}
		for l, h := range f.Funcs {
			g.Funcs[l] += h
		}
	}
	for s, n := range r.Symbols {
		out.Symbols[s] += n
	}
	if r.Universe != nil {
		out.Universe = map[string]*FileCov{}
		for p, f := range r.Universe {
			out.Universe[resolve(p)] = f
		}
	}
	return out
}
