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

// IsTestPath reports whether a path looks like test code by directory.
func IsTestPath(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, seg := range strings.Split(rel, "/") {
		switch seg {
		case "tests", "test", "__tests__":
			return true
		}
	}
	return false
}
