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

// IsTestPath reports whether a path looks like test code by directory, or by
// an end-to-end marker in the file name (login.e2e.ts). An e2e/ directory
// holds browser or device scripts, never production code.
func IsTestPath(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, seg := range strings.Split(rel, "/") {
		switch seg {
		case "tests", "test", "__tests__", "e2e", "__e2e__":
			return true
		}
	}
	return strings.Contains(filepath.Base(rel), ".e2e.")
}
