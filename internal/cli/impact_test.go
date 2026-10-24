package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// impactRepo is a git repository with an approved criterion whose enforcer
// a committed change edits, plus an uncommitted edit, an existing ledger
// and an open flag.
func impactRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "test")
	// No background maintenance: newer git detaches `maintenance run --auto`
	// after a commit, which writes into .git while the test snapshots it.
	git("config", "maintenance.auto", "false")
	git("config", "gc.auto", "0")
	write("lattice/config.yaml", "agentic:\n  llm:\n    enabled: false\n")
	write("lattice/adapters.yaml", "adapters:\n  go:\n    enabled: true\n")
	write("lattice/features/pay.yaml", "id: pay\nversion: 1\nstatus: production\npurpose: Pay.\nowners:\n  business: b\n  engineering: e\ninvariants:\n  - id: INV-1\n    statement: A payment is positive.\n")
	write("lattice/brds/brd.pay.yaml", "id: brd.pay\nversion: 1\nstatus: approved\ntitle: Pay\nbusiness_problem: Pay.\nsuccess_criteria:\n  - id: SC-1\n    statement: A payment is positive.\n    maps_to_invariant: pay:INV-1\nimplements_via: [pay]\n")
	write("lattice/.ledger/ledger.jsonl", `{"at":"2026-01-01T00:00:00Z","event":"check_run","actor":"ci","unit":"workspace","evidence":"seed"}`+"\n")
	write("lattice/.flags/flags.json", `[{"unit":"brd.pay/SC-1","reason":"seed","raised_by":"ci"}]`+"\n")
	write("pay/pay.go", "package pay\n\n// @module-feature pay\n\n// Positive guards a payment.\n// @enforces pay:INV-1\nfunc Positive(n int) bool {\n\treturn n > 0\n}\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	git("checkout", "-q", "-b", "change")
	write("pay/pay.go", "package pay\n\n// @module-feature pay\n\n// Positive guards a payment.\n// @enforces pay:INV-1\nfunc Positive(n int) bool {\n\treturn n >= 1\n}\n")
	git("commit", "-q", "-am", "change")
	write("pay/extra.go", "package pay\n\n// @module-feature pay\n\n// Extra is uncommitted and untracked.\nfunc Extra() int {\n\treturn 1\n}\n")
	// A tracked file whose stat no longer matches the index: a plain
	// `git status` or `git diff` would rewrite .git/index to refresh it.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "lattice/features/pay.yaml"), later, later); err != nil {
		t.Fatal(err)
	}
	return dir
}

// snapshot hashes every file under dir, .git included.
func snapshot(t *testing.T, dir string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = sha256.Sum256(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func runImpact(t *testing.T, dir string, jsonFlag bool, args ...string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	io := &IO{Repo: dir, JSON: jsonFlag, Out: &out, Err: &errOut}
	cmd := newImpactCommand(io)
	cmd.SetArgs(args)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("impact %v: %v\n%s%s", args, err, out.String(), errOut.String())
	}
	return out.String()
}

// lattice impact writes nothing: the ledger, the flags, every tracked file
// and git's own files are byte-identical after a run in every format.
// @verifies impact:INV-1
func TestImpactIsReadOnly(t *testing.T) {
	dir := impactRepo(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp) // where the private index copy goes
	before := snapshot(t, dir)

	runImpact(t, dir, false, "--since", "main")
	runImpact(t, dir, false, "--since", "main", "--format", "markdown")
	runImpact(t, dir, true, "--since", "main")
	runImpact(t, dir, false, "--since", "main", "--format", "json", "--include-drafts=false", "--max-symbols", "1")
	// A failing run (unknown ref) also cleans up after itself.
	var out, errOut bytes.Buffer
	failing := newImpactCommand(&IO{Repo: dir, Out: &out, Err: &errOut})
	failing.SetArgs([]string{"--since", "no-such-ref"})
	failing.SetOut(&out)
	failing.SetErr(&errOut)
	if err := failing.Execute(); err == nil {
		t.Error("an unknown --since ref should fail")
	}

	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
	after := snapshot(t, dir)
	if !reflect.DeepEqual(before, after) {
		for p, h := range after {
			if before[p] != h {
				t.Errorf("%s changed or appeared", p)
			}
		}
		for p := range before {
			if _, ok := after[p]; !ok {
				t.Errorf("%s disappeared", p)
			}
		}
	}
}

// The JSON report uses snake_case fields, finds the approved criterion, and
// degrades to "code graph unavailable" without a SCIP index.
func TestImpactJSON(t *testing.T) {
	dir := impactRepo(t)
	var rep map[string]interface{}
	if err := json.Unmarshal([]byte(runImpact(t, dir, true, "--since", "main")), &rep); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"since", "base", "head", "uncommitted", "changed_files", "changed_symbols",
		"affected_criteria", "drafts_included", "draft_criteria", "removed_enforcement", "manifest_diff", "code_rollup"} {
		if _, ok := rep[k]; !ok {
			t.Errorf("JSON lacks %q", k)
		}
	}
	crit := rep["affected_criteria"].([]interface{})
	if len(crit) != 1 || crit[0].(map[string]interface{})["criterion"] != "SC-1" {
		t.Errorf("affected criteria = %v", crit)
	}
	cr := rep["code_rollup"].(map[string]interface{})
	if cr["available"] != false || !strings.Contains(cr["note"].(string), "code graph unavailable") {
		t.Errorf("code roll-up = %v", cr)
	}
	syms := rep["changed_symbols"].([]interface{})
	var names []string
	for _, s := range syms {
		names = append(names, s.(map[string]interface{})["symbol"].(string))
	}
	if !reflect.DeepEqual(names, []string{"pay.Extra", "pay.Positive"}) {
		t.Errorf("changed symbols = %v (the untracked file is part of the working tree)", names)
	}

	md := runImpact(t, dir, false, "--since", "main", "--format", "markdown")
	if !strings.HasPrefix(md, "<!-- lattice-impact -->") || !strings.Contains(md, "**`brd.pay` / SC-1**") {
		t.Errorf("markdown:\n%s", md)
	}
}

func TestImpactRejectsUnknownFormat(t *testing.T) {
	dir := impactRepo(t)
	var out, errOut bytes.Buffer
	cmd := newImpactCommand(&IO{Repo: dir, Out: &out, Err: &errOut})
	cmd.SetArgs([]string{"--since", "main", "--format", "yaml"})
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	if err := cmd.Execute(); err == nil || !strings.Contains(errOut.String(), "--format") {
		t.Errorf("want a --format error, got %v: %s", err, errOut.String())
	}
}
