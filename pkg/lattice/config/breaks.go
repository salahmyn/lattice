package config

// @module-feature workspace

import (
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Break is the `break:` block of config.yaml: how `lattice break` runs only
// the verifiers of one promise. Go needs none (it runs `go test -run`);
// every other language needs a runner, or a command template, or its
// outcomes read unknown ("no runner configured").
type Break struct {
	// Runners are the test runners of the project, one per framework. A
	// promise's verifiers are grouped by the first runner whose Match covers
	// their file and every group is run, so a promise whose verifiers span
	// vitest and Deno runs both.
	Runners []Runner `yaml:"runners,omitempty"`

	// Commands maps a language (typescript, javascript, python, php, go) to a
	// shell command, the fallback for a verifier no runner matches.
	// {files} is replaced by the verifier files and {filter} by a pattern
	// selecting the verifiers, each already shell-quoted:
	//
	//	typescript: npx vitest run {files} -t {filter}
	//	python:     python -m pytest {files} -k {filter}
	//
	// The filter is a regular expression ending in $ over the test titles
	// (typescript, javascript, php) or a -k expression over the test names
	// (python). The command runs in the temporary worktree, from the
	// directory that holds lattice/. {junit} is replaced by the path of a
	// JUnit report the command is expected to write.
	Commands map[string]string `yaml:"commands,omitempty"`

	// Setup lists shell commands run once in the temporary worktree, from the
	// directory that holds lattice/, before the first baseline. {repo} is
	// the path of the main checkout.
	Setup []string `yaml:"setup,omitempty"`

	// LinkNodeModules symlinks each node_modules directory of the main
	// checkout into the same relative path of the worktree. On by default;
	// set it to false to opt out.
	LinkNodeModules *bool `yaml:"link_node_modules,omitempty"`
}

// Runner is one test framework: the verifier files it runs and the command
// that runs them.
type Runner struct {
	Name string `yaml:"name" json:"name"`
	// Match are doublestar globs over the verifier files, relative to the
	// directory that holds lattice/.
	Match []string `yaml:"match" json:"match"`
	// Command is an argument list, run without a shell. {files} (as its own
	// element) expands to one argument per verifier file; {filter} and
	// {junit} are replaced where they appear. {junit} is the path of a JUnit
	// report the runner should write: with it, lattice counts the tests that
	// ran. File paths are relative to Dir when set.
	Command []string `yaml:"command" json:"command"`
	// Dir is the directory to run in, relative to the directory that holds
	// lattice/. Empty runs from that directory.
	Dir string `yaml:"dir,omitempty" json:"dir,omitempty"`
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

// RunnerFor returns the first runner whose Match covers file.
func (b Break) RunnerFor(file string) (Runner, bool) {
	for _, r := range b.Runners {
		if len(r.Command) == 0 {
			continue
		}
		for _, g := range r.Match {
			if ok, _ := doublestar.Match(g, file); ok {
				return r, true
			}
		}
	}
	return Runner{}, false
}

// LinksNodeModules reports whether node_modules directories are linked into
// the worktree (the default).
func (b Break) LinksNodeModules() bool {
	return b.LinkNodeModules == nil || *b.LinkNodeModules
}
