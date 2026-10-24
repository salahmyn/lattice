package impact

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/scip"
)

// The base is merge-base(since, HEAD), a later commit on the since branch
// is not part of the change, and uncommitted edits are.
func TestBaseIsMergeBase(t *testing.T) {
	r, base := buildFixture(t)
	rep := r.report(r.input("main"))
	if rep.Base != base {
		t.Errorf("base = %s, want the branch point %s", rep.Base, base)
	}
	if !rep.Uncommitted {
		t.Error("the dropped tag is uncommitted; Uncommitted should be true")
	}
	for _, f := range rep.ChangedFiles {
		if f.path() == "other/unrelated.go" {
			t.Errorf("a commit on main after the branch point leaked into the report: %+v", f)
		}
	}
}

// A symbol is changed when a hunk touches its own lines on either side; a
// symbol that only shifted, moved files unchanged, or lost a tag above it
// is not.
// @verifies impact:INV-2
func TestChangedCodeByLineRange(t *testing.T) {
	r, _ := buildFixture(t)
	rep := r.report(r.input("main"))

	want := map[string]string{
		"refund.Guard":     ChangeModified, // body edited (new-side hunk)
		"refund.Audit":     ChangeModified, // body edited
		"refund.NewHelper": ChangeAdded,
		"refund.Limit":     ChangeRemoved, // old-side hunk only
		"refund.Gone":      ChangeRemoved, // file deleted
	}
	for fqn, change := range want {
		s, ok := findSymbol(rep, fqn)
		if !ok {
			t.Errorf("%s missing from changed symbols", fqn)
			continue
		}
		if s.Change != change {
			t.Errorf("%s change = %s, want %s", fqn, s.Change, change)
		}
	}
	for _, fqn := range []string{
		"refund.Untouched",              // same file, shifted down by NewHelper, lines untouched
		"refund.Tagged",                 // only the @enforces comment above it changed
		"refund.Kept", "elsewhere.Kept", // moved directories, text unchanged
		"refund.Moved", "other.Unrelated",
	} {
		if s, ok := findSymbol(rep, fqn); ok {
			t.Errorf("%s should not be changed: %+v", fqn, s)
		}
	}
	g, _ := findSymbol(rep, "refund.Guard")
	if g.Lines != "12-17" || g.OldLines != "7-9" || !reflect.DeepEqual(g.Enforces, []string{"refund:INV-1"}) {
		t.Errorf("Guard extents/enforces = %+v", g)
	}
	if len(rep.ChangedSymbols) != len(want) {
		t.Errorf("changed symbols = %d, want %d: %+v", len(rep.ChangedSymbols), len(want), rep.ChangedSymbols)
	}
}

// A function cut and pasted lower in its file sits in a hunk on both sides,
// but its text is unchanged, so it is not a changed symbol.
// @verifies impact:INV-2
func TestMovedSymbolIsNotChanged(t *testing.T) {
	r := newRepo(t)
	a := "// A is first.\nfunc A() int {\n\treturn 1\n}\n"
	b := "// B is second.\nfunc B() int {\n\tx := 2\n\treturn x\n}\n"
	c := "// C is third.\nfunc C() int {\n\treturn 3\n}\n"
	r.write("p/p.go", "package p\n\n"+a+"\n"+b+"\n"+c)
	r.commit("base")
	r.write("p/p.go", "package p\n\n"+b+"\n"+c+"\n"+a)
	rep := r.report(r.input("HEAD"))
	if len(rep.ChangedFiles) != 1 {
		t.Fatalf("changed files = %+v", rep.ChangedFiles)
	}
	if len(rep.ChangedSymbols) != 0 {
		t.Errorf("a moved, unchanged function is not changed: %+v", rep.ChangedSymbols)
	}
}

func TestOverlapsIgnoresEmptySide(t *testing.T) {
	s := sym(10, 20)
	if overlaps(s, nil) {
		t.Error("no ranges overlaps nothing")
	}
	h := parseHunks([]byte("diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -9,0 +10,3 @@\n+a\n@@ -21 +24 @@\n-b\n+c\n"))
	got := h["x.go"]
	if len(got.old) != 1 || got.old[0] != (lineRange{21, 21}) {
		t.Errorf("old ranges = %v: a pure insertion has no old-side lines", got.old)
	}
	if len(got.new) != 2 || got.new[0] != (lineRange{10, 12}) {
		t.Errorf("new ranges = %v", got.new)
	}
	if !overlaps(s, got.new[:1]) || overlaps(sym(13, 20), got.new[:1]) {
		t.Error("overlap must be inclusive at both ends and nothing more")
	}
}

// Criteria of approved BRDs come first, draft and proposed ones apart and
// only when asked for; direct-wired criteria and multi-invariant mappings
// count.
func TestAffectedCriteria(t *testing.T) {
	r, _ := buildFixture(t)
	in := r.input("main")
	rep := r.report(in)

	cases := []struct {
		sc, symbol, inv, change string
	}{
		{"SC-1", "refund.Guard", "refund:INV-1", ChangeModified},
		{"SC-2", "refund.Tagged", "refund:INV-3", ChangeEnforcementRemoved},
		{"SC-2", "refund.Gone", "refund:INV-4", ChangeEnforcementRemoved},
		{"SC-3", "refund.Audit", "brd.refunds:SC-3", ChangeModified}, // direct_wire + enforced_by
		{"SC-4", "refund.Moved", "refund:INV-5", ChangeEnforcementRemoved},
	}
	for _, c := range cases {
		ac, ok := findCriterion(rep.AffectedCriteria, "brd.refunds", c.sc)
		if !ok {
			t.Errorf("brd.refunds/%s not affected", c.sc)
			continue
		}
		if ac.BRDStatus != "approved" {
			t.Errorf("%s status = %s", c.sc, ac.BRDStatus)
		}
		found := false
		for _, e := range ac.Enforcers {
			if e.Symbol == c.symbol && e.Invariant == c.inv && e.Change == c.change {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want enforcer %s/%s (%s), got %+v", c.sc, c.symbol, c.inv, c.change, ac.Enforcers)
		}
	}
	if sc1, _ := findCriterion(rep.AffectedCriteria, "brd.refunds", "SC-1"); sc1.Tier != 3 || sc1.Statement == "" {
		t.Errorf("SC-1 detail = %+v", sc1)
	}
	if sc2, _ := findCriterion(rep.AffectedCriteria, "brd.refunds", "SC-2"); !reflect.DeepEqual(sc2.Invariants,
		[]string{"refund:INV-3", "refund:INV-4", "refund:INV-6"}) {
		t.Errorf("SC-2 invariants = %v", sc2.Invariants)
	}
	if _, ok := findCriterion(rep.AffectedCriteria, "brd.payouts", "SC-1"); ok {
		t.Error("a draft BRD's criterion is listed with the approved ones")
	}
	d, ok := findCriterion(rep.DraftCriteria, "brd.payouts", "SC-1")
	if !ok || d.BRDStatus != "draft" || d.Enforcers[0].Symbol != "refund.Limit" {
		t.Errorf("draft criterion = %+v (found %v)", d, ok)
	}
	if _, ok := findCriterion(rep.DraftCriteria, "brd.payouts", "SC-2"); ok {
		t.Error("brd.payouts/SC-2 maps to INV-6, whose enforcer only moved; it is not affected")
	}

	in.IncludeDrafts = false
	hidden := r.report(in)
	if hidden.DraftCriteria != nil || hidden.DraftsIncluded {
		t.Errorf("--include-drafts=false should hide the section: %+v", hidden.DraftCriteria)
	}
	b, _ := json.Marshal(hidden)
	if bytes.Contains(b, []byte(`"draft_criteria"`)) {
		t.Error("draft_criteria should be absent from JSON when hidden")
	}
	var md bytes.Buffer
	Markdown(&md, hidden)
	if strings.Contains(md.String(), "draft and proposed") {
		t.Error("markdown should not show the drafts section when hidden")
	}
}

// Every way an enforcer stops enforcing is reported, including a dropped
// tag on a symbol whose own lines did not change; a file moved within the
// scanned tree is not a removal.
// @verifies impact:INV-3
func TestRemovedEnforcement(t *testing.T) {
	r, _ := buildFixture(t)
	rep := r.report(r.input("main"))
	want := map[string]RemovedEnforcement{
		"refund.Limit":  {Invariant: "refund:INV-2", File: "refund/guard.go", Reason: ReasonSymbolDeleted},
		"refund.Tagged": {Invariant: "refund:INV-3", File: "refund/tagged.go", Reason: ReasonTagDropped},
		"refund.Gone":   {Invariant: "refund:INV-4", File: "refund/gone.go", Reason: ReasonFileDeleted},
		"refund.Moved":  {Invariant: "refund:INV-5", File: "refund/moved.go", Reason: ReasonMovedOutOfScan, NewFile: "vendor/moved.go"},
	}
	got := map[string]RemovedEnforcement{}
	for _, re := range rep.RemovedEnforcement {
		got[re.Symbol] = re
	}
	for fqn, w := range want {
		g, ok := got[fqn]
		if !ok {
			t.Errorf("%s: removal not reported", fqn)
			continue
		}
		if g.Invariant != w.Invariant || g.File != w.File || g.Reason != w.Reason || g.NewFile != w.NewFile {
			t.Errorf("%s = %+v, want %+v", fqn, g, w)
		}
	}
	if len(rep.RemovedEnforcement) != len(want) {
		t.Errorf("removals = %+v, want exactly %d (the file moved within the tree is not one)", rep.RemovedEnforcement, len(want))
	}
}

// The manifest diff names every changed field generically, including the
// ones a hand-written diff missed, and tells renames from removals.
// @verifies impact:INV-4
func TestManifestDiff(t *testing.T) {
	r, _ := buildFixture(t)
	md := r.report(r.input("main")).ManifestDiff

	feat, ok := findChange(md.Features.Changed, "refund")
	if !ok {
		t.Fatalf("feature refund not changed: %+v", md.Features)
	}
	for _, f := range []string{"invariants[INV-1].verifiable_by", "capabilities[issue].rules"} {
		if _, ok := findField(feat, f); !ok {
			t.Errorf("feature field %s not reported: %+v", f, feat.Fields)
		}
	}
	if len(feat.Fields) != 2 {
		t.Errorf("feature fields = %+v, want only the two that changed", feat.Fields)
	}

	brd, ok := findChange(md.BRDs.Changed, "brd.refunds")
	if !ok {
		t.Fatalf("brd.refunds not changed: %+v", md.BRDs)
	}
	for _, f := range []string{
		"user_scenarios[US-1].actor", "user_scenarios[US-1].verified_by", "user_scenarios[US-1].given",
		"user_scenarios[US-1].when", "user_scenarios[US-1].then", "user_scenarios[US-1].blocks",
		"success_criteria[SC-1].tier", "success_criteria[SC-2].maps_to_invariant",
	} {
		if _, ok := findField(brd, f); !ok {
			t.Errorf("BRD field %s not reported: %+v", f, brd.Fields)
		}
	}
	tier, _ := findField(brd, "success_criteria[SC-1].tier")
	if tier.Change != "changed" || tier.Before != 2 || tier.After != 3 {
		t.Errorf("tier change = %+v", tier)
	}

	ep, ok := findChange(md.EntryPoints.Changed, "ep.cli.refund")
	if !ok {
		t.Fatalf("entry point not changed: %+v", md.EntryPoints)
	}
	if _, ok := findField(ep, "purpose"); !ok {
		t.Errorf("entry point purpose change missing: %+v", ep.Fields)
	}

	// Renamed within lattice/features/: compared by id, old path kept.
	if len(md.Features.Renamed) != 1 || md.Features.Renamed[0].ID != "notes" ||
		md.Features.Renamed[0].OldPath != "lattice/features/notes/old.yaml" {
		t.Errorf("renamed = %+v", md.Features.Renamed)
	}
	// Moved out of lattice/features/: removed.
	if len(md.Features.Removed) != 1 || md.Features.Removed[0].ID != "legacy" {
		t.Errorf("removed = %+v", md.Features.Removed)
	}
	if len(md.Features.Added) != 1 || md.Features.Added[0].ID != "added" {
		t.Errorf("added = %+v", md.Features.Added)
	}
}

// A field the schema has but no test names still shows up: the walk is
// generic over the canonical YAML of the struct.
// @verifies impact:INV-4
func TestDiffNodeCoversEveryField(t *testing.T) {
	a := decode(kindBRD, []byte("id: brd.x\nversion: 1\nstatus: draft\ntitle: T\nbusiness_problem: P\nconstraints:\n  - kind: technical\n    note: a\nflags:\n  - id: f1\n    meaning: on\n"))
	b := decode(kindBRD, []byte("id: brd.x\nversion: 1\nstatus: draft\ntitle: T\nbusiness_problem: P\nconstraints:\n  - kind: technical\n    note: b\nflags:\n  - id: f1\n    meaning: off\n  - id: f2\n"))
	mc := compare(a, b)
	got := map[string]string{}
	for _, f := range mc.Fields {
		got[f.Field] = f.Change
	}
	want := map[string]string{"constraints": "changed", "flags[f1].meaning": "changed", "flags[f2]": "added"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fields = %v, want %v", got, want)
	}
	// A single ref and a one-item list are the same value.
	s := decode(kindBRD, []byte("id: brd.x\nversion: 1\nstatus: draft\ntitle: T\nbusiness_problem: P\nsuccess_criteria:\n  - id: SC-1\n    statement: s\n    maps_to_invariant: f:INV-1\n"))
	l := decode(kindBRD, []byte("id: brd.x\nversion: 1\nstatus: draft\ntitle: T\nbusiness_problem: P\nsuccess_criteria:\n  - id: SC-1\n    statement: s\n    maps_to_invariant: [f:INV-1]\n"))
	if mc := compare(s, l); len(mc.Fields) != 0 {
		t.Errorf("same refs written two ways differ: %+v", mc.Fields)
	}
}

// writeIndex writes a SCIP index where api/handler.go and a test reference
// refund.Guard, and other/unrelated.go references refund.Audit.
func writeIndex(t *testing.T, r *repo) *scip.Corpus {
	t.Helper()
	sym := func(name string) string { return "scip-go gomod example v1 `example/refund`/" + name + "()." }
	occ := func(line int32, s string, def bool) *scippb.Occurrence {
		o := &scippb.Occurrence{Range: []int32{line, 0, line, 1}, Symbol: s}
		if def {
			o.SymbolRoles = int32(scippb.SymbolRole_Definition)
		}
		return o
	}
	idx := &scippb.Index{Documents: []*scippb.Document{
		{RelativePath: "refund/guard.go", Occurrences: []*scippb.Occurrence{occ(11, sym("Guard"), true), occ(24, sym("Audit"), true)}},
		{RelativePath: "api/handler.go", Occurrences: []*scippb.Occurrence{occ(6, sym("Guard"), false)}},
		{RelativePath: "refund/guard_test.go", Occurrences: []*scippb.Occurrence{occ(6, sym("Guard"), false)}},
		{RelativePath: "other/unrelated.go", Occurrences: []*scippb.Occurrence{occ(6, sym("Audit"), false)}},
	}}
	data, err := proto.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(r.dir, "lattice", ".cache", "scip", "go.scip")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := scip.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// With a code graph each changed enforcer rolls up to the modules, test
// files and entry points that reach it; --max-symbols caps the expansion
// and says so.
func TestCodeRollup(t *testing.T) {
	r, _ := buildFixture(t)
	in := r.input("main")
	in.Corpus = writeIndex(t, r)
	cr := r.report(in).CodeRollup

	if !cr.Available || cr.Truncated || cr.Total != 6 || cr.Expanded != 6 {
		t.Fatalf("roll-up = available %v truncated %v total %d expanded %d", cr.Available, cr.Truncated, cr.Total, cr.Expanded)
	}
	var guard SymbolRollup
	for _, s := range cr.Symbols {
		if s.Symbol == "refund.Guard" {
			guard = s
		}
	}
	if !guard.Resolved || !reflect.DeepEqual(guard.Modules, []string{"api/handler.go"}) ||
		!reflect.DeepEqual(guard.TestFiles, []string{"refund/guard_test.go"}) ||
		!reflect.DeepEqual(guard.EntryPoints, []string{"ep.cli.refund"}) {
		t.Errorf("Guard roll-up = %+v", guard)
	}
	if len(cr.EntryPoints) != 1 || !reflect.DeepEqual(cr.EntryPoints[0].Via, []string{"flow: refund", "handler file"}) {
		t.Errorf("entry points = %+v", cr.EntryPoints)
	}
	if len(cr.Modules) != 2 || cr.Modules[0] != (RollupModule{File: "api/handler.go", Feature: "api"}) {
		t.Errorf("modules = %+v", cr.Modules)
	}

	in.MaxSymbols = 2
	capped := r.report(in).CodeRollup
	if !capped.Truncated || capped.Expanded != 2 || capped.Total != 6 || len(capped.Symbols) != 2 {
		t.Fatalf("capped roll-up = truncated %v expanded %d total %d", capped.Truncated, capped.Expanded, capped.Total)
	}
	for _, s := range capped.Symbols {
		if s.Symbol == "refund.Limit" {
			t.Error("an enforcer of a draft criterion only should come after the approved ones")
		}
	}
	var md bytes.Buffer
	Markdown(&md, r.report(in))
	if !strings.Contains(md.String(), "truncated at --max-symbols 2") {
		t.Errorf("markdown should say it truncated:\n%s", md.String())
	}
}

// With no code graph the section says so and the rest of the report holds.
func TestNoCodeGraph(t *testing.T) {
	r, _ := buildFixture(t)
	rep := r.report(r.input("main"))
	if rep.CodeRollup.Available || !strings.Contains(rep.CodeRollup.Note, "code graph unavailable") {
		t.Errorf("roll-up = %+v", rep.CodeRollup)
	}
	if len(rep.AffectedCriteria) == 0 {
		t.Error("the report should still hold without a code graph")
	}
	var txt, md bytes.Buffer
	Text(&txt, rep)
	Markdown(&md, rep)
	for name, out := range map[string]string{"text": txt.String(), "markdown": md.String()} {
		if !strings.Contains(out, "code graph unavailable") {
			t.Errorf("%s output does not say the code graph is unavailable", name)
		}
	}
}

// The markdown form is a PR comment: a marker to find it, the base commit,
// and every section in order.
func TestMarkdownShape(t *testing.T) {
	r, base := buildFixture(t)
	var md bytes.Buffer
	Markdown(&md, r.report(r.input("main")))
	out := md.String()
	if !strings.HasPrefix(out, MarkdownMarker+"\n") {
		t.Error("markdown must start with the marker")
	}
	if !strings.Contains(out, "base `"+base[:12]+"` (merge base of `main`") {
		t.Errorf("markdown should name the base commit:\n%s", out)
	}
	last := -1
	for _, h := range []string{"### Changed code", "### Affected criteria: approved BRDs",
		"### Affected criteria: draft and proposed BRDs", "### Removed enforcement", "### Manifest changes", "### Code roll-up"} {
		i := strings.Index(out, h)
		if i < 0 || i < last {
			t.Errorf("section %q missing or out of order", h)
		}
		last = i
	}
	if !strings.Contains(out, "| `refund.Tagged` | `refund:INV-3` | `refund/tagged.go:7` | @enforces tag dropped |") {
		t.Errorf("removed-enforcement row missing:\n%s", out)
	}
	if !strings.Contains(out, "  - `invariants[INV-1].verifiable_by`: added: `[test, mutation]`") {
		t.Errorf("field change line missing:\n%s", out)
	}
}

func sym(from, to int) schema.GraphSymbol { return schema.GraphSymbol{Line: from, EndLine: to} }
