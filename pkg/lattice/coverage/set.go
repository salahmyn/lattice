package coverage

// @module-feature coverage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// cacheFile is the ingested coverage set, relative to the lattice/ dir,
// beside the test results.
const cacheFile = ".cache/results/coverage.json"

// Evidence bases, the same names the RTM uses.
const (
	BasisPerTest = "per_test"
	BasisPerFile = "per_file"
	BasisSuite   = "suite"
)

// Set is the derived coverage of one run, reduced at ingest so it stays
// small: per-symbol suite facts, and per test the symbols it executed.
type Set struct {
	// Commit is the commit the run covers; Dirty says the working tree had
	// changes to tracked files when it was ingested (DirtyFiles lists some).
	Commit     string   `json:"commit,omitempty"`
	Dirty      bool     `json:"dirty"`
	DirtyFiles []string `json:"dirty_files,omitempty"`
	// Sources are the reports read, with their content hashes.
	Sources []Source `json:"sources"`
	// Symbols is the whole-suite coverage of each symbol whose file a suite
	// report covers.
	Symbols map[string]SymbolCov `json:"symbols,omitempty"`
	// PerTest maps a test FQN to the symbols its own run executed. A test
	// with an entry and an empty list ran and executed nothing; a test with
	// no entry has no per-test evidence.
	PerTest map[string][]string `json:"per_test,omitempty"`
	// PerFile maps a test file to the symbols a run of the whole file
	// executed: weaker evidence, which never promotes.
	PerFile map[string][]string `json:"per_file,omitempty"`
	// EnforcerLines keeps, per test, the body lines of each enforcer its run
	// measured and the ones that ran, for the invariant floor.
	EnforcerLines map[string]map[string]LineSet `json:"enforcer_lines,omitempty"`
}

// Source is one report read into the set.
type Source struct {
	Path   string `json:"path"`
	Format Format `json:"format"`
	SHA256 string `json:"sha256"`
	// Basis is suite, per_test or per_file.
	Basis string `json:"basis"`
	// Test is the test (per_test) or test file (per_file) the report is of.
	Test string `json:"test,omitempty"`
}

// LineSet is one enforcer's instrumented body lines and those that ran.
type LineSet struct {
	Body []int `json:"body"`
	Hit  []int `json:"hit,omitempty"`
}

// Input is one report, or a directory of per-test or per-file reports.
type Input struct {
	// Path is the file or directory to read; Label is how sources record it.
	Path  string
	Label string
	// Basis is "" or suite for a whole-suite report, per_test or per_file
	// for a directory.
	Basis   string
	Format  Format
	Root    string
	Include []string
}

// Options stamps and scopes one ingest.
type Options struct {
	Commit     string
	Dirty      bool
	DirtyFiles []string
	// OnlyVerifiers keeps per-test reports of @verifies tests only. Sources
	// that hold every test in one run (coverage.py contexts, Postgres
	// call-count exports) are kept whole regardless.
	OnlyVerifiers bool
}

// Notes reports what an ingest could not join, so nothing is dropped
// silently.
type Notes struct {
	Unmatched    []string // per-test reports or contexts that name no test
	Ambiguous    []string // names that fit several tests
	NotCoverage  []string // files in a per-test directory that are not coverage
	NonVerifiers int      // per-test reports skipped under only_verifiers
}

// Ingest builds the set from the inputs and writes it to the lattice cache,
// replacing any earlier set.
func Ingest(latticeDir string, kg schema.KnowledgeGraph, inputs []Input, opts Options) (Set, Notes, error) {
	set, notes, err := Build(kg, inputs, opts)
	if err != nil {
		return set, notes, err
	}
	return set, notes, Save(latticeDir, set)
}

// pending is one test's (or test file's) coverage before reduction.
type pending struct {
	report   Report
	included []Included
}

// Build reads every input and reduces it against the graph.
// @enforces coverage:INV-4
func Build(kg schema.KnowledgeGraph, inputs []Input, opts Options) (Set, Notes, error) {
	set := Set{Commit: opts.Commit, Dirty: opts.Dirty, DirtyFiles: opts.DirtyFiles}
	var notes Notes
	res := newResolver(kg)
	tests := newTestIndex(kg)
	suite := NewReport("")
	var suiteIncluded []Included
	perTest := map[string]*pending{}
	perFile := map[string]*pending{}
	add := func(into map[string]*pending, key string, r Report, inc Included) {
		p := into[key]
		if p == nil {
			p = &pending{report: NewReport(r.Format)}
			into[key] = p
		}
		if p.report.Universe == nil {
			p.report.Universe = r.Universe
		}
		p.report.Merge(r)
		p.included = append(p.included, inc)
	}

	for _, in := range inputs {
		label := in.Label
		if label == "" {
			label = in.Path
		}
		remap := func(r Report) Report { return r.remap(func(p string) string { return res.resolve(p, in.Root) }) }
		inc := includeSet(in.Root, in.Include)
		switch in.Basis {
		case "", BasisSuite:
			data, err := os.ReadFile(in.Path)
			if err != nil {
				return set, notes, err
			}
			parsed, err := Parse(data, in.Format)
			if err != nil {
				return set, notes, fmt.Errorf("%s: %w", in.Path, err)
			}
			set.Sources = append(set.Sources, Source{Path: label, Format: parsed.Format, SHA256: digest(data), Basis: BasisSuite})
			suite.Merge(remap(parsed.Report))
			suiteIncluded = append(suiteIncluded, inc)
			// A source that holds every test's data from one run is kept
			// whole: the data is there anyway.
			names := make([]string, 0, len(parsed.Contexts))
			for c := range parsed.Contexts {
				names = append(names, c)
			}
			sort.Strings(names)
			for _, c := range names {
				fqn, why := tests.matchRef(c)
				if fqn == "" {
					notes.note(why, c)
					continue
				}
				add(perTest, fqn, remap(parsed.Contexts[c]), inc)
			}
			for _, run := range parsed.PGRuns {
				r := pgReport(run.Calls)
				switch {
				case run.Test != "":
					fqn, why := tests.matchRef(run.Test)
					if fqn == "" {
						notes.note(why, run.Test)
						continue
					}
					add(perTest, fqn, r, nil)
				default:
					file := res.resolve(run.File, in.Root)
					if !tests.isTestFile(file) {
						notes.Unmatched = append(notes.Unmatched, run.File)
						continue
					}
					add(perFile, file, r, nil)
				}
			}
		case BasisPerTest, BasisPerFile:
			files, err := listReports(in.Path)
			if err != nil {
				return set, notes, err
			}
			for _, p := range files {
				data, err := os.ReadFile(p)
				if err != nil {
					return set, notes, err
				}
				f := in.Format
				if f == "" {
					f = Detect(data)
				}
				if f == "" {
					notes.NotCoverage = append(notes.NotCoverage, p)
					continue
				}
				stem := reportStem(filepath.Base(p))
				var key, why string
				if in.Basis == BasisPerTest {
					key, why = tests.matchReportName(stem)
				} else {
					key, why = tests.matchFileName(stem)
				}
				if key == "" {
					notes.note(why, p)
					continue
				}
				if in.Basis == BasisPerTest && opts.OnlyVerifiers && !tests.verifier[key] {
					notes.NonVerifiers++
					continue
				}
				parsed, err := Parse(data, f)
				if err != nil {
					return set, notes, fmt.Errorf("%s: %w", p, err)
				}
				rel := filepath.ToSlash(filepath.Join(label, filepath.Base(p)))
				set.Sources = append(set.Sources, Source{Path: rel, Format: parsed.Format, SHA256: digest(data), Basis: in.Basis, Test: key})
				if in.Basis == BasisPerTest {
					add(perTest, key, remap(parsed.Report), inc)
				} else {
					add(perFile, key, remap(parsed.Report), inc)
				}
			}
		default:
			return set, notes, fmt.Errorf("%s: unknown basis %q (want per_test or per_file)", in.Path, in.Basis)
		}
	}

	enforcers := enforcerSet(kg)
	if !suite.Empty() {
		set.Symbols = map[string]SymbolCov{}
		included := anyOf(suiteIncluded)
		for _, s := range kg.Symbols {
			c := Of(suite, s, included)
			if !c.Known {
				continue
			}
			if prev, ok := set.Symbols[s.FQN]; ok { // one FQN, several definitions
				c.Lines += prev.Lines
				c.Hit += prev.Hit
				c.Executed = c.Executed || prev.Executed
			}
			set.Symbols[s.FQN] = c
		}
	}
	reduce := func(p *pending) ([]string, map[string]LineSet) {
		included := anyOf(p.included)
		ran := []string{}
		seen := map[string]bool{}
		lines := map[string]LineSet{}
		for _, s := range kg.Symbols {
			c := Of(p.report, s, included)
			if c.Executed && !seen[s.FQN] {
				seen[s.FQN] = true
				ran = append(ran, s.FQN)
			}
			if enforcers[s.FQN] && c.Known && c.Lines > 0 {
				lines[s.FQN] = LineSet{Body: c.bodyLines, Hit: c.hitLines}
			}
		}
		sort.Strings(ran)
		return ran, lines
	}
	if len(perTest) > 0 {
		set.PerTest = map[string][]string{}
		for fqn, p := range perTest {
			ran, lines := reduce(p)
			set.PerTest[fqn] = ran
			if len(lines) > 0 {
				if set.EnforcerLines == nil {
					set.EnforcerLines = map[string]map[string]LineSet{}
				}
				set.EnforcerLines[fqn] = lines
			}
		}
	}
	if len(perFile) > 0 {
		set.PerFile = map[string][]string{}
		for file, p := range perFile {
			ran, _ := reduce(p)
			set.PerFile[file] = ran
		}
	}
	sort.SliceStable(set.Sources, func(i, j int) bool {
		if set.Sources[i].Basis != set.Sources[j].Basis {
			return set.Sources[i].Basis > set.Sources[j].Basis // suite first
		}
		return set.Sources[i].Path < set.Sources[j].Path
	})
	if len(set.Sources) == 0 && len(set.PerTest) == 0 {
		return set, notes, fmt.Errorf("no coverage report read")
	}
	return set, notes, nil
}

func (n *Notes) note(why, what string) {
	if why == "ambiguous" {
		n.Ambiguous = append(n.Ambiguous, what)
	} else {
		n.Unmatched = append(n.Unmatched, what)
	}
}

// enforcerSet is every symbol an invariant or a direct-wired criterion names
// as its enforcer.
func enforcerSet(kg schema.KnowledgeGraph) map[string]bool {
	out := map[string]bool{}
	for _, s := range kg.Symbols {
		if len(s.EnforcesInvariants) > 0 {
			out[s.FQN] = true
		}
	}
	for _, b := range kg.BRDs {
		for _, sc := range b.SuccessCriteria {
			for _, e := range sc.EnforcedBy {
				out[e] = true
			}
		}
	}
	return out
}

// listReports lists the files under a per-test directory, sorted.
func listReports(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// reportStem strips the coverage extensions from a report's file name:
// rejects-an-overdraft.lcov, TestSettle.cover, wallet.test.js.json.
func reportStem(name string) string {
	exts := []string{".lcov", ".info", ".json", ".xml", ".out", ".cover", ".coverprofile",
		".cov", ".txt", ".csv", ".profile"}
	for changed := true; changed; {
		changed = false
		for _, e := range exts {
			if strings.HasSuffix(strings.ToLower(name), e) && len(name) > len(e) {
				name = name[:len(name)-len(e)]
				changed = true
			}
		}
	}
	return name
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Save writes the set to lattice/.cache/results/coverage.json.
func Save(latticeDir string, set Set) error {
	p := filepath.Join(latticeDir, cacheFile)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0o644)
}

// Load reads the ingested set. ok is false when no coverage was ingested:
// callers then raise no coverage finding and promote nothing.
func Load(latticeDir string) (Set, bool) {
	data, err := os.ReadFile(filepath.Join(latticeDir, cacheFile))
	if err != nil {
		return Set{}, false
	}
	var set Set
	if json.Unmarshal(data, &set) != nil {
		return Set{}, false
	}
	return set, true
}

// testIndex joins report names and test references to graph tests.
type testIndex struct {
	byFQN    map[string]bool
	verifier map[string]bool
	slug     map[string][]string // report slug, slug of the FQN, slug of the leaf -> tests
	files    map[string]bool     // test files
	fileSlug map[string][]string // slug of a test file path (with and without extension, base name too) -> file
	norm     map[string]string   // normalized FQN -> FQN
}

func newTestIndex(kg schema.KnowledgeGraph) *testIndex {
	ix := &testIndex{byFQN: map[string]bool{}, verifier: map[string]bool{}, slug: map[string][]string{},
		files: map[string]bool{}, fileSlug: map[string][]string{}, norm: map[string]string{}}
	addSlug := func(m map[string][]string, k, v string) {
		for _, have := range m[k] {
			if have == v {
				return
			}
		}
		m[k] = append(m[k], v)
	}
	reportSlugs := results.ReportSlugs(kg.Tests)
	for _, t := range kg.Tests {
		ix.byFQN[t.FQN] = true
		if len(t.Verifies) > 0 {
			ix.verifier[t.FQN] = true
		}
		addSlug(ix.slug, reportSlugs[t.FQN], t.FQN)
		addSlug(ix.slug, results.Slug(t.FQN), t.FQN)
		leaf := t.FQN[strings.LastIndex(t.FQN, ".")+1:]
		addSlug(ix.slug, results.Slug(leaf), t.FQN)
		ix.norm[normRef(t.FQN)] = t.FQN
		if t.File != "" && !ix.files[t.File] {
			ix.files[t.File] = true
			noExt := strings.TrimSuffix(t.File, filepath.Ext(t.File))
			base := filepath.Base(t.File)
			for _, v := range []string{t.File, noExt, base, strings.TrimSuffix(base, filepath.Ext(base))} {
				addSlug(ix.fileSlug, results.Slug(v), t.File)
			}
		}
	}
	return ix
}

func (ix *testIndex) isTestFile(f string) bool { return ix.files[f] }

// matchReportName joins a per-test report's stem to a test: its FQN, its
// report slug (what `lattice results tests` prints), or a slug that only one
// test has. why is "ambiguous" or "unmatched" when it fails.
func (ix *testIndex) matchReportName(stem string) (string, string) {
	if ix.byFQN[stem] {
		return stem, ""
	}
	hits := ix.slug[results.Slug(stem)]
	switch len(hits) {
	case 1:
		return hits[0], ""
	case 0:
		return "", "unmatched"
	}
	return "", "ambiguous"
}

// matchFileName joins a per-file report's stem to a test file.
func (ix *testIndex) matchFileName(stem string) (string, string) {
	if ix.files[stem] {
		return stem, ""
	}
	hits := ix.fileSlug[results.Slug(stem)]
	switch len(hits) {
	case 1:
		return hits[0], ""
	case 0:
		return "", "unmatched"
	}
	return "", "ambiguous"
}

// matchRef joins a runner's test reference (a coverage.py context such as
// tests.test_wallet.test_x or tests/test_wallet.py::TestX::test_y, a pgTAP
// description) to a test FQN: exactly, else by the longest whole-segment
// suffix either way, else by a unique slug. Ties are ambiguous, never guessed.
func (ix *testIndex) matchRef(ref string) (string, string) {
	if ix.byFQN[ref] {
		return ref, ""
	}
	// "<file>::<title>", as pgTAP and JS runners name a case: the adapters
	// key such a test <file without extension>.test.<slug of the title>.
	if file, title, ok := strings.Cut(ref, "::"); ok && filepath.Ext(file) != "" && !strings.Contains(title, "::") {
		fqn := strings.ReplaceAll(strings.TrimSuffix(file, filepath.Ext(file)), "/", ".") + ".test." + results.Slug(title)
		if ix.byFQN[fqn] {
			return fqn, ""
		}
	}
	key := normRef(ref)
	if fqn, ok := ix.norm[key]; ok {
		return fqn, ""
	}
	best, bestScore, tied := "", 0, false
	for n, fqn := range ix.norm {
		score := 0
		switch {
		case strings.HasSuffix(n, "."+key):
			score = len(key)
		case strings.HasSuffix(key, "."+n):
			score = len(n)
		default:
			continue
		}
		switch {
		case score > bestScore:
			best, bestScore, tied = fqn, score, false
		case score == bestScore:
			tied = true
		}
	}
	if best != "" && !tied {
		return best, ""
	}
	if tied {
		return "", "ambiguous"
	}
	return ix.matchReportName(ref)
}

// normRef lowercases a test reference and unifies its separators to dots:
// "tests/test_x.py::TestX::test_y[2]" -> "tests.test_x.testx.test_y".
func normRef(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.Index(s, "["); i > 0 && strings.HasSuffix(s, "]") {
		s = s[:i] // a parametrized case joins its test
	}
	s = strings.ReplaceAll(s, ".py::", "::")
	for _, sep := range []string{"::", "/", "\\", "#", " "} {
		s = strings.ReplaceAll(s, sep, ".")
	}
	for strings.Contains(s, "..") {
		s = strings.ReplaceAll(s, "..", ".")
	}
	return strings.Trim(s, ".")
}
