package cli

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// preflightProblem and preflightRefusal are the wire form of a refused
// preflight, as a script reads it.
type preflightProblem struct {
	Code   string `json:"code"`
	Key    string `json:"key"`
	Detail string `json:"detail"`
	Next   string `json:"next"`
}

type preflightRefusal struct {
	Error      string             `json:"error"`
	Code       string             `json:"code"`
	NextAction string             `json:"next_action"`
	Problems   []preflightProblem `json:"problems"`
}

// refusal requires a preflight refusal: exit 2 (a setup error, not a
// verdict) and one structured JSON error whose every problem has its code,
// config key, detail and next step.
func refusal(t *testing.T, out string, err error) preflightRefusal {
	t.Helper()
	var ec exitCodeError
	if !errors.As(err, &ec) || ec.code != 2 {
		t.Fatalf("want exit 2 (setup error), got %v\n%s", err, out)
	}
	var r preflightRefusal
	if jerr := json.Unmarshal([]byte(out), &r); jerr != nil {
		t.Fatalf("refusal is not one JSON error: %v\n%s", jerr, out)
	}
	if r.Code != "PREFLIGHT_FAILED" || !strings.Contains(r.NextAction, "--no-preflight") || len(r.Problems) == 0 {
		t.Fatalf("refusal = %+v", r)
	}
	for _, p := range r.Problems {
		if p.Code == "" || p.Key == "" || p.Detail == "" || p.Next == "" {
			t.Errorf("a problem lacks its code, key, detail or next: %+v", p)
		}
	}
	return r
}

func hasProblem(r preflightRefusal, code, key string) bool {
	for _, p := range r.Problems {
		if p.Code == code && p.Key == key {
			return true
		}
	}
	return false
}

// closedPort is the address of a listener the test opened and closed, so
// nothing answers there.
func closedPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot listen on loopback: " + err.Error())
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// A runner whose executable is not on PATH stops break before anything runs:
// break.setup, which runs before the first baseline, leaves no marker, and no
// breaks.json is written. With --no-preflight the same config runs, so the
// marker is proof the refusal came first.
// @verifies cli:INV-4
func TestBreakPreflightRefusesMissingRunnerBeforeAnyRun(t *testing.T) {
	f := newCheckFixture(t)
	marker := filepath.Join(t.TempDir(), "setup-ran")
	f.write("lattice/config.yaml", "agentic:\n  llm:\n    enabled: false\nbreak:\n  setup:\n    - touch '"+marker+"'\n"+
		"  runners:\n    - name: web\n      match: [\"**/*.test.ts\"]\n      command: [\"no-such-runner-7f3a\", \"{files}\"]\n")
	f.commit("runner config")
	breaks := filepath.Join(f.dir, "lattice", ".cache", "results", "breaks.json")

	out, err := run(t, f.dir, newBreakCommand)
	r := refusal(t, out, err)
	if !hasProblem(r, "EXEC_NOT_FOUND", "break.runners[0].command") {
		t.Fatalf("missing runner not reported: %+v", r.Problems)
	}
	if pathExists(marker) || pathExists(breaks) {
		t.Fatalf("something ran before the refusal (marker %v, breaks.json %v)", pathExists(marker), pathExists(breaks))
	}

	// A dirty tree is reported in the same refusal, not after it.
	f.write("scratch.txt", "x\n")
	out, err = run(t, f.dir, newBreakCommand)
	r = refusal(t, out, err)
	if !hasProblem(r, "DIRTY_TREE", "--allow-dirty") || !hasProblem(r, "EXEC_NOT_FOUND", "break.runners[0].command") {
		t.Fatalf("want the dirty tree and the runner together: %+v", r.Problems)
	}
	if err := os.Remove(filepath.Join(f.dir, "scratch.txt")); err != nil {
		t.Fatal(err)
	}

	out, err = run(t, f.dir, newBreakCommand, "--no-preflight")
	if err != nil {
		t.Fatalf("--no-preflight: %v\n%s", err, out)
	}
	if !pathExists(marker) || !pathExists(breaks) {
		t.Fatalf("--no-preflight did not run break (marker %v, breaks.json %v)\n%s", pathExists(marker), pathExists(breaks), out)
	}
}

// One refusal lists every problem: two missing executables, a closed port, an
// address that is not host:port and, where node is installed, a Node minimum
// package.json declares that it does not meet. runs-clean and demonstrate
// refuse the same way; no install step ran. --no-preflight runs the steps.
// @verifies cli:INV-4
func TestRunsCleanPreflightReportsEveryProblem(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "install-ran")
	addr := closedPort(t)
	build := "no-such-build-9c2e --prod"
	_, nodeErr := exec.LookPath("node")
	if nodeErr == nil {
		build += " && node --version"
		writeFile(t, filepath.Join(dir, "package.json"), `{"name": "app", "engines": {"node": ">=999"}}`)
	}
	writeFile(t, filepath.Join(dir, "lattice", "config.yaml"), "agentic:\n  llm:\n    enabled: false\nruntime:\n"+
		"  clean_install: \"touch '"+marker+"' && no-such-install-41bd ci\"\n"+
		"  build: \""+build+"\"\n"+
		"  services:\n    - { name: db, address: \""+addr+"\" }\n    - { name: cache, address: \"no-port\" }\n")

	for _, mk := range []struct {
		name string
		cmd  func(*IO) *cobra.Command
	}{{"runs-clean", newRunsCleanCommand}, {"demonstrate", newDemonstrateCommand}} {
		out, err := run(t, dir, mk.cmd)
		r := refusal(t, out, err)
		for _, want := range [][2]string{
			{"EXEC_NOT_FOUND", "runtime.clean_install"},
			{"EXEC_NOT_FOUND", "runtime.build"},
			{"SERVICE_UNREACHABLE", "runtime.services[0].address"},
			{"SERVICE_BAD_ADDRESS", "runtime.services[1].address"},
		} {
			if !hasProblem(r, want[0], want[1]) {
				t.Errorf("%s: %s at %s not reported: %+v", mk.name, want[0], want[1], r.Problems)
			}
		}
		if nodeErr == nil && !hasProblem(r, "VERSION_TOO_OLD", "runtime.build") {
			t.Errorf("%s: node below engines.node not reported: %+v", mk.name, r.Problems)
		}
		if !strings.Contains(r.Error, "nothing was run") {
			t.Errorf("%s: error = %q", mk.name, r.Error)
		}
		if pathExists(marker) {
			t.Fatalf("%s: the install step ran before the refusal", mk.name)
		}
	}

	out, err := run(t, dir, newRunsCleanCommand, "--no-preflight")
	var ec exitCodeError
	if err == nil || errors.As(err, &ec) || !strings.Contains(out, "V0_FAILED") {
		t.Fatalf("--no-preflight should run the steps and fail V0: %v\n%s", err, out)
	}
	if !pathExists(marker) {
		t.Fatal("--no-preflight did not run the install step")
	}
}

// A command that runs docker needs the daemon: a docker CLI whose daemon
// does not answer is refused before the step that would use it runs.
// @verifies cli:INV-4
func TestRunsCleanPreflightNeedsTheDockerDaemon(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "compose-ran")
	// A docker CLI with no daemon behind it: `docker version` fails the way
	// the real one does; anything else would start containers, and leaves
	// the marker.
	writeFile(t, filepath.Join(bin, "docker"), "#!/bin/sh\n"+
		"if [ \"$1\" = version ]; then echo 'Cannot connect to the Docker daemon at unix:///var/run/docker.sock' >&2; exit 1; fi\n"+
		"touch '"+marker+"'\n")
	if err := os.Chmod(filepath.Join(bin, "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lattice", "config.yaml"), "agentic:\n  llm:\n    enabled: false\nruntime:\n"+
		"  clean_install: \"docker compose up -d\"\n  build: \"true\"\n")
	out, err := run(t, dir, newRunsCleanCommand)
	r := refusal(t, out, err)
	if !hasProblem(r, "DOCKER_UNAVAILABLE", "runtime.clean_install") {
		t.Fatalf("a docker without its daemon not reported: %+v", r.Problems)
	}
	if hasProblem(r, "EXEC_NOT_FOUND", "runtime.clean_install") {
		t.Errorf("docker is on PATH, yet reported missing: %+v", r.Problems)
	}
	if pathExists(marker) {
		t.Fatal("docker compose ran before the refusal")
	}
}

// checkRun is the part of lattice.check/1 the preflight note touches.
type checkRun struct {
	Verdict struct {
		State string `json:"state"`
	} `json:"verdict"`
	MergeAllowed *bool `json:"merge_allowed"`
	Preflight    *struct {
		Text     string             `json:"text"`
		Problems []preflightProblem `json:"problems"`
	} `json:"preflight"`
}

// runCheck runs lattice check and returns its exit code and the output.
func runCheck(t *testing.T, dir string, asJSON bool, args ...string) (int, string) {
	t.Helper()
	var out strings.Builder
	io := &IO{Repo: dir, JSON: asJSON, Out: &out, Err: &out, Actor: "test"}
	cmd := newCheckCommand(io)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs(args)
	err := cmd.Execute()
	var ec exitCodeError
	switch {
	case err == nil:
		return 0, out.String()
	case errors.As(err, &ec):
		return ec.code, out.String()
	}
	t.Fatalf("check %v: %v\n%s", args, err, out.String())
	return 0, ""
}

// check runs no test: it only lists the touched verifiers by break.runners.
// A runner whose executable or dir is missing is a note in the JSON, text and
// Markdown reports, and the verdict, merge_allowed and the exit code are
// those of the same check with --no-preflight, which carries no note.
// @verifies cli:INV-4
func TestCheckPreflightNotesMissingRunner(t *testing.T) {
	f := newCheckFixture(t)
	f.write("lattice/config.yaml", "agentic:\n  llm:\n    enabled: false\nbreak:\n"+
		"  runners:\n    - name: web\n      match: [\"**/*.test.ts\"]\n      command: [\"no-such-runner-7f3a\", \"{files}\"]\n"+
		"      dir: no-such-dir\n")
	f.commit("runner config")

	decode := func(out string) checkRun {
		t.Helper()
		var r checkRun
		if err := json.Unmarshal([]byte(out), &r); err != nil || r.MergeAllowed == nil {
			t.Fatalf("not a check report: %v\n%s", err, out)
		}
		return r
	}
	quietCode, quietOut := runCheck(t, f.dir, true, "--base", "HEAD", "--no-preflight")
	quiet := decode(quietOut)
	if quiet.Preflight != nil {
		t.Errorf("--no-preflight still carries the note: %+v", quiet.Preflight)
	}

	code, out := runCheck(t, f.dir, true, "--base", "HEAD")
	if code == 2 {
		t.Fatalf("check refused instead of giving its verdict:\n%s", out)
	}
	r := decode(out)
	if code != quietCode || r.Verdict.State != quiet.Verdict.State || *r.MergeAllowed != *quiet.MergeAllowed {
		t.Errorf("the note changed the outcome: exit %d %s merge %v, want exit %d %s merge %v",
			code, r.Verdict.State, *r.MergeAllowed, quietCode, quiet.Verdict.State, *quiet.MergeAllowed)
	}
	if r.Preflight == nil || r.Preflight.Text == "" {
		t.Fatalf("no preflight note:\n%s", out)
	}
	note := preflightRefusal{Problems: r.Preflight.Problems}
	if !hasProblem(note, "EXEC_NOT_FOUND", "break.runners[0].command") || !hasProblem(note, "PATH_NOT_FOUND", "break.runners[0].dir") {
		t.Fatalf("want the runner and its dir together: %+v", r.Preflight.Problems)
	}
	for _, p := range r.Preflight.Problems {
		if p.Code == "" || p.Key == "" || p.Detail == "" || p.Next == "" {
			t.Errorf("a problem lacks its code, key, detail or next: %+v", p)
		}
	}

	for _, args := range [][]string{{"--base", "HEAD"}, {"--base", "HEAD", "--markdown"}} {
		c, out := runCheck(t, f.dir, false, args...)
		if c != quietCode || !strings.Contains(out, "no-such-runner-7f3a is not on PATH") || !strings.Contains(out, "break.runners[0].dir") {
			t.Errorf("check %v: exit %d (want %d), note missing:\n%s", args, c, quietCode, out)
		}
		_, quietOut := runCheck(t, f.dir, false, append(args, "--no-preflight")...)
		if strings.Contains(quietOut, "no-such-runner-7f3a") {
			t.Errorf("check %v --no-preflight still shows the note:\n%s", args, quietOut)
		}
	}
}

// doctor lists the same checks, and a failed one makes it not ok.
// @verifies cli:INV-4
func TestDoctorListsPreflightChecks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lattice", "config.yaml"),
		"agentic:\n  llm:\n    enabled: false\nruntime:\n  build: \"no-such-build-9c2e\"\n  services:\n    - { address: \""+closedPort(t)+"\" }\n")
	out, err := run(t, dir, newDoctorCommand)
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	var rep doctorReport
	if jerr := json.Unmarshal([]byte(out), &rep); jerr != nil {
		t.Fatalf("doctor output: %v\n%s", jerr, out)
	}
	got := map[string]bool{}
	for _, c := range rep.Checks {
		if c.Category == "preflight" {
			got[c.Name] = c.Found
		}
	}
	for _, name := range []string{"runs-clean runtime.build", "runs-clean runtime.services[0].address"} {
		if found, ok := got[name]; !ok || found {
			t.Errorf("doctor check %q: listed %v, found %v (%+v)", name, ok, found, rep.Checks)
		}
	}
	if rep.OK {
		t.Error("doctor is ok with a failed preflight check")
	}
}
