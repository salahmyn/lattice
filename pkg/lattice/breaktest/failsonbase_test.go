package breaktest

import (
	"strings"
	"testing"
)

// A non-Go test that fails on the base because an import does not resolve
// did not load there: it reads unknown with the line that says so. A real
// failure in the test body (an assertion, a TypeError) reads fails_on_base.
// @verifies breaktest:INV-10
func TestJudgeBaseLoadFailures(t *testing.T) {
	head := execResult{status: statusOK, ran: []string{"v"}, executed: 1}
	loads := map[string]string{
		"node esm":         "SyntaxError: The requested module './fees.js' does not provide an export named 'cap'",
		"export named":     "file:///w/fees.test.mjs:1\n  import { cap } from './fees.js'\n           ^^^\ndoes not provide an export named 'cap'",
		"commonjs":         "Error: Cannot find module './limits'\nRequire stack:\n- /w/limits.test.js",
		"node code":        "node:internal/modules/esm/resolve:1 code: 'ERR_MODULE_NOT_FOUND'",
		"vite":             `Error: Failed to resolve import "./cap" from "src/fees.test.ts". Does the file exist?`,
		"webpack/deno":     "error: Module not found \"file:///w/cap.ts\".",
		"tsc member":       "src/fees.test.ts(1,10): error TS2305: Module '\"./fees\"' has no exported member 'cap'.",
		"tsc 2307":         "error: TS2307 [ERROR]: Cannot resolve './cap'",
		"tsc 2724":         "src/a.test.ts(2,10): error TS2724: '\"./a\"' has no exported member named 'Cap'. Did you mean 'cap'?",
		"deno ts2305":      "error: TS2305 [ERROR]: Module '\"./fees.ts\"' has no exported member 'cap'.",
		"python import":    "Traceback (most recent call last):\n  File \"test_fees.py\", line 1, in <module>\nImportError: cannot import name 'cap' from 'fees'",
		"python module":    "ModuleNotFoundError: No module named 'limits'",
		"pytest collect":   "==== ERRORS ====\n____ ERROR collecting tests/test_fees.py ____",
		"cannot import as": "E   cannot import name 'cap' from partially initialized module 'fees'",
	}
	for name, out := range loads {
		for _, st := range []status{statusFail, statusBuild} {
			o, why, _ := judgeBase(false, head, execResult{status: st, reason: "failed: v", output: out}, "v")
			if o != Unknown || !strings.HasPrefix(why, "does not load on base") {
				t.Errorf("%s (status %d): %s %q", name, st, o, why)
			}
		}
	}

	fails := map[string]string{
		"assertion":  "AssertionError: expected 5 to equal 0\n  at fees.test.ts:9:12",
		"type error": "TypeError: Cannot read properties of undefined (reading 'amount')\n  at fees.test.ts:4:20",
		"pytest":     "E       assert -5 == 0\nFAILED test_fees.py::test_never_negative",
	}
	for name, out := range fails {
		o, why, _ := judgeBase(false, head, execResult{status: statusFail, reason: "failed: v", output: out}, "v")
		if o != FailsOnBase {
			t.Errorf("%s: %s %q", name, o, why)
		}
	}

	// A Go test's own message is not a load marker: go test reports build
	// failures by status.
	if o, _, _ := judgeBase(true, head, execResult{status: statusFail, reason: "failed: v", output: "--- FAIL: TestX\n    x_test.go:3: Cannot find module in cache"}, "v"); o != FailsOnBase {
		t.Errorf("go failure = %s", o)
	}
}
