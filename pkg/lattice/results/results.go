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

// @module-feature results

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
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
	Commit string `json:"commit,omitempty"`
	// CIRun is the CI run that produced the reports (CIRunID at ingest), or
	// "local". `lattice check --ci` refuses a set whose run is local.
	CIRun   string             `json:"ci_run,omitempty"`
	Results map[string]Outcome `json:"results"`
	// Cases maps each key to the report case it came from
	// ("<classname>::<name>"). One case is indexed under several keys;
	// Cases tells "one case seen through two keys" from "two different
	// cases", which is what makes a lookup ambiguous.
	Cases map[string]string `json:"cases,omitempty"`
	// tests are the graph's tests, bound by WithTests, so a parameterized
	// test's lookup knows which report cases other tests claim.
	tests *testIndex
}

// testIndex is the graph's tests as Lookup compares them: plain tests by the
// slug of their title, parameterized tests with their title pattern.
type testIndex struct {
	plain     map[string][][]string // last FQN segment -> FQN segments
	templates []templateTest
}

type templateTest struct {
	key  string
	segs []string
	re   *regexp.Regexp
}

// WithTests binds the set to the graph's tests. Without it, a parameterized
// test takes every report case its title pattern matches; with it, a case a
// plain test names exactly stays that test's, and a case two parameterized
// tests match equally makes both ambiguous.
func (s Set) WithTests(tests []schema.GraphSymbol) Set {
	idx := &testIndex{plain: map[string][][]string{}}
	for _, t := range tests {
		key := normalize(t.FQN)
		segs := strings.Split(key, ".")
		if re, ok := templatePattern(segs[len(segs)-1]); ok {
			idx.templates = append(idx.templates, templateTest{key: key, segs: segs, re: re})
			continue
		}
		last := segs[len(segs)-1]
		idx.plain[last] = append(idx.plain[last], segs)
	}
	s.tests = idx
	return s
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
	// Cases directly under the root: Node's built-in runner
	// (node --test --test-reporter=junit) writes no <testsuite> for
	// top-level test() calls.
	Cases []junitCase `xml:"testcase"`
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
// Set. A top-level <testsuite> (no <testsuites> wrapper) is accepted too,
// and so are <testcase> elements directly under <testsuites>. A report with
// no cases at all (a runner whose project has no tests writes an empty
// <testsuites/>) is an empty set, not an error.
// @enforces results:INV-7
func ParseJUnit(data []byte) (Set, error) {
	set := Set{Results: map[string]Outcome{}, Cases: map[string]string{}}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // a UTF-8 byte-order mark

	switch root, err := junitRoot(data); {
	case err != nil:
		return set, fmt.Errorf("parse junit xml: %w", err)
	case root == "testsuites":
		var suites junitSuites
		if err := xml.Unmarshal(data, &suites); err != nil {
			return set, fmt.Errorf("parse junit xml: %w", err)
		}
		for _, s := range suites.Suites {
			collect(s, set.Results, set.Cases)
		}
		collect(junitSuite{Cases: suites.Cases}, set.Results, set.Cases)
		return set, nil
	case root == "testsuite":
		var single junitSuite
		if err := xml.Unmarshal(data, &single); err != nil {
			return set, fmt.Errorf("parse junit xml: %w", err)
		}
		collect(single, set.Results, set.Cases)
		return set, nil
	default:
		return set, fmt.Errorf("parse junit xml: root is <%s>, want <testsuites> or <testsuite>", root)
	}
}

// junitRoot returns the local name of the report's root element, skipping
// the XML declaration, comments, processing instructions and whitespace.
func junitRoot(data []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return "", fmt.Errorf("no root element")
		}
		if err != nil {
			return "", err
		}
		if el, ok := tok.(xml.StartElement); ok {
			return el.Name.Local, nil
		}
	}
}

// collect walks a suite (and nested suites) recording one Outcome per
// case under every key normalize emits.
func collect(s junitSuite, out map[string]Outcome, cases map[string]string) {
	for _, c := range s.Cases {
		o := Pass
		switch {
		case c.Failure != nil || c.Error != nil:
			o = Fail
		case c.Skipped != nil:
			o = Skip
		}
		id := strings.TrimSpace(c.Classname) + "::" + strings.TrimSpace(c.Name)
		for _, k := range keysFor(c.Classname, c.Name) {
			record(out, k, o)
			if prev, ok := cases[k]; ok && prev != id {
				// Two cases share this key (the same test in two projects or
				// files): keep the first id; the outcome already merged.
				continue
			}
			cases[k] = id
		}
	}
	for _, nested := range s.Suites {
		collect(nested, out, cases)
	}
}

// record stores an outcome under a key. A failure anywhere wins over a pass
// under the same key, across cases, projects and reports.
// @enforces results:INV-1
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
	jsKey := ""
	if classname != "" {
		add(classname + "." + name)
		if jsKey = jsTestKey(classname, name); jsKey != "" {
			add(jsKey)
		}
	}
	add(name)
	// A runner that reports a test by its title but not its file (Node's
	// built-in runner writes classname "test") still joins the TypeScript
	// adapter's "<module>.test.<slug>" FQN by its tail. Only a title with a
	// space gets this key: an identifier-style name (TestX, test_x) is never
	// a test() title.
	if jsKey == "" && strings.Contains(name, " ") {
		add("test." + slugify(titleLeaf(name)))
	}
	return keys
}

// titleLeaf returns the last part of a runner's title path: "describe >
// ... > it description" (vitest/jest) or "describe › ... › title"
// (Playwright) gives the it() or test() text.
func titleLeaf(name string) string {
	for _, sep := range []string{" > ", " › "} {
		if i := strings.LastIndex(name, sep); i >= 0 {
			name = name[i+len(sep):]
		}
	}
	return name
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
	case ".sql", ".pg": // pgTAP: the postgres adapter keys assertions the same way
	default:
		return ""
	}
	return strings.TrimSuffix(classname, ext) + ".test." + slugify(titleLeaf(name))
}

// Slug is the test-name slug Lattice joins reports by: lowercase ASCII
// letters and digits, every other run collapsed to one dash. A per-test
// coverage report is named by its test's slug.
func Slug(s string) string { return slugify(s) }

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
// @enforces results:INV-2
func (s Set) Lookup(ref string) (Outcome, bool) {
	o, found, ambiguous := s.lookup(ref)
	if len(ambiguous) > 0 {
		return "", false
	}
	return o, found
}

// Ambiguous returns the distinct report cases a ref matches equally well
// when they are more than one, else nil. Lookup refuses such a ref (it is
// not demonstrated) rather than guess which case is its verifier.
func (s Set) Ambiguous(ref string) []string {
	_, _, ambiguous := s.lookup(ref)
	return ambiguous
}

// @enforces results:INV-2, results:INV-3
func (s Set) lookup(ref string) (Outcome, bool, []string) {
	if len(s.Results) == 0 {
		return "", false, nil
	}
	key := normalize(ref)
	if o, ok := s.Results[key]; ok {
		return o, true, nil
	}
	if o, ok, isTemplate, ambiguous := s.lookupTemplate(key); isTemplate {
		return o, ok, ambiguous
	}
	last := key
	if i := strings.LastIndex(key, "."); i >= 0 {
		last = key[i+1:]
	}
	best, bestScore, found := Outcome(""), 0, false
	tied := map[string]bool{} // case ids at the best score
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
		if !found || score > bestScore {
			best, bestScore, found = o, score, true
			tied = map[string]bool{}
		} else if score == bestScore && worse(o, best) {
			best = o
		}
		if score == bestScore {
			id := s.Cases[k]
			if id == "" {
				id = k // a set ingested before case ids were recorded
			}
			tied[id] = true
		}
	}
	if len(tied) > 1 && len(s.Cases) > 0 {
		ids := make([]string, 0, len(tied))
		for id := range tied {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return "", false, ids
	}
	return best, found, nil
}

// slugRun is what a title placeholder matches in a slug: any non-empty run
// of slug words, as few as will do.
const slugRun = `[a-z0-9]+?(?:-[a-z0-9]+)*?`

// dupSuffix is the _2, _3, ... the TypeScript adapter appends to the second
// and later test of a file with the same slug.
var dupSuffix = regexp.MustCompile(`^(.+)_[0-9]+$`)

// templatePattern turns the last FQN segment of a parameterized test
// ("rounds-_-to-_", a title template whose placeholders are "_" tokens) into
// an anchored pattern over a report case's slug: each placeholder matches
// any non-empty text, the literal words match as themselves. A duplicate
// suffix is dropped, so two call sites with one template share a pattern.
// ok is false for a plain test's segment.
func templatePattern(last string) (*regexp.Regexp, bool) {
	if m := dupSuffix.FindStringSubmatch(last); m != nil {
		last = m[1]
	}
	tokens := strings.Split(last, "-")
	isTemplate := false
	for i, t := range tokens {
		if t == "_" {
			tokens[i] = slugRun
			isTemplate = true
		} else {
			tokens[i] = regexp.QuoteMeta(t)
		}
	}
	if !isTemplate {
		return nil, false
	}
	return regexp.MustCompile("^" + strings.Join(tokens, "-") + "$"), true
}

// sharedPath counts the trailing path segments two dotted keys share before
// their last segment (the title): how sure a report case is to be in the
// test's file.
func sharedPath(a, b []string) int {
	n := 0
	for i, j := len(a)-2, len(b)-2; i >= 0 && j >= 0 && a[i] == b[j]; i, j = i-1, j-1 {
		n++
	}
	return n
}

// lookupTemplate resolves a parameterized test (it.each / test.each, a
// template-literal title, a test in a loop), whose FQN marks each title
// placeholder with a standalone "_" token ("…test.fails-when-the-_-read-fails").
// Every report case whose last segment fills the placeholders is an expansion
// of it; among those, the ones with the longest shared path to the ref (the
// same file) count. The test passes only when every expansion passed, so the
// worst outcome wins. isTemplate is false when key is a plain test.
//
// With the graph's tests bound (WithTests), an expansion that a plain test
// of the same file names exactly is that test's, not this one's, and an
// expansion that another parameterized test matches as closely is
// ambiguous: the ref then resolves to no outcome and the cases are returned.
// @enforces results:INV-4, results:INV-8
func (s Set) lookupTemplate(key string) (o Outcome, found, isTemplate bool, ambiguous []string) {
	refSegs := strings.Split(key, ".")
	re, ok := templatePattern(refSegs[len(refSegs)-1])
	if !ok {
		return "", false, false, nil
	}
	type expansion struct {
		key  string
		segs []string
	}
	bestScore := -1
	var rows []expansion
	for k := range s.Results {
		segs := strings.Split(k, ".")
		if !re.MatchString(segs[len(segs)-1]) {
			continue
		}
		switch score := sharedPath(segs, refSegs); {
		case score > bestScore:
			bestScore, rows = score, []expansion{{k, segs}}
		case score == bestScore:
			rows = append(rows, expansion{k, segs})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].key < rows[j].key })
	tied := map[string]bool{}
	best := Outcome("")
	for _, r := range rows {
		if s.tests != nil && s.claimedElsewhere(key, r.segs, bestScore, tied, s.caseID(r.key)) {
			continue
		}
		if !found || worse(s.Results[r.key], best) {
			best, found = s.Results[r.key], true
		}
	}
	if len(tied) > 0 {
		ids := make([]string, 0, len(tied))
		for id := range tied {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return "", false, true, ids
	}
	return best, found, true, nil
}

// claimedElsewhere reports whether a report case (its key's segments) that
// the parameterized test key matches at score belongs to another test: a
// plain test whose title is the case's and whose path agrees at least as
// well, or a parameterized test that matches it on a longer path. A
// parameterized test that matches it on the same path makes it ambiguous: the
// case id goes into tied.
// @enforces results:INV-8
func (s Set) claimedElsewhere(key string, segs []string, score int, tied map[string]bool, id string) bool {
	title := segs[len(segs)-1]
	for _, plain := range s.tests.plain[title] {
		if sharedPath(segs, plain) >= score {
			return true
		}
	}
	claimed := false
	for _, t := range s.tests.templates {
		if t.key == key || !t.re.MatchString(title) {
			continue
		}
		switch other := sharedPath(segs, t.segs); {
		case other > score:
			claimed = true
		case other == score:
			tied[id] = true
		}
	}
	return claimed
}

// caseID is the report case a key came from, the key itself in a set
// ingested before case ids were recorded.
func (s Set) caseID(k string) string {
	if id := s.Cases[k]; id != "" {
		return id
	}
	return k
}

// Tally counts the report cases in the set by outcome. One case is indexed
// under several keys (see keysFor), so counting keys overstates a run; each
// case is counted once, by its fully-qualified key, whose outcome is the
// worst the case had across reports. A set saved before case ids were
// recorded can only count its keys.
func (s Set) Tally() (cases, pass, fail, skip int) {
	count := func(o Outcome) {
		cases++
		switch o {
		case Pass:
			pass++
		case Fail:
			fail++
		case Skip:
			skip++
		}
	}
	if len(s.Cases) == 0 {
		for _, o := range s.Results {
			count(o)
		}
		return
	}
	seen := map[string]bool{}
	for _, id := range s.Cases {
		if !seen[id] {
			seen[id] = true
			count(s.Results[normalize(id)])
		}
	}
	return
}

// FailedCases lists the report cases that failed, as "<classname>::<name>"
// ids (just the name when the case has no class), sorted.
func (s Set) FailedCases() []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range s.Cases {
		if seen[id] || s.Results[normalize(id)] != Fail {
			continue
		}
		seen[id] = true
		out = append(out, strings.TrimPrefix(id, "::"))
	}
	sort.Strings(out)
	return out
}

// Failure is one failed report case and the @verifies refs of the graph
// tests whose result it is.
type Failure struct {
	Case     string   `json:"case"`
	Verifies []string `json:"verifies,omitempty"`
}

// Failures lists the failed report cases, as FailedCases names them, each
// with the @verifies refs of the graph tests it is the result of: a test
// that matches the case and reads as failed in the set. A case no tagged
// test matches has no refs. Bind the set to the graph (WithTests) first, so
// a parameterized test's lookup is the one validation uses.
func (s Set) Failures(tests []schema.GraphSymbol) []Failure {
	cases := s.FailedCases()
	out := make([]Failure, 0, len(cases))
	red := map[string]bool{} // test FQN -> failed in s, looked up once
	seen := map[string]bool{}
	for _, id := range cases {
		f := Failure{Case: id}
		one := FailedSet([]string{id})
		for _, t := range tests {
			if len(t.Verifies) == 0 {
				continue
			}
			if o, ok := one.Lookup(t.FQN); !ok || o != Fail {
				continue
			}
			if !seen[t.FQN] {
				seen[t.FQN] = true
				o, ok := s.Lookup(t.FQN)
				red[t.FQN] = ok && o == Fail
			}
			if !red[t.FQN] {
				continue
			}
			for _, ref := range verifierRefs(t) {
				f.Verifies = appendRef(f.Verifies, ref)
			}
		}
		sort.Strings(f.Verifies)
		out = append(out, f)
	}
	return out
}

// verifierRefs are a test's @verifies refs as written, qualified by the
// test's feature when bare: a case ref ("f:INV-1#a") stands in for the
// invariant ref it adds to Verifies.
func verifierRefs(t schema.GraphSymbol) []string {
	qualify := func(ref string) string {
		if !strings.Contains(ref, ":") && t.Feature != "" {
			return t.Feature + ":" + ref
		}
		return ref
	}
	var out []string
	byCase := map[string]bool{}
	for _, ref := range t.VerifiesCases {
		ref = qualify(ref)
		base, _, _ := schema.SplitCaseRef(ref)
		byCase[base] = true
		out = append(out, ref)
	}
	for _, ref := range t.Verifies {
		if ref = qualify(ref); !byCase[ref] {
			out = append(out, ref)
		}
	}
	return out
}

func appendRef(list []string, ref string) []string {
	for _, x := range list {
		if x == ref {
			return list
		}
	}
	return append(list, ref)
}

// FailedSet is a set in which every named test failed, for names as
// FailedCases or `go test -v` report them ("<classname>::<name>", or a bare
// name). Its Lookup says whether a test is among them, with the matching
// rules of an ingested set.
func FailedSet(names []string) Set {
	s := Set{Results: map[string]Outcome{}, Cases: map[string]string{}}
	for _, id := range names {
		classname, name := "", id
		if i := strings.Index(id, "::"); i >= 0 {
			classname, name = id[:i], id[i+2:]
		}
		for _, k := range keysFor(classname, name) {
			record(s.Results, k, Fail)
			if _, ok := s.Cases[k]; !ok {
				s.Cases[k] = id
			}
		}
	}
	return s
}

// worse reports whether a is a worse outcome than b (fail < skip < pass).
func worse(a, b Outcome) bool {
	rank := map[Outcome]int{Fail: 0, Skip: 1, Pass: 2}
	return rank[a] < rank[b]
}

// Ingest parses one or more reports (JUnit XML or a TAP stream) into a single
// Set and writes it to the lattice cache, replacing any earlier set. commit
// stamps which commit the run covers. Pass every report of the run together
// (unit, e2e, database, ...): a verifier with no result in the set cannot
// count as demonstrated.
func Ingest(latticeDir, commit string, paths ...string) (Set, error) {
	set := Set{Commit: commit, CIRun: CIRunID(), Results: map[string]Outcome{}, Cases: map[string]string{}}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return Set{}, err
		}
		parse := ParseJUnit
		if isTAP(p, data) {
			parse = ParseTAP
		}
		one, err := parse(data)
		if err != nil {
			return Set{}, fmt.Errorf("%s: %w", p, err)
		}
		for k, o := range one.Results {
			record(set.Results, k, o)
			if _, ok := set.Cases[k]; !ok {
				set.Cases[k] = one.Cases[k]
			}
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

// isTAP reports whether a report is a TAP stream rather than JUnit XML: a
// .tap file, or content that does not start with '<'.
func isTAP(p string, data []byte) bool {
	if strings.EqualFold(path.Ext(p), ".tap") {
		return true
	}
	trimmed := strings.TrimSpace(string(data))
	return trimmed != "" && trimmed[0] != '<'
}

var (
	// pg_prove --verbose prints "<file> .." before each file's TAP.
	reTAPFile = regexp.MustCompile(`^(\S+\.(?:sql|pg))\s+\.\.`)
	// TAP 14 names a nested stream "# Subtest: <name>".
	reTAPSubtest   = regexp.MustCompile(`^\s*#\s*Subtest:\s*(.+?)\s*$`)
	reTAPResult    = regexp.MustCompile(`^\s*(not\s+)?ok\b\s*(?:\d+)?\s*(?:-\s*)?(.*)$`)
	reTAPDirective = regexp.MustCompile(`(?i)\s#\s*(SKIP|TODO)\b.*$`)
)

// ParseTAP reads a TAP stream, such as pgTAP output from pg_prove --verbose or
// psql. Each "ok N - description" line is a case; the file comes from
// pg_prove's "<file> .." header or a TAP 14 "# Subtest: <file>". A SKIP or
// TODO directive counts as a skip, so a known-broken test never reads as a
// pass. Lines with no description cannot be joined and are left out.
func ParseTAP(data []byte) (Set, error) {
	set := Set{Results: map[string]Outcome{}, Cases: map[string]string{}}
	file, seen := "", 0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if m := reTAPFile.FindStringSubmatch(line); m != nil {
			file = m[1]
			continue
		}
		if m := reTAPSubtest.FindStringSubmatch(line); m != nil {
			if ext := path.Ext(m[1]); ext == ".sql" || ext == ".pg" {
				file = m[1]
			}
			continue
		}
		m := reTAPResult.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		o := Pass
		if m[1] != "" {
			o = Fail
		}
		desc := m[2]
		if d := reTAPDirective.FindString(desc); d != "" {
			o = Skip
			desc = strings.TrimSuffix(desc, d)
		}
		desc = strings.TrimSpace(strings.ReplaceAll(desc, `\#`, "#"))
		if desc == "" || path.Ext(desc) == ".sql" { // a file's summary line
			continue
		}
		seen++
		id := file + "::" + desc
		keys := keysFor(file, desc)
		if file == "" {
			keys = append(keys, normalize("test."+slugify(desc)))
		}
		for _, k := range keys {
			record(set.Results, k, o)
			if _, ok := set.Cases[k]; !ok {
				set.Cases[k] = id
			}
		}
	}
	if seen == 0 {
		return set, fmt.Errorf("parse tap: no test lines with a description")
	}
	return set, nil
}
