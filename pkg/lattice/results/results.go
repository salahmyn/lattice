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
	"path/filepath"
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
			// A failure anywhere wins over a pass under the same key.
			if prev, ok := out[k]; ok && prev == Fail {
				continue
			}
			out[k] = o
		}
	}
	for _, nested := range s.Suites {
		collect(nested, out)
	}
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
	}
	add(name)
	return keys
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
func (s Set) Lookup(ref string) (Outcome, bool) {
	if len(s.Results) == 0 {
		return "", false
	}
	key := normalize(ref)
	if o, ok := s.Results[key]; ok {
		return o, true
	}
	// Trailing-segment match: the report key ends with the ref's last
	// segment(s), or vice-versa.
	last := key
	if i := strings.LastIndex(key, "."); i >= 0 {
		last = key[i+1:]
	}
	for k, o := range s.Results {
		if strings.HasSuffix(k, "."+last) || k == last || strings.HasSuffix(key, "."+k) {
			return o, true
		}
	}
	return "", false
}

// Ingest parses an XML report and writes the normalized Set to the
// lattice cache. commit stamps which commit the run covers.
func Ingest(latticeDir, xmlPath, commit string) (Set, error) {
	data, err := os.ReadFile(xmlPath)
	if err != nil {
		return Set{}, err
	}
	set, err := ParseJUnit(data)
	if err != nil {
		return Set{}, err
	}
	set.Commit = commit
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
