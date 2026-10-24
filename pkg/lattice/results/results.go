// Package results ingests test-result reports (junit/pytest/phpunit XML)
// and exposes a pass/fail lookup keyed to the verifier tests in the
// knowledge graph.
//
// This is the v0.8 γ stream: today's RTM marks a criterion "verified"
// when a `@verifies` test *exists*; γ adds the missing rung —
// DEMONSTRATED — which means that verifier actually *passed* on the
// generated commit. The ingested set lives under lattice/.cache/results/
// and is opt-in (`lattice results ingest <file>`); without it, RTM
// behaves exactly as it did in v0.7.
package results

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Outcome is the result of one test case.
type Outcome string

const (
	Pass Outcome = "pass"
	Fail Outcome = "fail"
	Skip Outcome = "skip"
)

// Set is the ingested result corpus. Keys are normalized test
// identifiers (see normalize); the same case is indexed under several
// keys so a lattice test FQN can match by its trailing segment.
type Set struct {
	Commit  string             `json:"commit,omitempty"`
	Results map[string]Outcome `json:"results"`
}

// cacheFile is the on-disk location of the ingested set, relative to the
// lattice/ directory.
const cacheFile = ".cache/results/results.json"

// junit mirrors the common junit/pytest/phpunit XML shape. The three
// tools agree on testsuite/testcase with classname+name; failures and
// errors are child elements (or, for phpunit, attributes we don't need).
type junitSuites struct {
	XMLName xml.Name     `xml:"testsuites"`
	Suites  []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	XMLName xml.Name     `xml:"testsuite"`
	Name    string       `xml:"name,attr"`
	Cases   []junitCase  `xml:"testcase"`
	Suites  []junitSuite `xml:"testsuite"` // nested suites (pytest)
}

type junitCase struct {
	Classname string        `xml:"classname,attr"`
	Name      string        `xml:"name,attr"`
	Failure   *junitOutcome `xml:"failure"`
	Error     *junitOutcome `xml:"error"`
	Skipped   *junitOutcome `xml:"skipped"`
}

type junitOutcome struct {
	Message string `xml:"message,attr"`
}

// ParseJUnit reads a junit-family XML report and returns a normalized
// Set. A top-level <testsuite> (no <testsuites> wrapper) is accepted too.
func ParseJUnit(data []byte) (Set, error) {
	set := Set{Results: map[string]Outcome{}}

	var suites junitSuites
	if err := xml.Unmarshal(data, &suites); err == nil && len(suites.Suites) > 0 {
		for _, s := range suites.Suites {
			collect(s, set.Results)
		}
		return set, nil
	}

	// Fall back to a bare <testsuite> root.
	var single junitSuite
	if err := xml.Unmarshal(data, &single); err != nil {
		return set, fmt.Errorf("parse junit xml: %w", err)
	}
	if single.XMLName.Local != "testsuite" {
		return set, fmt.Errorf("parse junit xml: no <testsuites> or <testsuite> root")
	}
	collect(single, set.Results)
	return set, nil
}

// collect walks a suite (and nested suites) recording one Outcome per
// case under every key normalize emits.
func collect(s junitSuite, out map[string]Outcome) {
	for _, c := range s.Cases {
		o := Pass
		switch {
		case c.Failure != nil || c.Error != nil:
			o = Fail
		case c.Skipped != nil:
			o = Skip
		}
		for _, k := range keysFor(c.Classname, c.Name) {
			record(out, k, o)
		}
	}
	for _, nested := range s.Suites {
		collect(nested, out)
	}
}

// record stores an outcome under a key. A failure anywhere wins over a pass
// under the same key, across cases, projects and reports.
func record(out map[string]Outcome, k string, o Outcome) {
	if prev, ok := out[k]; ok && prev == Fail {
		return
	}
	out[k] = o
}

// keysFor returns the lookup keys for a test case: the fully-qualified
// "classname.name", the bare name, and the name with method separators
// normalized to dots. Indexing under several forms lets a lattice test
// FQN match by suffix regardless of language separator conventions.
func keysFor(classname, name string) []string {
	name = strings.TrimSpace(name)
	classname = strings.TrimSpace(classname)
	seen := map[string]bool{}
	var keys []string
	add := func(k string) {
		k = normalize(k)
		if k != "" && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	if classname != "" {
		add(classname + "." + name)
		if k := jsTestKey(classname, name); k != "" {
			add(k)
		}
	}
	add(name)
	return keys
}

// jsTestKey indexes a vitest/jest/Playwright case under the FQN shape the
// TypeScript adapter gives it-blocks: <file path without extension>.test.<slug
// of the it() description>. These runners report classname as the file path
// and name as the title path, "describe > ... > it description" (vitest/jest)
// or "describe › ... › test title" (Playwright); only the last part is the
// it() text.
// Lookup's suffix match then joins the adapter's repo-relative FQN to the
// runner's package-relative path.
func jsTestKey(classname, name string) string {
	ext := path.Ext(classname)
	switch ext {
	case ".ts", ".tsx", ".js", ".jsx", ".mts", ".cts", ".mjs", ".cjs":
	default:
		return ""
	}
	leaf := name
	for _, sep := range []string{" > ", " › "} {
		if i := strings.LastIndex(leaf, sep); i >= 0 {
			leaf = leaf[i+len(sep):]
		}
	}
	return strings.TrimSuffix(classname, ext) + ".test." + slugify(leaf)
}

// slugify mirrors the TypeScript adapter's test-name slug: lowercase ASCII
// letters and digits, every other run collapsed to one dash.
func slugify(s string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "test"
	}
	return out
}

// normalize lowercases and unifies separators (::, /, \, #, space) to a
// single dot so cross-language identifiers compare cleanly.
func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, sep := range []string{"::", "\\", "/", "#", " ", "()"} {
		s = strings.ReplaceAll(s, sep, ".")
	}
	// Collapse repeated dots.
	for strings.Contains(s, "..") {
		s = strings.ReplaceAll(s, "..", ".")
	}
	return strings.Trim(s, ".")
}

// Lookup resolves a lattice test reference (an FQN such as
// "src.cli.AddTodoHandler.execute" or a junit "Class.method") to an
// outcome. Matching is exact-on-normalized first, then by trailing
// segment so a verifier FQN need not be byte-identical to the report's
// classname.name. The bool is false when the test isn't in the set.
//
// When several report cases match, the most specific one wins: the
// longest overlap between the ref and the report key, so a runner path
// relative to a test directory (auth.signin.spec...) still beats a
// same-titled case in another file. Equally specific matches resolve
// to the worst outcome (fail, then skip, then pass), so the answer
// never depends on map order and a failure is never hidden.
func (s Set) Lookup(ref string) (Outcome, bool) {
	if len(s.Results) == 0 {
		return "", false
	}
	key := normalize(ref)
	if o, ok := s.Results[key]; ok {
		return o, true
	}
	if o, ok := s.lookupTemplate(key); ok {
		return o, ok
	}
	last := key
	if i := strings.LastIndex(key, "."); i >= 0 {
		last = key[i+1:]
	}
	best, bestScore, found := Outcome(""), 0, false
	for k, o := range s.Results {
		score := 0
		switch {
		case strings.HasSuffix(key, "."+k):
			score = len(k) // the report key is a dotted tail of the ref
		case strings.HasSuffix(k, "."+key):
			score = len(key) // the ref is a dotted tail of the report key
		case strings.HasSuffix(k, "."+last) || k == last:
			score = len(last) // only the final segment agrees
		default:
			continue
		}
		if !found || score > bestScore || (score == bestScore && worse(o, best)) {
			best, bestScore, found = o, score, true
		}
	}
	return best, found
}

// lookupTemplate resolves a table-driven test (it.each / test.each), whose
// FQN marks each title placeholder with a standalone "_" token
// ("…test.fails-when-the-_-read-fails"). Every report case whose last segment
// fills the placeholders is a row of it; among those, the rows with the
// longest shared path to the ref (the same file) count. The test is
// demonstrated only when every row passed, so the worst outcome wins.
func (s Set) lookupTemplate(key string) (Outcome, bool) {
	lastDot := strings.LastIndex(key, ".")
	last := key[lastDot+1:]
	tokens := strings.Split(last, "-")
	isTemplate := false
	for i, t := range tokens {
		if t == "_" {
			tokens[i] = `[a-z0-9]+(?:-[a-z0-9]+)*`
			isTemplate = true
		} else {
			tokens[i] = regexp.QuoteMeta(t)
		}
	}
	if !isTemplate {
		return "", false
	}
	re := regexp.MustCompile("^" + strings.Join(tokens, "-") + "$")
	refSegs := strings.Split(key, ".")
	best, bestScore, found := Outcome(""), -1, false
	for k, o := range s.Results {
		segs := strings.Split(k, ".")
		if !re.MatchString(segs[len(segs)-1]) {
			continue
		}
		score := 0 // trailing path segments shared before the title
		for i, j := len(segs)-2, len(refSegs)-2; i >= 0 && j >= 0 && segs[i] == refSegs[j]; i, j = i-1, j-1 {
			score++
		}
		if !found || score > bestScore || (score == bestScore && worse(o, best)) {
			best, bestScore, found = o, score, true
		}
	}
	return best, found
}

// worse reports whether a is a worse outcome than b (fail < skip < pass).
func worse(a, b Outcome) bool {
	rank := map[Outcome]int{Fail: 0, Skip: 1, Pass: 2}
	return rank[a] < rank[b]
}

// Ingest parses one or more XML reports into a single Set and writes it to
// the lattice cache, replacing any earlier set. commit stamps which commit
// the run covers. Pass every report of the run together (unit, e2e, ...):
// a verifier with no result in the set cannot count as demonstrated.
func Ingest(latticeDir, commit string, xmlPaths ...string) (Set, error) {
	set := Set{Commit: commit, Results: map[string]Outcome{}}
	for _, p := range xmlPaths {
		data, err := os.ReadFile(p)
		if err != nil {
			return Set{}, err
		}
		one, err := ParseJUnit(data)
		if err != nil {
			return Set{}, fmt.Errorf("%s: %w", p, err)
		}
		for k, o := range one.Results {
			record(set.Results, k, o)
		}
	}
	if err := Save(latticeDir, set); err != nil {
		return set, err
	}
	return set, nil
}

// Save writes the set to lattice/.cache/results/results.json.
func Save(latticeDir string, set Set) error {
	path := filepath.Join(latticeDir, cacheFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Load reads the ingested set, returning an empty (non-nil) Set when no
// results have been ingested — callers treat "no results" as "no
// demonstration evidence", never as an error.
func Load(latticeDir string) Set {
	path := filepath.Join(latticeDir, cacheFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return Set{Results: map[string]Outcome{}}
	}
	var set Set
	if json.Unmarshal(data, &set) != nil || set.Results == nil {
		return Set{Results: map[string]Outcome{}}
	}
	return set
}
