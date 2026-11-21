package lines

// @module-feature audit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/coverage"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Schema is the version tag of the lines section of the audit report. The
// section is an addition to lattice.audit/1: a report made without --lines
// has none, so the audit's own contract does not change.
const Schema = "lattice.audit-lines/1"

// Levels of an instrumented line, strongest first.
const (
	LevelChecked   = "checked"    // a killed break of the block's intent recorded the line
	LevelRun       = "run"        // an unshared invocation of a verifier group of the intent ran it
	LevelRunShared = "run_shared" // only a shared invocation (it also ran other tests) ran it
	LevelOther     = "other"      // the suite, or a verifier of another intent, ran it
	LevelNotRun    = "not_run"    // instrumented, and nothing ran it
	LevelUnknown   = "unknown"    // no line data
)

// Why a block has no line data.
const (
	WhyNotRun  = "not run with lattice break --lines"
	WhyNoFile  = "no coverage report lists this file"
	WhyPgTAP   = "pgTAP reports calls per function, not lines"
	whyNoLines = "the verifiers' coverage lists no line of this file"
)

// BreakRun is one run of breaks.json, as the lines read it.
type BreakRun struct {
	Promise      string `json:"promise"`
	Enforcer     string `json:"enforcer"`
	EnforcerFile string `json:"enforcer_file"`
	Method       string `json:"method"`
	Outcome      string `json:"outcome"`
	// Lines are the first and last line of EnforcerFile the break edited.
	Lines []int `json:"lines"`
}

// Input is everything Compute joins.
type Input struct {
	KG schema.KnowledgeGraph
	// Edges is the call graph (callgraph.json). Without it no block serves
	// anything: only enforcers have an intent.
	Edges []Edge
	// Verifier is verifier-lines.json; VerifierWhy says why it cannot be
	// read as this tree's (another tree, a dirty working tree), "" when it
	// can. With neither, the verifiers' line data is not run.
	Verifier    *File
	VerifierWhy string
	// Breaks are the runs of breaks.json that are of the graph's commit.
	Breaks []BreakRun
	// BreaksWhy says why breaks.json was set aside, "" when it was not.
	BreaksWhy string
	// Suite is the merged whole-suite coverage, empty when none was read;
	// SuiteIncluded is its include set.
	Suite         coverage.Report
	SuiteIncluded coverage.Included
	// SuiteProblems are the suite reports that could not be read.
	SuiteProblems []string
	// Coverage is the ingested set, for the per-test lines of an enforcer
	// and the function-level evidence of a SQL block.
	Coverage *coverage.Index
	// Source returns a file's text, to tell the lines of a block that has no
	// line data from its blank lines and comments. Optional.
	Source func(file string) ([]byte, bool)
}

// Levels counts the lines of a block, a feature or the workspace by level.
type Levels struct {
	Checked   int `json:"checked"`
	Run       int `json:"run"`
	RunShared int `json:"run_shared"`
	Other     int `json:"other"`
	NotRun    int `json:"not_run"`
	Unknown   int `json:"unknown"`
}

// Known counts the lines that have a level: every one but the unknown.
func (l Levels) Known() int { return l.Checked + l.Run + l.RunShared + l.Other + l.NotRun }

// Verified counts the lines a verifier of the intent ran in its own run.
func (l Levels) Verified() int { return l.Checked + l.Run }

func (l *Levels) add(o Levels) {
	l.Checked += o.Checked
	l.Run += o.Run
	l.RunShared += o.RunShared
	l.Other += o.Other
	l.NotRun += o.NotRun
	l.Unknown += o.Unknown
}

// Ref is one invariant of a block's intent, and whether a BRD criterion
// requires it (maps_to_invariant).
type Ref struct {
	Invariant string `json:"invariant"`
	Required  string `json:"required"` // required | unrequired
}

// Intent is what a block is for.
type Intent struct {
	Kind       string `json:"kind"`
	Invariants []Ref  `json:"invariants,omitempty"`
}

// Block is one function, method, policy or trigger, with the level of each
// of its instrumented lines.
type Block struct {
	FQN     string `json:"fqn"`
	Kind    string `json:"kind"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	EndLine int    `json:"end_line"`
	Feature string `json:"feature,omitempty"`
	Intent  Intent `json:"intent"`
	Levels  Levels `json:"levels"`
	// RunShared, Other and NotRun are the line ranges of those levels
	// ("12-14, 20"), the ones to look at.
	RunShared string `json:"run_shared_lines,omitempty"`
	Other     string `json:"other_lines,omitempty"`
	NotRun    string `json:"not_run_lines,omitempty"`
	// Unknown says why the lines with no data have none.
	Unknown string `json:"unknown,omitempty"`
	// Note is function-level evidence for a SQL block, whose lines stay
	// unknown.
	Note string `json:"note,omitempty"`
}

// Totals are the lines of a set of blocks, split by whether the block has an
// intent.
type Totals struct {
	// WithIntent counts the lines of blocks that enforce, serve or share an
	// intent; NoIntent those of blocks with none.
	WithIntent Levels `json:"with_intent"`
	NoIntent   Levels `json:"no_intent"`
	// Verified is (checked + run) over the known lines of blocks with
	// intent, as a percentage; nil when no such line is known.
	Verified *float64 `json:"verified_pct,omitempty"`
}

func (t *Totals) finish() {
	t.Verified = nil
	if known := t.WithIntent.Known(); known > 0 {
		p := 100 * float64(t.WithIntent.Verified()) / float64(known)
		t.Verified = &p
	}
}

// Feature is one feature's lines.
type Feature struct {
	Feature string `json:"feature"`
	Totals
}

// Outside counts the instrumented lines that fall in no block: top-level
// code, declarations, imports. They never enter a percentage.
type Outside struct {
	Lines int `json:"lines"`
	Hit   int `json:"hit"`
}

// Inputs says which evidence the lines read.
type Inputs struct {
	Verifier string `json:"verifier_lines"`
	Suite    string `json:"suite_coverage"`
	Breaks   string `json:"breaks"`
	Graph    string `json:"call_graph"`
}

// Report is the lines section of the audit: lattice.audit-lines/1.
type Report struct {
	Schema    string    `json:"schema"`
	Inputs    Inputs    `json:"inputs"`
	Workspace Totals    `json:"workspace"`
	Features  []Feature `json:"features"`
	Outside   Outside   `json:"outside"`
	Blocks    []Block   `json:"blocks"`
	// Narrowed says --feature, --promise or --class kept only some blocks:
	// the totals are over those, and outside is not counted.
	Narrowed bool `json:"narrowed,omitempty"`
}

// fileData is one file's coverage in one source.
type fileData struct {
	inst map[int]bool
	hit  map[int]bool
	decl bool
}

func (f *fileData) report(file string) coverage.Report {
	rep := coverage.NewReport("")
	c := rep.File(file)
	c.DeclEvidence = f.decl
	for l := range f.inst {
		n := 0
		if f.hit[l] {
			n = 1
		}
		c.Lines[l] = n
	}
	return rep
}

func fileDataOf(fl FileLines) *fileData {
	d := &fileData{inst: map[int]bool{}, hit: map[int]bool{}, decl: fl.Decl}
	for _, l := range Expand(fl.Instrumented) {
		d.inst[l] = true
	}
	for _, l := range Expand(fl.Hit) {
		d.hit[l] = true
	}
	return d
}

// span is a block's extent in its file.
type span struct{ from, to int }

// Compute joins the call graph, the verifiers' line data, the breaks and the
// suite coverage into one level per instrumented line of every block.
//
// A line is reached by an invariant's verifiers only through the coverage
// of an invocation that ran those verifiers (verifier-lines.json) or a
// verifier's own per-test run; the suite, or another invariant's
// verifiers, make it other, and a line with no data at all is unknown,
// never run.
// @enforces audit:INV-10
func Compute(in Input) Report {
	kg := in.KG
	rep := Report{Schema: Schema, Features: []Feature{}, Blocks: []Block{}}
	rep.Inputs = inputsOf(in)

	test := map[string]bool{}
	for _, t := range kg.Tests {
		test[t.File] = true
	}
	var syms []schema.GraphSymbol
	isBlock := map[string]bool{}
	byFile := map[string][]span{}
	for _, s := range kg.Symbols {
		if s.IsTest || test[s.File] || s.EndLine <= 0 || s.File == "" {
			continue
		}
		if !blockKinds[s.Kind] && len(s.EnforcesInvariants) == 0 {
			continue
		}
		syms = append(syms, s)
		isBlock[s.FQN] = true
		byFile[s.File] = append(byFile[s.File], span{s.Line, s.EndLine})
	}
	nodes := intents(kg, isBlock, in.Edges)

	required := map[string]bool{}
	for _, b := range kg.BRDs {
		for _, sc := range b.SuccessCriteria {
			for _, r := range sc.MapsToInvariant {
				required[r] = true
			}
		}
	}
	verifiers := map[string][]string{} // promise -> verifier FQNs
	for _, t := range kg.Tests {
		for _, v := range t.Verifies {
			p := ref(t.Feature, v)
			verifiers[p] = append(verifiers[p], t.FQN)
		}
	}
	killed := map[string][]BreakRun{} // promise -> killed runs with lines
	for _, r := range in.Breaks {
		if r.Outcome == "killed" && len(r.Lines) == 2 && r.Method != "edge" {
			killed[r.Promise] = append(killed[r.Promise], r)
		}
	}

	sort.SliceStable(syms, func(i, j int) bool {
		a, b := syms[i], syms[j]
		if a.Feature != b.Feature {
			return a.Feature < b.Feature
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.FQN < b.FQN
	})

	suiteFiles := in.Suite.Files
	c := &ctx{in: in, byFile: byFile, verifiers: verifiers, killed: killed, cache: map[string]*fileData{}}
	feat := map[string]*Feature{}
	for _, s := range syms {
		n := nodes[s.FQN]
		b := Block{FQN: s.FQN, Kind: s.Kind, File: s.File, Line: s.Line, EndLine: s.EndLine, Feature: s.Feature}
		b.Intent.Kind = n.kind()
		for _, r := range n.refs() {
			req := "unrequired"
			if required[r] {
				req = "required"
			}
			b.Intent.Invariants = append(b.Intent.Invariants, Ref{Invariant: r, Required: req})
		}
		if s.Line > 0 {
			c.level(&b, s, n)
		}
		rep.Blocks = append(rep.Blocks, b)

		key := b.Feature
		f := feat[key]
		if f == nil {
			f = &Feature{Feature: key}
			feat[key] = f
		}
		if b.Intent.Kind == IntentNone {
			f.NoIntent.add(b.Levels)
			rep.Workspace.NoIntent.add(b.Levels)
		} else {
			f.WithIntent.add(b.Levels)
			rep.Workspace.WithIntent.add(b.Levels)
		}
	}
	rep.Workspace.finish()
	names := make([]string, 0, len(feat))
	for k := range feat {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		f := feat[k]
		f.finish()
		rep.Features = append(rep.Features, *f)
	}
	rep.Outside = c.outside(suiteFiles, SourceFiles(kg))
	return rep
}

// ctx carries what the per-block join shares.
type ctx struct {
	in        Input
	byFile    map[string][]span
	verifiers map[string][]string
	killed    map[string][]BreakRun
	cache     map[string]*fileData
}

func inputsOf(in Input) Inputs {
	var out Inputs
	switch {
	case in.Verifier != nil && in.VerifierWhy == "":
		out.Verifier = fmt.Sprintf("tree %s, %d promises", short(in.Verifier.Tree), len(in.Verifier.Promises))
	case in.VerifierWhy != "":
		out.Verifier = "ignored: " + in.VerifierWhy
	default:
		out.Verifier = "none (lattice break --lines)"
	}
	switch {
	case len(in.Suite.Files) > 0:
		out.Suite = fmt.Sprintf("%d files", len(in.Suite.Files))
	default:
		out.Suite = "none"
	}
	if len(in.SuiteProblems) > 0 {
		out.Suite += " (unreadable: " + strings.Join(in.SuiteProblems, "; ") + ")"
	}
	killed := 0
	for _, r := range in.Breaks {
		if r.Outcome == "killed" && len(r.Lines) == 2 {
			killed++
		}
	}
	out.Breaks = fmt.Sprintf("%d killed with lines", killed)
	if in.BreaksWhy != "" {
		out.Breaks = "ignored: " + in.BreaksWhy
	}
	if len(in.Edges) > 0 {
		out.Graph = fmt.Sprintf("%d edges", len(in.Edges))
	} else {
		out.Graph = "none: only enforcers have an intent"
	}
	return out
}

func short(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// owned returns the lines of a block's body, minus the lines of blocks
// nested inside it, which belong to those.
func (c *ctx) owned(s schema.GraphSymbol, lines []int) []int {
	var out []int
outer:
	for _, l := range lines {
		for _, sp := range c.byFile[s.File] {
			inner := sp.from >= s.Line && sp.to <= s.EndLine && (sp.from != s.Line || sp.to != s.EndLine)
			if inner && l > sp.from && l <= sp.to {
				continue outer
			}
		}
		out = append(out, l)
	}
	return out
}

// level fills a block's levels, line ranges and reason.
func (c *ctx) level(b *Block, s schema.GraphSymbol, n *node) {
	refs := n.refs()
	mine := map[string]bool{}
	for _, r := range refs {
		mine[r] = true
	}
	inst, hitRun, hitShared, hitOther := map[int]bool{}, map[int]bool{}, map[int]bool{}, map[int]bool{}
	take := func(f *fileData, into map[int]bool) (known bool) {
		if f == nil {
			return false
		}
		cov := coverage.Of(f.report(s.File), s, nil)
		if !cov.Known {
			return false
		}
		for _, l := range c.owned(s, cov.BodyLines()) {
			inst[l] = true
		}
		if into != nil {
			for _, l := range c.owned(s, cov.HitLines()) {
				into[l] = true
			}
		}
		return true
	}

	var whys []string
	sql := isSQL(s)
	verifierData := false // some group of the block's intent has data for this file
	verifierSeen := map[string]bool{}
	if !sql && c.in.Verifier != nil && c.in.VerifierWhy == "" {
		// The block's own intent first: its groups' invocations make it
		// run or run_shared.
		for _, p := range refs {
			for _, g := range c.in.Verifier.Promises[p] {
				if g.Unknown != "" {
					whys = append(whys, g.Unknown)
					continue
				}
				inv, ok := c.in.Verifier.Invocations[g.Key]
				if !ok || g.Key == "" {
					whys = append(whys, "no line data was kept for "+p)
					continue
				}
				verifierSeen[g.Key] = true
				if inv.Unknown != "" {
					whys = append(whys, inv.Unknown)
					continue
				}
				fl, ok := inv.Files[s.File]
				if !ok {
					continue
				}
				into := hitRun
				if g.Shared {
					into = hitShared
				}
				if take(c.file(g.Key, s.File, fl), into) {
					verifierData = true
				}
			}
		}
		// Every other invocation only shows that something ran the line.
		var promises []string
		for p := range c.in.Verifier.Promises {
			promises = append(promises, p)
		}
		sort.Strings(promises)
		for _, p := range promises {
			for _, g := range c.in.Verifier.Promises[p] {
				if g.Key == "" || g.Unknown != "" || verifierSeen[g.Key] {
					continue
				}
				verifierSeen[g.Key] = true
				inv, ok := c.in.Verifier.Invocations[g.Key]
				if !ok || inv.Unknown != "" {
					continue
				}
				if fl, ok := inv.Files[s.File]; ok {
					take(c.file(g.Key, s.File, fl), hitOther)
				}
			}
		}
	} else if !sql && len(refs) > 0 {
		if c.in.VerifierWhy != "" {
			whys = append(whys, c.in.VerifierWhy)
		} else {
			whys = append(whys, WhyNotRun)
		}
	}

	// A verifier's own per-test run of an enforcer is an unshared run too.
	if n != nil && n.isEnf && c.in.Coverage != nil && !sql {
		for _, p := range n.enforces {
			vs := c.verifiers[p]
			if len(vs) == 0 || !c.allPerTest(vs) {
				continue
			}
			for _, v := range vs {
				ls, has := c.in.Coverage.Set.EnforcerLines[v][s.FQN]
				if !has {
					continue
				}
				for _, l := range c.owned(s, filterRange(ls.Body, s)) {
					inst[l] = true
				}
				for _, l := range c.owned(s, filterRange(ls.Hit, s)) {
					hitRun[l] = true
				}
				verifierData = true
			}
		}
	}

	if !sql && len(c.in.Suite.Files) > 0 {
		cov := coverage.Of(c.in.Suite, s, c.in.SuiteIncluded)
		if cov.Known {
			for _, l := range c.owned(s, cov.BodyLines()) {
				inst[l] = true
			}
			for _, l := range c.owned(s, cov.HitLines()) {
				hitOther[l] = true
			}
		}
	}

	// Lines a killed break of the block's intent edited. A line the
	// verifiers' own data shows none of them ran cannot be what the break
	// was noticed by, so it is not checked.
	checked := map[int]bool{}
	for _, p := range refs {
		for _, r := range c.killed[p] {
			if r.EnforcerFile != s.File {
				continue
			}
			for l := r.Lines[0]; l <= r.Lines[1]; l++ {
				if l > s.Line && l <= s.EndLine {
					checked[l] = true
				}
			}
		}
	}
	checkedLines := c.owned(s, keysOf(checked))
	dataless := len(inst) == 0
	if !dataless {
		for _, l := range checkedLines {
			if !inst[l] {
				delete(checked, l)
			} else if verifierData && !hitRun[l] && !hitShared[l] {
				delete(checked, l)
			}
		}
	} else {
		code := c.codeLines(s)
		for _, l := range checkedLines {
			if code != nil && !code[l] {
				delete(checked, l)
				continue
			}
			inst[l] = true
		}
	}

	var shared, other, notRun []int
	for l := range inst {
		switch {
		case checked[l]:
			b.Levels.Checked++
		case hitRun[l]:
			b.Levels.Run++
		case hitShared[l]:
			b.Levels.RunShared++
			shared = append(shared, l)
		case hitOther[l]:
			b.Levels.Other++
			other = append(other, l)
		default:
			b.Levels.NotRun++
			notRun = append(notRun, l)
		}
	}
	b.RunShared, b.Other, b.NotRun = Compact(shared), Compact(other), Compact(notRun)

	if dataless {
		// No report lists this block: its lines are unknown, except the ones
		// a killed break edited.
		code := c.codeLines(s)
		total := len(code)
		if code == nil {
			total = s.EndLine - s.Line
		}
		if b.Levels.Unknown = total - b.Levels.Checked; b.Levels.Unknown < 0 {
			b.Levels.Unknown = 0
		}
		if b.Levels.Unknown == 0 {
			return
		}
		switch {
		case sql:
			b.Unknown = WhyPgTAP
			b.Note = c.functionLevel(s, refs)
		case len(whys) > 0:
			b.Unknown = whys[0]
		case len(refs) > 0 && c.in.Verifier != nil && verifierData:
			b.Unknown = whyNoLines
		default:
			b.Unknown = WhyNoFile
		}
	}
}

// file builds the coverage of one file in one invocation once.
func (c *ctx) file(key, file string, fl FileLines) *fileData {
	k := key + "\x00" + file
	if d, ok := c.cache[k]; ok {
		return d
	}
	d := fileDataOf(fl)
	c.cache[k] = d
	return d
}

func (c *ctx) allPerTest(tests []string) bool {
	for _, t := range tests {
		if _, ok := c.in.Coverage.Set.PerTest[t]; !ok {
			return false
		}
	}
	return true
}

// functionLevel says, for a SQL block, whether a verifier of its intent is
// known to have called it: pgTAP and pg_stat_user_functions count calls per
// function. The lines stay unknown.
func (c *ctx) functionLevel(s schema.GraphSymbol, refs []string) string {
	if c.in.Coverage == nil || len(refs) == 0 {
		return ""
	}
	ran, seen := false, false
	for _, p := range refs {
		for _, v := range c.verifiers[p] {
			ok, basis := c.in.Coverage.TestRan(v, s.FQN)
			if basis != coverage.BasisPerTest {
				continue
			}
			seen = true
			ran = ran || ok
		}
	}
	switch {
	case ran:
		return "function level: a verifier of its intent called it (pg_stat_user_functions); lines unknown"
	case seen:
		return "function level: no verifier of its intent called it (pg_stat_user_functions); lines unknown"
	}
	return ""
}

// codeLines are the lines of a block's body that are code: not blank and
// not a comment, by the comment markers of the languages Lattice reads. nil
// when the source is not available.
func (c *ctx) codeLines(s schema.GraphSymbol) map[int]bool {
	if c.in.Source == nil {
		return nil
	}
	data, ok := c.in.Source(s.File)
	if !ok {
		return nil
	}
	out := map[int]bool{}
	for i, text := range strings.Split(string(data), "\n") {
		l := i + 1
		if l <= s.Line || l > s.EndLine {
			continue
		}
		t := strings.TrimSpace(text)
		if t == "" || strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "--") ||
			strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "*") {
			continue
		}
		out[l] = true
	}
	return out
}

// outside counts the instrumented lines of workspace files that no block
// holds, over the suite and every verifier invocation.
func (c *ctx) outside(suite map[string]*coverage.FileCov, src map[string]bool) Outside {
	type lineSet struct{ inst, hit map[int]bool }
	files := map[string]*lineSet{}
	get := func(f string) *lineSet {
		ls := files[f]
		if ls == nil {
			ls = &lineSet{inst: map[int]bool{}, hit: map[int]bool{}}
			files[f] = ls
		}
		return ls
	}
	for f, fc := range suite {
		if !src[f] {
			continue
		}
		ls := get(f)
		for l, n := range fc.Lines {
			ls.inst[l] = true
			if n > 0 {
				ls.hit[l] = true
			}
		}
	}
	if v := c.in.Verifier; v != nil && c.in.VerifierWhy == "" {
		for _, inv := range v.Invocations {
			if inv.Unknown != "" {
				continue
			}
			for f, fl := range inv.Files {
				if !src[f] {
					continue
				}
				d := fileDataOf(fl)
				ls := get(f)
				for l := range d.inst {
					ls.inst[l] = true
				}
				for l := range d.hit {
					ls.hit[l] = true
				}
			}
		}
	}
	var out Outside
	for f, ls := range files {
		for l := range ls.inst {
			inside := false
			for _, sp := range c.byFile[f] {
				if l >= sp.from && l <= sp.to {
					inside = true
					break
				}
			}
			if inside {
				continue
			}
			out.Lines++
			if ls.hit[l] {
				out.Hit++
			}
		}
	}
	return out
}

func filterRange(lines []int, s schema.GraphSymbol) []int {
	var out []int
	for _, l := range lines {
		if l > s.Line && l <= s.EndLine {
			out = append(out, l)
		}
	}
	return out
}

func keysOf(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for l := range m {
		out = append(out, l)
	}
	sort.Ints(out)
	return out
}
