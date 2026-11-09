package config

// @module-feature workspace

import "strings"

// Break is the `break:` block of config.yaml: how `lattice break` runs only
// the verifiers of one promise. Go needs none (it runs `go test -run`);
// every other language needs a command template, or its outcomes read
// unknown ("no runner configured").
type Break struct {
	// Commands maps a language (typescript, javascript, python, php, go) to a
	// shell command. {files} is replaced by the verifier files and {filter}
	// by a pattern selecting the verifiers, each already shell-quoted:
	//
	//	typescript: npx vitest run {files} -t {filter}
	//	python:     python -m pytest {files} -k {filter}
	//
	// The filter is a regular expression ending in $ over the test titles
	// (typescript, javascript, php) or a -k expression over the test names
	// (python). The command runs in the temporary worktree, from the
	// directory that holds lattice/.
	Commands map[string]string `yaml:"commands,omitempty"`
}

// Command returns the command template for a language. JavaScript falls back
// to the TypeScript template, which runs the same test runners.
func (b Break) Command(language string) string {
	if c := strings.TrimSpace(b.Commands[language]); c != "" {
		return c
	}
	if language == "javascript" {
		return strings.TrimSpace(b.Commands["typescript"])
	}
	return ""
}
