package breaktest

import (
	"slices"
	"testing"
)

// A runner that colours its output (Deno does, even into a pipe) still has its
// build markers read as a build failure, never as a failing test: a break that
// stops the code from type-checking says nothing about the verifiers.
// @verifies breaktest:INV-3
func TestClassifyStripsTerminalColour(t *testing.T) {
	deno := "Check file:///w/fn/handler.ts\n" +
		"\x1b[0m\x1b[1mTS18046 \x1b[0m[\x1b[0m\x1b[1m\x1b[31mERROR\x1b[0m]: 'size' is of type 'unknown'.\n" +
		"    if (size > MAX) {\n"
	got := classify("", 1, deno, false)
	if got.status != statusBuild {
		t.Fatalf("coloured Deno type error: status %v (%s), want a build failure", got.status, got.reason)
	}
	if want := "does not compile: Check file:///w/fn/handler.ts"; got.reason != want {
		t.Errorf("reason = %q, want %q", got.reason, want)
	}

	onlyTS := "\x1b[0m\x1b[1mTS2322 \x1b[0m[ERROR]: Type 'undefined' is not assignable to type 'string'.\n"
	if got := classify("", 1, onlyTS, false); got.status != statusBuild {
		t.Errorf("coloured TS2322 without a Check line: status %v, want a build failure", got.status)
	}

	// A coloured failing test is still a failing test.
	fail := "\x1b[31mFAIL\x1b[39m  test/a.test.ts > refuses a bad size\n\x1b[31mAssertionError\x1b[39m: expected 400 to be 200\n"
	if got := classify("", 1, fail, false); got.status != statusFail {
		t.Errorf("coloured test failure: status %v, want a failing test", got.status)
	}

	// An OSC hyperlink around a marker is stripped too.
	link := "\x1b]8;;file:///w/a.ts\x07src/a.ts:3:10: ERROR: Expected \";\"\x1b]8;;\x07\n"
	if got := classify("", 1, link, false); got.status != statusBuild {
		t.Errorf("esbuild error inside a hyperlink: status %v, want a build failure", got.status)
	}
}

// Runner processes are asked not to colour their output, unless the
// environment already says otherwise.
func TestNoColorEnv(t *testing.T) {
	env := noColorEnv([]string{"PATH=/bin"})
	if !slices.Contains(env, "NO_COLOR=1") || !slices.Contains(env, "FORCE_COLOR=0") {
		t.Errorf("env = %v, want NO_COLOR=1 and FORCE_COLOR=0 added", env)
	}
	kept := noColorEnv([]string{"NO_COLOR=", "FORCE_COLOR=3"})
	if slices.Contains(kept, "NO_COLOR=1") || slices.Contains(kept, "FORCE_COLOR=0") {
		t.Errorf("env = %v, want the existing NO_COLOR and FORCE_COLOR kept", kept)
	}
}
