package breaktest

// @module-feature breaktest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// status is how one invocation of a test runner ended.
type status int

const (
	statusOK      status = iota // exit 0, and the verifiers ran
	statusFail                  // the verifiers ran and one failed
	statusBuild                 // the code did not compile or load
	statusTimeout               // the timeout ran out
	statusError                 // the runner could not run, or no verifier ran
)

// execResult is one runner invocation: its status, a one-line reason, and
// the output the reason came from. ran, failed and unchecked say what the
// invocation executed, where the runner lets that be known.
type execResult struct {
	status status
	reason string
	output string
	// ran are the verifiers (test FQNs) that executed.
	ran []string
	// failed are the names of the tests that failed.
	failed []string
	// unchecked says the number of tests that ran could not be checked: the
	// runner writes no JUnit report ({junit}) and is not Go.
	unchecked bool
	// executed counts the tests that ran, or the verifiers given to a runner
	// that cannot say.
	executed int
}

// Build markers say the code did not compile, load or transform. Only an
// explicit marker counts: a runtime failure with a stack trace is a failing
// test, so a break that causes one reads killed.
var (
	// esbuild and vitest/jest transform errors: "src/a.ts:3:10: ERROR: ...".
	reEsbuild = regexp.MustCompile(`(?m)^\s*\S+:\d+:\d+: ERROR: .*$|(?m)^.*Transform failed with \d+ error.*$`)
	// Deno type-check, resolution and check-file errors.
	reDeno = regexp.MustCompile(`(?m)^\s*(error: (TS\d+|Module not found).*|TS\d+ \[ERROR\].*|Check file:.*)$`)
	// pytest collection and import errors, and php parse errors, matched
	// case-insensitively as substrings.
	otherMarkers = []string{"error collecting", "indentationerror", "modulenotfounderror", "importerror", "parse error"}
)

// buildLine finds the explicit build marker in a non-Go runner's output and
// returns its line, "" when there is none.
func buildLine(out string) string {
	for _, re := range []*regexp.Regexp{reEsbuild, reDeno} {
		if m := re.FindString(out); m != "" {
			return strings.TrimSpace(m)
		}
	}
	for _, l := range strings.Split(out, "\n") {
		low := strings.ToLower(l)
		for _, m := range otherMarkers {
			if strings.Contains(low, m) {
				return strings.TrimSpace(l)
			}
		}
	}
	return ""
}

// classify decides how a runner invocation ended. exit is the process exit
// code (-1 when it did not start); timedOut says the timeout killed it. kind
// is "go" for `go test` output, "" for any other runner's.
// @enforces breaktest:INV-3
func classify(kind string, exit int, out string, timedOut bool) execResult {
	switch {
	case timedOut:
		return execResult{status: statusTimeout, reason: "timed out", output: out}
	case exit == 0:
		return execResult{status: statusOK, output: out}
	case exit < 0:
		return execResult{status: statusError, reason: "runner did not start: " + firstLine(out), output: out}
	}
	if kind == "go" {
		if l := goBuildLine(out); l != "" {
			return execResult{status: statusBuild, reason: "does not compile: " + clip(l), output: out}
		}
		return execResult{status: statusFail, reason: failingLine(out), output: out}
	}
	if l := buildLine(out); l != "" {
		return execResult{status: statusBuild, reason: "does not compile: " + clip(l), output: out}
	}
	return execResult{status: statusFail, reason: failingLine(out), output: out}
}

// goBuildLine finds the line `go test` prints when a package does not build
// ("FAIL<tab>pkg [build failed]"). Only a line that starts that way counts,
// so a test that prints those words is not mistaken for a build failure.
func goBuildLine(out string) string {
	for _, l := range strings.Split(out, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(l, "FAIL") && (strings.HasSuffix(t, "[build failed]") ||
			strings.HasSuffix(t, "[setup failed]") || strings.HasSuffix(t, "[vet failed]")) {
			return l
		}
		if strings.HasPrefix(l, "go: cannot find main module") ||
			(strings.HasPrefix(l, "package ") && strings.Contains(l, "build constraints exclude all Go files")) {
			return l
		}
	}
	return ""
}

// failHint finds a line that says something failed.
var failHint = regexp.MustCompile(`(?i)(fail|error|assert|panic|exception|✗|×)`)

// failingLine picks the line of output that says what failed: the first test
// failure, else the first line that mentions an error or a failure, else the
// last line with any text in it. It is never a line of punctuation.
func failingLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "--- FAIL") || strings.HasPrefix(t, "panic:") ||
			strings.HasPrefix(t, "FAILED") || strings.HasPrefix(t, "✗") || strings.HasPrefix(t, "FAIL ") {
			return clip(t)
		}
	}
	for _, l := range lines {
		if t := strings.TrimSpace(l); hasText(t) && failHint.MatchString(t) {
			return clip(t)
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); hasText(t) {
			return clip(t)
		}
	}
	return "the runner failed with no output"
}

// hasText reports whether s holds a letter or digit, so a line of brackets
// and punctuation is never taken for a reason.
func hasText(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return clip(s)
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:199] + "…"
	}
	return s
}

// command runs name with args in dir for at most timeout, in its own
// process group where the platform has one so a timeout also stops the test
// binaries the runner started.
func command(ctx context.Context, kind, dir string, timeout time.Duration, name string, args ...string) execResult {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	setProcessGroup(cmd)
	err := cmd.Run()
	out := buf.String()
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return execResult{status: statusTimeout, reason: fmt.Sprintf("timed out after %s", timeout), output: out}
		}
		return execResult{status: statusError, reason: "interrupted", output: out}
	}
	exit := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitCode()
		} else {
			exit = -1
			out = err.Error()
		}
	}
	return classify(kind, exit, out, false)
}

var (
	passLine = regexp.MustCompile(`(?m)^\s*--- PASS: (\S+)`)
	failLine = regexp.MustCompile(`(?m)^\s*--- FAIL: (\S+)`)
)

// goVerifiersPassed checks that every top-level test named ran and passed in
// `go test -v` output. A -run pattern that matches nothing exits 0, which
// would read as a verifier that survives every break.
func goVerifiersPassed(out string, tests []string) (string, bool) {
	passed := map[string]bool{}
	for _, m := range passLine.FindAllStringSubmatch(out, -1) {
		passed[m[1]] = true
	}
	for _, t := range tests {
		if !passed[t] {
			return t, false
		}
	}
	return "", true
}

// goFailedTests lists the tests `go test -v` reported as failed.
func goFailedTests(out string) []string {
	seen := map[string]bool{}
	var names []string
	for _, m := range failLine.FindAllStringSubmatch(out, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			names = append(names, m[1])
		}
	}
	return names
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
