// Package astutil holds small helpers shared by the tree-sitter language
// adapters.
package astutil

import (
	"path/filepath"
	"strings"
)

// ModulePath turns a repo-relative source path into a dotted module path:
// "src/checkout/refund/service.py" -> "src.checkout.refund.service".
func ModulePath(rel string) string {
	rel = filepath.ToSlash(rel)
	rel = strings.TrimSuffix(rel, filepath.Ext(rel))
	rel = strings.Trim(rel, "/")
	return strings.ReplaceAll(rel, "/", ".")
}

// IsTestPath reports whether a path looks like test code by directory
// (tests/, __tests__/, e2e/, __mocks__/, __fixtures__/, testdata/), by an
// end-to-end marker in the file name (login.e2e.ts), or by a test-support
// name (testing.ts, test-utils.ts, test_helpers.py). An e2e/ directory holds
// browser or device scripts, never production code.
func IsTestPath(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, seg := range strings.Split(rel, "/") {
		switch seg {
		case "tests", "test", "__tests__", "e2e", "__e2e__", "__mocks__", "__fixtures__", "testdata":
			return true
		}
	}
	base := filepath.Base(rel)
	if strings.Contains(base, ".e2e.") {
		return true
	}
	// Test-support modules by name: testing.ts, test-utils.ts, test_helpers.py.
	stem := strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))
	switch stem {
	case "testing", "test-utils", "test_utils", "testutils", "test-helpers", "test_helpers", "testhelpers":
		return true
	}
	return false
}
