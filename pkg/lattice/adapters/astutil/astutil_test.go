package astutil

import "testing"

// @verifies adapters:INV-1
func TestIsTestPath(t *testing.T) {
	for path, want := range map[string]bool{
		"tests/test_refund.py":                  true,
		"src/__tests__/cart.ts":                 true,
		"apps/client/e2e/tv.e2e.cjs":            true,
		"apps/client/e2e/helpers.ts":            true,
		"src/flows/login.e2e.ts":                true,
		"src/cart/total.ts":                     false,
		"src/e2emetrics/report.ts":              false,
		"src/testing-library/adapter.ts":        false,
		"supabase/functions/_shared/testing.ts": true,
		"src/test-utils.tsx":                    true,
		"app/test_helpers.py":                   true,
		"src/__mocks__/api.ts":                  true,
		"src/attesting.ts":                      false,
	} {
		if got := IsTestPath(path); got != want {
			t.Errorf("IsTestPath(%q) = %v, want %v", path, got, want)
		}
	}
}
