package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/store"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// lensJSON is the assurance lens as the page reads it, decoded from the wire.
type lensJSON struct {
	Blocks    []schema.Block `json:"blocks"`
	Assurance struct {
		Contract  string            `json:"contract"`
		Available bool              `json:"available"`
		Note      string            `json:"note"`
		Order     []string          `json:"order"`
		Styles    map[string]string `json:"styles"`
		Fallback  string            `json:"fallback"`
		Legend    []struct {
			Key, Style, Label, Meaning string
			Count                      int
		} `json:"legend"`
		Promises map[string]struct {
			Class   string `json:"class"`
			Block   string `json:"block"`
			Missing bool   `json:"missing"`
		} `json:"promises"`
		Nodes map[string]struct {
			Class    string         `json:"class"`
			Style    string         `json:"style"`
			Counts   map[string]int `json:"counts"`
			Promises []string       `json:"promises"`
		} `json:"nodes"`
	} `json:"assurance"`
}

// inlined decodes the data an exported map page carries.
func inlined(t *testing.T, page []byte) lensJSON {
	t.Helper()
	m := regexp.MustCompile(`(?s)<script id="graph" type="application/json">(.*?)</script>`).FindSubmatch(page)
	if m == nil {
		t.Fatal("the page has no inlined graph")
	}
	var d lensJSON
	if err := json.Unmarshal(m[1], &d); err != nil {
		t.Fatal(err)
	}
	return d
}

// lensFixture is an invented shop: fees has a break-tested promise and a fake
// one, wallet an unenforced one, orders one only demonstrated. The block graph
// nests fees and wallet in a group inside a system, and keeps a promise block
// of a feature the audit no longer knows.
func lensFixture(t *testing.T) (*Server, schema.KnowledgeGraph, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "lattice")
	writeFile(t, filepath.Join(dir, ".cache", "results", "results.json"),
		`{"commit":"c0ffee","results":{"fees.testcap":"pass","fees.testround":"pass","orders.testplace":"pass"}}`)
	writeFile(t, filepath.Join(dir, ".cache", "results", "breaks.json"), `{"schema":"lattice.breaks/1","commit":"c0ffee","runs":[
	  {"promise":"fees:INV-1","enforcer":"fees.Cap","method":"early_return","baseline":"pass","outcome":"killed","verifiers":["fees.TestCap"]},
	  {"promise":"fees:INV-2","enforcer":"fees.Round","method":"early_return","baseline":"pass","outcome":"survived","verifiers":["fees.TestRound"]}]}`)
	kg := schema.KnowledgeGraph{
		GeneratedFromCommit: "c0ffee",
		Features: []schema.Manifest{
			{ID: "fees", Invariants: []schema.Invariant{{ID: "INV-1", Statement: "A fee never exceeds five percent."}, {ID: "INV-2", Statement: "A fee rounds half up."}}},
			{ID: "wallet", Invariants: []schema.Invariant{{ID: "INV-1", Statement: "A balance is never negative."}}},
			{ID: "orders", Invariants: []schema.Invariant{{ID: "INV-1", Statement: "Only a paid basket becomes an order."}}},
		},
		Symbols: []schema.GraphSymbol{
			{FQN: "fees.Cap", Feature: "fees", EnforcesInvariants: []string{"INV-1"}},
			{FQN: "fees.Round", Feature: "fees", EnforcesInvariants: []string{"INV-2"}},
			{FQN: "orders.Place", Feature: "orders", EnforcesInvariants: []string{"INV-1"}},
		},
		Tests: []schema.GraphSymbol{
			{FQN: "fees.TestCap", Feature: "fees", IsTest: true, Verifies: []string{"INV-1"}},
			{FQN: "fees.TestRound", Feature: "fees", IsTest: true, Verifies: []string{"INV-2"}},
			{FQN: "orders.TestPlace", Feature: "orders", IsTest: true, Verifies: []string{"INV-1"}},
		},
	}
	ws := &workspace.Workspace{LatticeDir: dir}
	st := store.OpenFiles(ws)
	g := store.Graph{Blocks: []schema.Block{
		{ID: "sys.shop", Kind: "system", Title: "Shop"},
		{ID: "grp.money", Kind: "group", Parent: "sys.shop", Title: "Money"},
		{ID: "feat:fees", Kind: "composite", Parent: "grp.money", Title: "fees"},
		{ID: "inv:fees.INV-1", Kind: "invariant", Parent: "feat:fees", Title: "INV-1"},
		{ID: "inv:fees.INV-2", Kind: "invariant", Parent: "feat:fees", Title: "INV-2"},
		{ID: "feat:wallet", Kind: "composite", Parent: "grp.money", Title: "wallet"},
		{ID: "inv:wallet.INV-1", Kind: "invariant", Parent: "feat:wallet", Title: "INV-1"},
		{ID: "feat:orders", Kind: "composite", Title: "orders"},
		{ID: "inv:orders.INV-1", Kind: "invariant", Parent: "feat:orders", Title: "INV-1"},
		{ID: "web:cart", Kind: "screen", Parent: "sys.shop", Title: "Cart"},
		{ID: "feat:legacy", Kind: "composite", Title: "legacy"},
		{ID: "inv:legacy.INV-1", Kind: "invariant", Parent: "feat:legacy", Title: "INV-1"},
	}}
	if err := st.ReplaceGraph(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	build := func(context.Context) (schema.KnowledgeGraph, error) { return kg, nil }
	return New(ws, Options{Store: st, GraphBuilder: build}), kg, dir
}

func getGraph(t *testing.T, srv *Server) lensJSON {
	t.Helper()
	w := httptest.NewRecorder()
	srv.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/graph", nil))
	if w.Code != 200 {
		t.Fatalf("/api/graph = %d %s", w.Code, w.Body.String())
	}
	var d lensJSON
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	return d
}

// The map's data carries, for every block, the class the audit computed for
// the promises it holds; a block holding several takes the weakest, by the
// order the kernel exports with the data.
// @verifies ui:INV-2
func TestMapDataCarriesTheKernelClass(t *testing.T) {
	srv, kg, dir := lensFixture(t)
	d := getGraph(t, srv)
	a := d.Assurance
	if len(d.Blocks) != 12 {
		t.Fatalf("the graph lost blocks: %d", len(d.Blocks))
	}
	if !a.Available || a.Contract != audit.Schema {
		t.Fatalf("lens: available %v, contract %q (%s)", a.Available, a.Contract, a.Note)
	}
	if !slices.Equal(a.Order, audit.Classes) {
		t.Errorf("order = %v, want the audit's own %v", a.Order, audit.Classes)
	}

	// each promise block reads exactly the audit's class for its promise
	rep := audit.Compute(audit.Collect(dir, kg, false))
	for _, row := range rep.Rows {
		id := "inv:" + row.Feature + "." + row.Invariant
		if n := a.Nodes[id]; n.Class != row.Class || !slices.Equal(n.Promises, []string{row.Promise}) {
			t.Errorf("%s: node %+v, want the audit's class %s", id, n, row.Class)
		}
	}
	want := map[string]string{"fees:INV-1": audit.ClassBreakTested, "fees:INV-2": audit.ClassFake,
		"wallet:INV-1": audit.ClassUnenforced, "orders:INV-1": audit.ClassDemonstratedOnly}
	for ref, c := range want {
		if a.Promises[ref].Class != c {
			t.Errorf("fixture: %s reads %q, want %s", ref, a.Promises[ref].Class, c)
		}
	}

	// the weakest, by the kernel's order, with the count per class
	for _, c := range []struct {
		id, class string
		counts    map[string]int
		promises  []string
	}{
		{"feat:fees", audit.ClassFake, map[string]int{audit.ClassFake: 1, audit.ClassBreakTested: 1}, []string{"fees:INV-2", "fees:INV-1"}},
		{"grp.money", audit.ClassFake, map[string]int{audit.ClassFake: 1, audit.ClassUnenforced: 1, audit.ClassBreakTested: 1}, []string{"fees:INV-2", "wallet:INV-1", "fees:INV-1"}},
		{"sys.shop", audit.ClassFake, map[string]int{audit.ClassFake: 1, audit.ClassUnenforced: 1, audit.ClassBreakTested: 1}, []string{"fees:INV-2", "wallet:INV-1", "fees:INV-1"}},
		{"feat:wallet", audit.ClassUnenforced, map[string]int{audit.ClassUnenforced: 1}, []string{"wallet:INV-1"}},
		{"feat:orders", audit.ClassDemonstratedOnly, map[string]int{audit.ClassDemonstratedOnly: 1}, []string{"orders:INV-1"}},
	} {
		n := a.Nodes[c.id]
		if n.Class != c.class || n.Style != a.Styles[c.class] || !mapsEqual(n.Counts, c.counts) || !slices.Equal(n.Promises, c.promises) {
			t.Errorf("%s = %+v, want class %s, counts %v, promises %v", c.id, n, c.class, c.counts, c.promises)
		}
	}
	// every node: its class is the first class of the order it holds
	for _, b := range d.Blocks {
		n, ok := a.Nodes[b.ID]
		if !ok {
			t.Errorf("%s has no lens entry", b.ID)
			continue
		}
		first := ""
		for _, c := range a.Order {
			if n.Counts[c] > 0 {
				first = c
				break
			}
		}
		if n.Class != first {
			t.Errorf("%s reads %q, but the weakest class it holds is %q (%v)", b.ID, n.Class, first, n.Counts)
		}
	}
	if n := a.Nodes["web:cart"]; n.Class != "" || n.Style != "none" {
		t.Errorf("a block with no promise = %+v, want no class and the no-promise style", n)
	}
	// a promise block the audit has no row for reads unknown, kept on the map
	if p, n := a.Promises["legacy:INV-1"], a.Nodes["inv:legacy.INV-1"]; !p.Missing || p.Class != audit.ClassUnknown || n.Class != audit.ClassUnknown || n.Style != a.Fallback {
		t.Errorf("an unaudited promise block: promise %+v node %+v, want unknown", p, n)
	}
}

func mapsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// cssTable reads the template's style table: .as-<style> { --as-c: var(--as-<token>); }.
func cssTable(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^\.as-([a-z-]+) \{ --as-c: var\(--([a-z-]+)\); \}$`).FindAllStringSubmatch(string(mapHTML), -1) {
		out[m[1]] = m[2]
	}
	if len(out) == 0 {
		t.Fatal("the template has no assurance style table")
	}
	return out
}

// No class is drawn in the pass style unless the audit calls it positive:
// break_tested alone gets the full pass style, exercised the weak one, and
// everything else, unknown and anything the page cannot place included, a
// style whose colour is not a pass colour.
// @verifies ui:INV-2
func TestLensPassStyleOnlyForPositiveClasses(t *testing.T) {
	a := inlined(t, mustPage(t)).Assurance
	passStyles := map[string]bool{"pass": true, "pass-weak": true} // the template's green styles
	for _, class := range audit.Classes {
		style, ok := a.Styles[class]
		switch {
		case !ok:
			t.Errorf("no style for class %s", class)
		case passStyles[style] != audit.Positive(class):
			t.Errorf("%s is drawn %q; the audit calls it positive: %v", class, style, audit.Positive(class))
		case style == "pass" && class != audit.ClassBreakTested:
			t.Errorf("%s gets the full pass style; only break_tested may", class)
		}
	}
	if passStyles[a.Fallback] {
		t.Errorf("the fallback style %q is a pass style", a.Fallback)
	}
	for _, k := range a.Legend {
		if passStyles[k.Style] != audit.Positive(k.Key) {
			t.Errorf("legend %s drawn %q", k.Key, k.Style)
		}
		if k.Meaning == "" || k.Label == "" {
			t.Errorf("legend %s has no plain meaning", k.Key)
		}
	}

	// the template's table: every style the data can name has a colour, and
	// only the two pass styles take a pass colour
	table := cssTable(t)
	emitted := map[string]bool{a.Fallback: true}
	for _, s := range a.Styles {
		emitted[s] = true
	}
	for _, k := range a.Legend {
		emitted[k.Style] = true
	}
	for s := range emitted {
		if _, ok := table[s]; !ok {
			t.Errorf("style %q has no colour in the template", s)
		}
	}
	passTokens := map[string]bool{"as-pass": true, "as-pass-weak": true}
	for style, token := range table {
		if passTokens[token] != passStyles[style] || (passTokens[token] && token != "as-"+style) {
			t.Errorf(".as-%s takes --%s", style, token)
		}
	}
	// and no other token has a pass colour's value, in either theme
	for _, theme := range regexp.MustCompile(`(?s)--as-fake:.*?--as-pass: (#[0-9a-f]{6});`).FindAllString(string(mapHTML), -1) {
		vals := map[string]string{}
		for _, m := range regexp.MustCompile(`--(as-[a-z-]+): (#[0-9a-f]{6});`).FindAllStringSubmatch(theme, -1) {
			vals[m[1]] = m[2]
		}
		for tok, v := range vals {
			if !passTokens[tok] && (v == vals["as-pass"] || v == vals["as-pass-weak"]) {
				t.Errorf("--%s shares a pass colour %s", tok, v)
			}
		}
	}
	// the page computes no class: it names none in its script
	script := string(mapHTML)[strings.Index(string(mapHTML), "<script>"):]
	for _, class := range audit.Classes {
		for _, q := range []string{`"`, `'`, "`"} {
			if strings.Contains(script, q+class+q) {
				t.Errorf("the page's script names the class %s: a class must come from the data", class)
			}
		}
	}
	if !strings.Contains(string(mapHTML), "Unknown is never a pass.") {
		t.Error("the legend lacks the line: Unknown is never a pass.")
	}
}

func mustPage(t *testing.T) []byte {
	t.Helper()
	page, err := MapHTML(store.Graph{Blocks: []schema.Block{{ID: "feat:fees", Kind: "composite", Title: "fees"}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	return page
}

// Missing evidence reads unknown in the exported data: an export without an
// audit keeps every block and draws every promise unknown, and a promise the
// audit has no row for reads unknown too. None reads a pass.
// @verifies ui:INV-2
func TestMapExportMissingEvidenceReadsUnknown(t *testing.T) {
	g := store.Graph{Blocks: []schema.Block{
		{ID: "grp.money", Kind: "group", Title: "Money"},
		{ID: "feat:fees", Kind: "composite", Parent: "grp.money", Title: "fees"},
		{ID: "inv:fees.INV-1", Kind: "invariant", Parent: "feat:fees", Title: "INV-1"},
		{ID: "inv:fees.INV-2", Kind: "invariant", Parent: "feat:fees", Title: "INV-2"},
		{ID: "web:cart", Kind: "screen", Title: "Cart"},
	}}
	page, err := MapHTML(g, "")
	if err != nil {
		t.Fatal(err)
	}
	a := inlined(t, page).Assurance
	if a.Available || a.Note == "" {
		t.Errorf("an export without an audit says so: available %v, note %q", a.Available, a.Note)
	}
	for _, id := range []string{"grp.money", "feat:fees", "inv:fees.INV-1", "inv:fees.INV-2"} {
		if n := a.Nodes[id]; n.Class != audit.ClassUnknown || n.Style != "unknown" {
			t.Errorf("%s = %+v, want unknown", id, n)
		}
	}
	if len(a.Nodes) != len(g.Blocks) {
		t.Errorf("%d lens entries for %d blocks: none may be dropped", len(a.Nodes), len(g.Blocks))
	}
	for ref, p := range a.Promises {
		if !p.Missing || p.Class != audit.ClassUnknown {
			t.Errorf("%s = %+v, want unknown and missing", ref, p)
		}
	}

	// the audit ran, with no results, coverage or breaks: the kernel's own
	// classes, never a pass; a promise it has no row for reads unknown
	kg := schema.KnowledgeGraph{
		Features: []schema.Manifest{{ID: "fees", Invariants: []schema.Invariant{{ID: "INV-1", Statement: "A fee never exceeds five percent."}}}},
		Symbols:  []schema.GraphSymbol{{FQN: "fees.Cap", Feature: "fees", EnforcesInvariants: []string{"INV-1"}}},
		Tests:    []schema.GraphSymbol{{FQN: "fees.TestCap", Feature: "fees", IsTest: true, Verifies: []string{"INV-1"}}},
	}
	ws := &workspace.Workspace{LatticeDir: t.TempDir()}
	st := store.OpenFiles(ws)
	if err := st.ReplaceGraph(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	build := func(context.Context) (schema.KnowledgeGraph, error) { return kg, nil }
	rep := audit.Compute(audit.Collect(ws.LatticeDir, kg, false))
	a = getGraph(t, New(ws, Options{Store: st, GraphBuilder: build})).Assurance
	if n := a.Nodes["inv:fees.INV-1"]; n.Class != rep.Rows[0].Class || audit.Positive(n.Class) {
		t.Errorf("a promise with no evidence = %+v, want the audit's %s and not a pass", n, rep.Rows[0].Class)
	}
	if n := a.Nodes["inv:fees.INV-2"]; n.Class != audit.ClassUnknown || !a.Promises["fees:INV-2"].Missing {
		t.Errorf("a promise block with no audit row = %+v, want unknown", n)
	}
	for id, n := range a.Nodes {
		if n.Style == "pass" || n.Style == "pass-weak" {
			t.Errorf("%s is drawn as a pass with no evidence", id)
		}
	}
}
