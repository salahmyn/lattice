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
// the output the reason came from.
type execResult struct {
	status status
	reason string
	output string
}

// buildMarkers say the code did not compile, load or transform, in a
// runner's output. A break that does that is unknown, never killed.
var buildMarkers = map[string][]string{
	// vitest, jest and esbuild transform errors, TypeScript, Node, then
	// pytest collection and import errors, then php
	"": {"transform failed", "esbuild", "syntaxerror", "error ts", "failed to parse source",
		"cannot find module", "failed to resolve import", "unexpected token",
		"error collecting", "indentationerror", "modulenotfounderror", "importerror",
		"parse error"},
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
		return execResult{status: statusFail, reason: failingLine(out, ""), output: out}
	}
	low := strings.ToLower(out)
	for _, m := range buildMarkers[kind] {
		if strings.Contains(low, m) {
			return execResult{status: statusBuild, reason: "does not compile: " + failingLine(out, m), output: out}
		}
	}
	return execResult{status: statusFail, reason: failingLine(out, ""), output: out}
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

// failingLine picks the line of output that says what failed: the line with
// the marker, else the first test failure, else the last line.
func failingLine(out, marker string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, l := range lines {
		if marker != "" && strings.Contains(strings.ToLower(l), marker) {
			return clip(l)
		}
	}
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "--- FAIL") || strings.HasPrefix(t, "panic:") ||
			strings.HasPrefix(t, "FAILED") || strings.HasPrefix(t, "✗") || strings.HasPrefix(t, "FAIL ") {
			return clip(t)
		}
	}
	return clip(lines[len(lines)-1])
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

var passLine = regexp.MustCompile(`(?m)^\s*--- PASS: (\S+)`)

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

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
