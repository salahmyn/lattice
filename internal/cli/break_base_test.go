package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/breaktest"
	"github.com/salahmyn/lattice/pkg/lattice/check"
)

func featureYAML(id, statement string) string {
	return "id: " + id + "\nversion: 1\nstatus: production\npurpose: A fixture.\nowners:\n  business: b\n  engineering: e\n" +
		"invariants:\n  - id: INV-1\n    statement: " + statement + "\n"
}

// baseProofFixture is a repository whose main branch has a buggy fee
// function, and a checked-out branch that fixes it and adds four verifiers:
// one that fails on the bug, one that passes either way, one that calls a
// function only the branch has, a TypeScript one no runner covers, and a
// Python one, run by a {files} runner, that imports a name only the branch
// has.
func baseProofFixture(t *testing.T) *checkFixture {
	t.Helper()
	for _, tool := range []string{"go", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
	f := newCheckFixture(t)
	f.write("go.mod", "module fixture\n\ngo 1.21\n")
	f.write("lattice/adapters.yaml", "adapters:\n  go:\n    enabled: true\n  typescript:\n    enabled: true\n  python:\n    enabled: true\n")
	f.write("lattice/config.yaml", "agentic:\n  llm:\n    enabled: false\nbreak:\n  runners:\n    - name: py\n      match: [\"**/test_*.py\"]\n      command: [\"python3\", \"{files}\"]\n")
	f.write("lattice/features/calc.yaml", featureYAML("calc", "A sum is capped."))
	f.write("calc/calc.py", pyTags+"\nmodule_feature(\"calc\")\n\n\n@enforces_invariant(\"INV-1\")\ndef total(a, b):\n    return a + b\n")
	f.write("lattice/features/fees.yaml", featureYAML("fees", "A fee is never negative."))
	f.write("lattice/features/limits.yaml", featureYAML("limits", "A value never exceeds its cap."))
	f.write("lattice/features/web.yaml", featureYAML("web", "A label is never empty."))
	f.write("fees/fees.go", "package fees\n\n// @module-feature fees\n\n// @enforces fees:INV-1\nfunc Fee(n int) int {\n\treturn n / 10\n}\n")
	f.write("fees/fees_test.go", "package fees\n\nimport \"testing\"\n\n// @verifies fees:INV-1\nfunc TestFeeOfTen(t *testing.T) {\n\tif Fee(100) != 10 {\n\t\tt.Fatal(Fee(100))\n\t}\n}\n")
	f.write("limits/limits.go", "package limits\n\n// @module-feature limits\n\n// @enforces limits:INV-1\nfunc Max(a, b int) int {\n\tif a > b {\n\t\treturn a\n\t}\n\treturn b\n}\n")
	f.write("web/label.ts", "/** @module-feature web */\n\n/** @enforces web:INV-1 */\nexport function label(s: string): string {\n  return s;\n}\n")
	f.commit("base")
	f.git("checkout", "-q", "-b", "fix")

	f.write("fees/fees.go", "package fees\n\n// @module-feature fees\n\n// @enforces fees:INV-1\nfunc Fee(n int) int {\n\tif n < 0 {\n\t\treturn 0\n\t}\n\treturn n / 10\n}\n")
	f.write("fees/fees_test.go", "package fees\n\nimport \"testing\"\n\n// @verifies fees:INV-1\nfunc TestFeeOfTen(t *testing.T) {\n\tif Fee(100) != 10 {\n\t\tt.Fatal(Fee(100))\n\t}\n}\n\n"+
		"// @verifies fees:INV-1\nfunc TestFeeNeverNegative(t *testing.T) {\n\tif Fee(-50) < 0 {\n\t\tt.Fatal(Fee(-50))\n\t}\n}\n\n"+
		"// @verifies fees:INV-1\nfunc TestFeeOfTwenty(t *testing.T) {\n\tif Fee(200) != 20 {\n\t\tt.Fatal(Fee(200))\n\t}\n}\n")
	f.write("limits/limits.go", "package limits\n\n// @module-feature limits\n\n// @enforces limits:INV-1\nfunc Max(a, b int) int {\n\tif a > b {\n\t\treturn a\n\t}\n\treturn b\n}\n\n"+
		"// @enforces limits:INV-1\nfunc Cap(n, c int) int {\n\tif n > c {\n\t\treturn c\n\t}\n\treturn n\n}\n")
	f.write("limits/cap_test.go", "package limits\n\nimport \"testing\"\n\n// @verifies limits:INV-1\nfunc TestCapLimits(t *testing.T) {\n\tif Cap(5, 3) != 3 {\n\t\tt.Fatal(Cap(5, 3))\n\t}\n}\n")
	f.write("calc/calc.py", pyTags+"\nmodule_feature(\"calc\")\n\n\n@enforces_invariant(\"INV-1\")\ndef total(a, b):\n    return a + b\n\n\n"+
		"@enforces_invariant(\"INV-1\")\ndef cap(n, c):\n    return min(n, c)\n")
	f.write("calc/test_calc.py", "import os\nimport sys\n\nsys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))\n\nfrom calc import cap  # noqa: E402\n\n"+
		pyTags+"\n@verifies(\"calc:INV-1\")\ndef test_cap_limits():\n    assert cap(5, 3) == 3\n\n\nif __name__ == \"__main__\":\n    test_cap_limits()\n")
	f.write("web/label.test.ts", "/** @module-feature web */\n\n/** @verifies web:INV-1 */\ntest('label keeps text', () => {});\n")
	f.commit("fix")
	return f
}

// pyTags are no-op Lattice tags, so the Python fixture runs without the
// lattice package.
const pyTags = "def module_feature(*_):\n    return None\n\n\ndef enforces_invariant(*_):\n    return lambda fn: fn\n\n\ndef verifies(*_):\n    return lambda fn: fn\n\n"

func baseRuns(rep breaktest.FailsOnBaseReport) map[string]breaktest.BaseRun {
	m := map[string]breaktest.BaseRun{}
	for _, v := range rep.Verifiers {
		name := v.Verifier
		if i := strings.LastIndexAny(name, ".:"); i >= 0 {
			name = name[i+1:]
		}
		m[name] = v
	}
	return m
}

func runFailsOnBaseCLI(t *testing.T, dir string, args ...string) (breaktest.FailsOnBaseReport, string, error) {
	t.Helper()
	out, err := run(t, dir, newBreakCommand, append([]string{"--fails-on-base", "main"}, args...)...)
	var rep breaktest.FailsOnBaseReport
	if err == nil {
		if jerr := json.Unmarshal([]byte(out), &rep); jerr != nil {
			t.Fatalf("output is not a report: %v\n%s", jerr, out)
		}
	}
	return rep, out, err
}

// The fix's new verifiers run on main's code with the branch's test files: the
// one that catches the bug fails on base, the one that passes either way is
// flagged, the one calling a function main lacks does not compile there and
// the TypeScript one has no runner; both of those read unknown, never a pass.
// check --base then carries a note for the flagged one and nothing else
// changes. A dirty tree is refused unless allowed, and recorded.
// @verifies breaktest:INV-10
// @verifies breaktest:INV-2
// @verifies check:INV-11
func TestFailsOnBaseEndToEnd(t *testing.T) {
	clearCIEnv(t)
	f := baseProofFixture(t)
	dir := f.dir
	headSHA := f.git("rev-parse", "HEAD")
	before, beforeCode := f.check("--base", "main")

	// Dirty-tree rules are break's.
	f.write("scratch.txt", "x\n")
	if _, out, err := runFailsOnBaseCLI(t, dir); err == nil || !strings.Contains(out, "DIRTY_TREE") {
		t.Fatalf("dirty tree: %v\n%s", err, out)
	}
	rep, out, err := runFailsOnBaseCLI(t, dir, "--allow-dirty", "--promise", "web:INV-1")
	if err != nil || !rep.Dirty || len(rep.Verifiers) != 1 {
		t.Fatalf("allow-dirty: %v %+v\n%s", err, rep, out)
	}
	if err := os.Remove(filepath.Join(dir, "scratch.txt")); err != nil {
		t.Fatal(err)
	}

	status := f.git("status", "--porcelain")
	rep, out, err = runFailsOnBaseCLI(t, dir)
	if err != nil {
		t.Fatalf("fails-on-base: %v\n%s", err, out)
	}
	if rep.Schema != breaktest.FailsOnBaseSchema || rep.Head != headSHA || rep.Base != f.git("merge-base", "main", "HEAD") ||
		rep.BaseRev != "main" || rep.Dirty || rep.CIRun != "local" {
		t.Fatalf("stamps = %+v", rep)
	}
	runs := baseRuns(rep)
	if len(runs) != 5 {
		t.Fatalf("want the five new verifiers, got %+v", rep.Verifiers)
	}
	if _, ok := runs["TestFeeOfTen"]; ok {
		t.Errorf("an unchanged verifier was run: %+v", runs["TestFeeOfTen"])
	}
	if r := runs["TestFeeNeverNegative"]; r.Outcome != breaktest.FailsOnBase || r.Head != breaktest.HeadPass || r.Change != "added" {
		t.Errorf("the test of the fix: %+v", r)
	}
	if r := runs["TestFeeOfTwenty"]; r.Outcome != breaktest.PassesOnBase || r.Head != breaktest.HeadPass {
		t.Errorf("the test that passes either way: %+v", r)
	}
	if r := runs["TestCapLimits"]; r.Outcome != breaktest.Unknown || !strings.Contains(r.Reason, "does not compile on base") {
		t.Errorf("the test of a new function: %+v", r)
	}
	if r := runs["label-keeps-text"]; r.Outcome != breaktest.Unknown || !strings.Contains(r.Reason, "no runner configured") {
		t.Errorf("the TypeScript test: %+v (%v)", r, runs)
	}
	if r := runs["test_cap_limits"]; r.Outcome != breaktest.Unknown || r.Head != breaktest.HeadPass ||
		!strings.Contains(r.Reason, "does not load on base") || !strings.Contains(r.Reason, "cannot import name 'cap'") {
		t.Errorf("the Python test of a new name: %+v", r)
	}
	overlay := strings.Join(rep.Overlay, " ")
	if !strings.Contains(overlay, "fees/fees_test.go") || !strings.Contains(overlay, "limits/cap_test.go") ||
		strings.Contains(overlay, "fees/fees.go") || strings.Contains(overlay, "limits/limits.go") {
		t.Errorf("overlay = %v", rep.Overlay)
	}
	if got := f.git("status", "--porcelain"); got != status {
		t.Errorf("the working tree changed:\n%s\nwas\n%s", got, status)
	}
	if n := strings.Count(f.git("worktree", "list", "--porcelain"), "worktree "); n != 1 {
		t.Errorf("%d worktrees left", n)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "lattice", ".cache", "results", "fails-on-base.json")); err != nil ||
		!strings.Contains(string(data), `"schema": "lattice.fails_on_base/1"`) {
		t.Errorf("fails-on-base.json: %v", err)
	}

	after, afterCode := f.check("--base", "main")
	if after.Verdict.State != before.Verdict.State || after.MergeAllowed != before.MergeAllowed || afterCode != beforeCode {
		t.Fatalf("the result changed the verdict: %+v/%v vs %+v/%v", after.Verdict, after.MergeAllowed, before.Verdict, before.MergeAllowed)
	}
	if !after.Evidence.FailsOnBase.Counted {
		t.Fatalf("evidence = %+v", after.Evidence.FailsOnBase)
	}
	var notes []check.Note
	for _, row := range after.Promises {
		for _, n := range row.Notes {
			if n.Code == check.CodePassesOnBase {
				if row.Promise != "fees:INV-1" {
					t.Errorf("note on %s", row.Promise)
				}
				notes = append(notes, n)
			}
		}
	}
	if len(notes) != 1 || !strings.Contains(notes[0].Text, "TestFeeOfTwenty") || !strings.Contains(notes[0].Next, "--fails-on-base main") {
		t.Errorf("notes = %+v", notes)
	}
}
