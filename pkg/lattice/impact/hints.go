package impact

// @module-feature impact

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Hints: the two weakest sources of the test listing. They are used only
// when coverage, the call graph and the neighbour heuristic find no test for
// a touched symbol no promise guards, and only test files that hold a test
// the adapters detected (see testCases) count: a support file of helpers
// alone is never hinted.
const (
	// ViaCoChange: the test file changed in the same commits as the
	// symbol's file (a hint: files that change together tend to belong
	// together).
	ViaCoChange = "co_change"
	// ViaMentions: the test file's text names the symbol, or a route its
	// file declares (a hint: a name is not a call).
	ViaMentions = "mentions"
)

// HintedSymbol is a touched production symbol no promise guards whose tests
// only a hint found. The tests are selected and the symbol is listed here,
// so a reader sees how weak the link is.
type HintedSymbol struct {
	Symbol string `json:"symbol"`
	File   string `json:"file"`
	Lines  string `json:"lines,omitempty"`
	Via    string `json:"via"` // co_change | mentions
	// Files are the test files of Tests, sorted: what a runner takes.
	Files []string `json:"files"`
	// Tests are test cases only, never a helper of a test file.
	Tests []string `json:"tests"`
}

// thisChange is how the change being reported is named among the commits a
// test file shared with a touched file.
const thisChange = "this change"

// maxShownCommits caps the commits a co_change why lists by name.
const maxShownCommits = 5

// scoreEpsilon absorbs float rounding when a co-change score is compared
// with break.co_change.min_score, so three shared commits of twelve files
// each still reach 0.25.
const scoreEpsilon = 1e-9

// minMentionLen is the shortest name the mention hint looks for.
const minMentionLen = 4

// commonNames are compound names too common in test code to say anything
// when a test file mentions them: test-framework, language and UI-library
// vocabulary. They are compared without case.
var commonNames = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`
		beforeEach afterEach beforeAll afterAll setUp tearDown setUpClass
		toBe toEqual toBeVisible toHaveText toContain toMatch toThrow
		getByRole getByText getByTestId getByLabel getByLabelText querySelector
		forEach toString toJSON valueOf hasOwnProperty isArray
		useState useEffect useMemo useCallback useRef useContext setState
		onClick onChange onSubmit onLoad onError onClose
		isEmpty isValid isEnabled getValue setValue getName setName getData
		setData getItem setItem getId`) {
		commonNames[strings.ToLower(w)] = true
	}
}

// hintSource holds what the hint sources read, each once per run: the
// co-change history (one `git log --name-only` over the window) and the text
// of the test files.
type hintSource struct {
	g      repoGit
	base   string
	window config.CoChange
	pm     pathMap
	kg     schema.KnowledgeGraph
	// testFiles maps a graph path to the tests it holds, in graph order:
	// test cases only, never the file's helpers.
	testFiles map[string][]string
	// changed are the git paths the change itself touches; nChanged counts
	// its files.
	changed  []string
	nChanged int

	logRead bool
	// wholeChange is true when the change is small enough (max_files) to
	// count as one unit, "this change"; otherwise its commits count one by
	// one, like the history's.
	wholeChange bool
	commits     []coCommit
	byPath      map[string][]int // git path -> indices into commits

	names    map[string]string // symbol FQN -> its own name
	indexed  bool
	mentions map[string][]mention // name -> first line per test file
	texts    map[string][]byte    // graph path -> text, for route lookups
}

type coCommit struct {
	sha     string // full
	short   string // as git abbreviates it
	parents []string
	files   []string
}

type mention struct {
	file string // graph path
	line int
}

func newHintSource(g repoGit, base string, window config.CoChange, pm pathMap, kg schema.KnowledgeGraph, changes []FileChange) *hintSource {
	h := &hintSource{g: g, base: base, window: window, pm: pm, kg: kg, testFiles: map[string][]string{}, nChanged: len(changes)}
	cases := testCases(kg.Tests)
	for _, t := range kg.Tests {
		if cases[t.FQN] {
			h.testFiles[t.File] = appendNew(h.testFiles[t.File], t.FQN)
		}
	}
	for _, c := range changes {
		for _, p := range []string{c.NewPath, c.OldPath} {
			if p != "" {
				h.changed = appendNew(h.changed, p)
			}
		}
	}
	return h
}

// testFile maps a git path to the graph path of a test file, if it is one.
func (h *hintSource) testFile(gitPath string) (string, bool) {
	gp, _, ok := h.pm.toGraph(gitPath)
	if !ok {
		return "", false
	}
	_, isTest := h.testFiles[gp]
	return gp, isTest
}

// readLog reads the co-change window once: the file lists of the last
// Commits commits before HEAD, no older than Days. A commit that changes
// more than MaxFiles files is skipped, since a sweeping commit says nothing
// about which files belong together, and so is the change as a whole. A
// change within MaxFiles is one unit, "this change", and its own commits
// (those the base does not reach) are not counted again. A failure (no
// history, say) leaves the history empty.
func (h *hintSource) readLog() {
	if h.logRead {
		return
	}
	h.logRead = true
	h.byPath = map[string][]int{}
	commits, days, maxFiles, _ := h.window.Window()
	h.wholeChange = h.nChanged <= maxFiles
	out, err := h.g.run("log", "--no-renames", "--name-only", "--format=\x1e%H %h %P",
		"-n", strconv.Itoa(commits), "--since="+strconv.Itoa(days)+".days.ago", "HEAD", "--")
	if err != nil {
		return
	}
	var all []coCommit
	for _, rec := range strings.Split(string(out), "\x1e") {
		lines := strings.Split(rec, "\n")
		head := strings.Fields(lines[0])
		if len(head) < 2 {
			continue
		}
		c := coCommit{sha: head[0], short: head[1], parents: head[2:]}
		for _, ln := range lines[1:] {
			if ln = strings.TrimRight(ln, "\r"); ln != "" {
				c.files = append(c.files, unquote(ln))
			}
		}
		all = append(all, c)
	}
	inBase := reachable(all, h.base)
	for _, c := range all {
		if len(c.files) == 0 || len(c.files) > maxFiles || (h.wholeChange && !inBase[c.sha]) {
			continue
		}
		i := len(h.commits)
		h.commits = append(h.commits, c)
		for _, f := range c.files {
			h.byPath[f] = append(h.byPath[f], i)
		}
	}
}

// reachable lists the commits of the window that from reaches (from
// included), following the parents the window holds.
func reachable(commits []coCommit, from string) map[string]bool {
	parents := make(map[string][]string, len(commits))
	for _, c := range commits {
		parents[c.sha] = c.parents
	}
	seen := map[string]bool{}
	if _, ok := parents[from]; !ok {
		return seen
	}
	queue := []string{from}
	seen[from] = true
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, p := range parents[cur] {
			if _, ok := parents[p]; ok && !seen[p] {
				seen[p] = true
				queue = append(queue, p)
			}
		}
	}
	return seen
}

// byCoChange lists the tests of the test files that changed in the same
// commits as the symbol's file: in the change itself, or in at least
// MinCommits commits of the window whose co-change score reaches MinScore.
// Each shared commit scores 1/(the files it changes), so a test file that
// only ever changed with the symbol's file in large commits is not hinted.
// The evidence of each file is the commits it shared, the change first, then
// newest first.
// @enforces impact:INV-8
func (h *hintSource) byCoChange(cs ChangedSymbol) ([]string, map[string]string) {
	h.readLog()
	shared := map[string][]string{} // test graph path -> commits
	score := map[string]float64{}   // test graph path -> sum of 1/files over its shared commits
	own := map[string]bool{cs.File: true}
	if cs.OldFile != "" {
		own[cs.OldFile] = true
	}
	if h.wholeChange {
		for _, p := range h.changed {
			if own[p] {
				continue
			}
			if gp, ok := h.testFile(p); ok {
				shared[gp] = appendNew(shared[gp], thisChange)
			}
		}
	}
	var idx []int
	seen := map[int]bool{}
	for p := range own {
		for _, i := range h.byPath[p] {
			if !seen[i] {
				seen[i] = true
				idx = append(idx, i)
			}
		}
	}
	sort.Ints(idx)
	for _, i := range idx {
		c := h.commits[i]
		counted := map[string]bool{}
		for _, f := range c.files {
			if own[f] {
				continue
			}
			if gp, ok := h.testFile(f); ok && !counted[gp] {
				counted[gp] = true
				shared[gp] = appendNew(shared[gp], c.short)
				score[gp] += 1 / float64(len(c.files))
			}
		}
	}
	_, _, _, minCommits := h.window.Window()
	minScore := h.window.Score()
	for gp, commits := range shared {
		if commits[0] == thisChange {
			continue
		}
		if len(commits) < minCommits || score[gp] < minScore-scoreEpsilon {
			delete(shared, gp)
		}
	}
	return h.selectFiles(shared, func(gp string, commits []string) string {
		var parts []string
		if commits[0] == thisChange {
			parts = append(parts, thisChange)
			commits = commits[1:]
		}
		if n := len(commits); n > 0 {
			shown, more := commits, ""
			if n > maxShownCommits {
				shown, more = commits[:maxShownCommits], fmt.Sprintf(" and %d more", n-maxShownCommits)
			}
			noun := "commit"
			if n > 1 {
				noun += "s"
			}
			parts = append(parts, fmt.Sprintf("%d %s (%s%s)", n, noun, strings.Join(shown, ", "), more))
		}
		return fmt.Sprintf("hint: %s changed with %s in %s", h.pm.toGit(gp), cs.File, strings.Join(parts, " and "))
	})
}

// byMention lists the tests of the test files whose text mentions the
// symbol's name as a whole word (case-sensitive), or a route its file
// declares. Only compound names of four characters or more that are not
// common ones are looked for (see mentionable). The evidence of each file is its first mentioning line.
// @enforces impact:INV-8
func (h *hintSource) byMention(cs ChangedSymbol) ([]string, map[string]string) {
	h.index()
	found := map[string][]string{} // test graph path -> ["<line>", "<name>"]
	if name := h.symbolName(cs); mentionable(name) {
		for _, m := range h.mentions[name] {
			found[m.file] = []string{strconv.Itoa(m.line), name}
		}
	}
	for _, route := range h.routes(cs) {
		for gp, text := range h.texts {
			if _, ok := found[gp]; ok {
				continue
			}
			if line := routeLine(text, route); line > 0 {
				found[gp] = []string{strconv.Itoa(line), route}
			}
		}
	}
	return h.selectFiles(found, func(gp string, ev []string) string {
		return fmt.Sprintf("hint: %s:%s mentions %q", h.pm.toGit(gp), ev[0], ev[1])
	})
}

// selectFiles turns per-file evidence into the tests of those files and the
// evidence of each test.
func (h *hintSource) selectFiles(files map[string][]string, say func(string, []string) string) ([]string, map[string]string) {
	var tests []string
	why := map[string]string{}
	paths := make([]string, 0, len(files))
	for gp := range files {
		paths = append(paths, gp)
	}
	sort.Strings(paths)
	for _, gp := range paths {
		ev := say(gp, files[gp])
		for _, t := range h.testFiles[gp] {
			if _, ok := why[t]; !ok {
				tests = append(tests, t)
				why[t] = ev
			}
		}
	}
	sort.Strings(tests)
	return tests, why
}

// index reads every test file once and records, per identifier, the first
// line each file names it on.
func (h *hintSource) index() {
	if h.indexed {
		return
	}
	h.indexed = true
	h.mentions = map[string][]mention{}
	h.texts = map[string][]byte{}
	top := h.g.dir
	paths := make([]string, 0, len(h.testFiles))
	for gp := range h.testFiles {
		paths = append(paths, gp)
	}
	sort.Strings(paths)
	for _, gp := range paths {
		text, err := os.ReadFile(filepath.Join(top, filepath.FromSlash(h.pm.toGit(gp))))
		if err != nil {
			continue
		}
		h.texts[gp] = text
		seen := map[string]bool{}
		line := 1
		for i := 0; i < len(text); {
			c := text[i]
			if c == '\n' {
				line++
				i++
				continue
			}
			if !identByte(c) {
				i++
				continue
			}
			j := i
			for j < len(text) && identByte(text[j]) {
				j++
			}
			w := string(text[i:j])
			if !seen[w] && mentionable(w) {
				seen[w] = true
				h.mentions[w] = append(h.mentions[w], mention{file: gp, line: line})
			}
			i = j
		}
	}
}

// routes are the literal HTTP routes the entry points know for the
// symbol's file: an entry point whose handler is the symbol or lives in its
// file. A route with a parameter, or one shorter than four characters, is
// not looked for.
func (h *hintSource) routes(cs ChangedSymbol) []string {
	gp, _, ok := h.pm.toGraph(cs.File)
	if !ok {
		gp = cs.File
	}
	var out []string
	for _, ep := range h.kg.EntryPoints {
		r := ep.Trigger.Path
		if r == "" || (ep.Handler.Symbol != cs.Symbol && ep.Handler.File != gp && ep.Handler.File != cs.File) {
			continue
		}
		if len(r) < minMentionLen || strings.ContainsAny(r, ":{}[]<>*") || !strings.ContainsAny(r, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			continue
		}
		out = appendNew(out, r)
	}
	sort.Strings(out)
	return out
}

// routeLine is the first line of text holding route as a whole path: not
// preceded by a path or word character and not followed by one, so /cart
// matches '/cart' and "/cart?x=1" but not /carts, /api/cart or /cart/items.
func routeLine(text []byte, route string) int {
	r := []byte(route)
	for off := 0; ; {
		i := bytes.Index(text[off:], r)
		if i < 0 {
			return 0
		}
		i += off
		end := i + len(r)
		if (i == 0 || !routeByte(text[i-1])) && (end == len(text) || !routeByte(text[end])) {
			return bytes.Count(text[:i], []byte{'\n'}) + 1
		}
		off = i + 1
	}
}

func routeByte(c byte) bool { return identByte(c) || c == '/' || c == '-' || c == '.' }

// identByte reports whether c can be part of an identifier. Bytes of a
// multi-byte character count, so a name is never split inside one.
func identByte(c byte) bool {
	return c == '_' || c == '$' || c >= 0x80 || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// mentionable reports whether a name is specific enough to look for: four
// characters or more, a compound identifier (camelCase, PascalCase or
// snake_case: a single plain word such as line or Summary reads as prose in
// a test), and not a common one.
func mentionable(name string) bool {
	if utf8.RuneCountInString(name) < minMentionLen || name[0] >= '0' && name[0] <= '9' || commonNames[strings.ToLower(name)] {
		return false
	}
	return compound(name)
}

// compound reports whether a name joins two or more words: a lower-case
// letter followed by an upper-case one (totalFee, TotalBanner), an
// upper-case run followed by a word (HTTPServer), or an underscore between
// letters or digits (total_fee, MAX_FEE).
func compound(name string) bool {
	isLower := func(c byte) bool { return c >= 'a' && c <= 'z' }
	isUpper := func(c byte) bool { return c >= 'A' && c <= 'Z' }
	isAlnum := func(c byte) bool { return isLower(c) || isUpper(c) || c >= '0' && c <= '9' }
	for i := 1; i < len(name); i++ {
		p, c := name[i-1], name[i]
		switch {
		case isLower(p) && isUpper(c):
			return true
		case isUpper(p) && isUpper(c) && i+1 < len(name) && isLower(name[i+1]):
			return true
		case c == '_' && isAlnum(p) && i+1 < len(name) && isAlnum(name[i+1]):
			return true
		}
	}
	return false
}

// symbolName is the symbol's own name: the graph's, else the last segment
// of its FQN.
func (h *hintSource) symbolName(cs ChangedSymbol) string {
	if h.names == nil {
		h.names = make(map[string]string, len(h.kg.Symbols))
		for _, s := range h.kg.Symbols {
			h.names[s.FQN] = s.Name
		}
	}
	if n, ok := h.names[cs.Symbol]; ok && n != "" {
		return n
	}
	fqn := cs.Symbol
	if fqn == "" {
		fqn = cs.OldSymbol
	}
	return fqn[strings.LastIndex(fqn, ".")+1:]
}
