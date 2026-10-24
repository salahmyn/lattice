package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fmt rewrites a hand-ordered BRD, leaves a commented feature untouched
// and says why, and --check exits non-zero without writing.
func TestFmtCommand(t *testing.T) {
	repo := t.TempDir()
	write := func(rel, body string) string {
		p := filepath.Join(repo, "lattice", rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("config.yaml", "{}\n")
	brdPath := write("brds/brd.x.yaml", "id: brd.x\nversion: 1\nstatus: draft\ntitle: X\nbusiness_problem: p\nprovenance:\n  source: human\nopen_questions:\n  - q\n")
	featPath := write("features/f.yaml", "# kept by hand\nid: f\nversion: 1\n")
	before, _ := os.ReadFile(brdPath)

	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		cmd := newFmtCommand(&IO{Repo: repo, Out: &out, Err: &out})
		cmd.SetArgs(args)
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		err := cmd.Execute()
		return out.String(), err
	}

	out, err := run("--check")
	if !IsExit(err) || !strings.Contains(out, "would format lattice/brds/brd.x.yaml") {
		t.Fatalf("--check: err %v, out:\n%s", err, out)
	}
	if after, _ := os.ReadFile(brdPath); !bytes.Equal(after, before) {
		t.Error("--check wrote the file")
	}
	if !strings.Contains(out, "skipped lattice/features/f.yaml: a comment at line 2 would be dropped") {
		t.Errorf("the commented feature should be reported as skipped:\n%s", out)
	}

	if out, err = run(); err != nil {
		t.Fatalf("fmt: %v\n%s", err, out)
	}
	after, _ := os.ReadFile(brdPath)
	if s := string(after); strings.Index(s, "open_questions:") > strings.Index(s, "provenance:") {
		t.Errorf("not rewritten in field order:\n%s", s)
	}
	if feat, _ := os.ReadFile(featPath); !strings.HasPrefix(string(feat), "# kept by hand") {
		t.Error("the commented feature was rewritten")
	}
	if out, err = run("--check"); err != nil {
		t.Errorf("second --check should pass: %v\n%s", err, out)
	}
}
