package golang

import "testing"

// A test-support package by the standard library's convention (its name
// ends in test and it imports testing) is test code; a package that has
// only one of the two is production.
// @verifies adapters.go:INV-6
func TestTestSupportPackages(t *testing.T) {
	for _, tc := range []struct {
		name, file, src string
		want            bool
	}{
		{"helper package importing testing", "pkg/sig/sigtest/sigtest.go",
			"package sigtest\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc NewKey(t testing.TB) { _ = os.Args }\n", true},
		{"single import form", "pkg/web/webtest/server.go",
			"package webtest\n\nimport \"testing\"\n\nfunc Serve(t *testing.T) {}\n", true},
		{"name ends in test, no testing import", "pkg/release/latest/latest.go",
			"package latest\n\nimport \"os\"\n\nfunc Version() string { return os.Getenv(\"V\") }\n", false},
		{"imports testing, ordinary name", "pkg/flags/flags.go",
			"package flags\n\nimport \"testing\"\n\nfunc InTest() bool { return testing.Testing() }\n", false},
		{"bare package named test", "pkg/x/test.go",
			"package test\n\nimport \"testing\"\n\nfunc H(t *testing.T) {}\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mod := parse(t, tc.file, tc.src)
			if len(mod.Symbols) == 0 {
				t.Fatalf("no symbols parsed")
			}
			for _, s := range mod.Symbols {
				if s.IsTest != tc.want {
					t.Errorf("%s: IsTest = %v, want %v", s.FQN, s.IsTest, tc.want)
				}
			}
		})
	}
}
