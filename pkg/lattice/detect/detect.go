// Package detect inspects a project root and guesses its primary
// language and framework. Used by `lattice init` and the v0.5.0
// onboarding wizard to suggest the right adapters and plugin packages
// without making the user type them.
//
// Detection is intentionally conservative: it scores candidates from
// presence of manifest files (composer.json, package.json, etc.) plus
// signature paths (bootstrap/app.php, manage.py, etc.) and returns the
// highest-confidence guess. Confidence is exposed so the caller (UI)
// can flag low-confidence guesses for human confirmation.
package detect

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Language is the detected programming language. Kept as a typed
// alias for documentation; the schema is open — new languages add
// a new constant + scoring rule.
type Language string

const (
	LanguagePHP        Language = "php"
	LanguagePython     Language = "python"
	LanguageTypeScript Language = "typescript"
	LanguageJavaScript Language = "javascript"
	LanguageGo         Language = "go"
	LanguageRuby       Language = "ruby"
	LanguageRust       Language = "rust"
	LanguageJava       Language = "java"
)

// Framework is the detected primary application framework. Includes
// the "none" sentinel for projects that use a language but no clear
// framework (e.g. a plain Python script collection).
type Framework string

const (
	FrameworkNone     Framework = ""
	FrameworkLaravel  Framework = "laravel"
	FrameworkSymfony  Framework = "symfony"
	FrameworkDjango   Framework = "django"
	FrameworkFastAPI  Framework = "fastapi"
	FrameworkFlask    Framework = "flask"
	FrameworkExpress  Framework = "express"
	FrameworkNestJS   Framework = "nestjs"
	FrameworkNextJS   Framework = "nextjs"
	FrameworkRails    Framework = "rails"
	FrameworkSpring   Framework = "spring"
)

// Detection is the result of inspecting a project root.
type Detection struct {
	Root       string    `json:"root"`
	Language   Language  `json:"language"`
	Framework  Framework `json:"framework"`
	Confidence string    `json:"confidence"` // "high" | "medium" | "low" | "none"

	// CodeRoots are the directories that look like they hold source
	// (not vendor/, not test fixtures). Suggested for workspace.yaml.
	CodeRoots []string `json:"code_roots,omitempty"`

	// RequiredPackages is the list of language-ecosystem packages
	// Lattice needs to index this stack: e.g. scip-php for PHP/Laravel,
	// scip-python for Django/FastAPI.
	RequiredPackages []Package `json:"required_packages,omitempty"`

	// Evidence is the human-readable list of signals (file paths) that
	// fed the score. Surfaced in the CLI and the wizard so the user
	// understands *why* we guessed what we did.
	Evidence []string `json:"evidence,omitempty"`
}

// Package is one external dependency Lattice needs installed to index
// the detected stack. Manager is the package-manager command verb,
// chosen so a one-shot install string can be assembled.
type Package struct {
	Name    string `json:"name"`
	Manager string `json:"manager"` // composer | pip | npm | gem | cargo | go
	Reason  string `json:"reason"`  // short justification, surfaced in UI
}

// Detect inspects root and returns the highest-confidence guess.
// A root with no recognised manifest returns Language="", Framework="",
// Confidence="none" — callers should treat that as "ask the user".
func Detect(root string) Detection {
	root = filepath.Clean(root)
	det := Detection{Root: root}

	// Score each (language, framework) pair from manifest + signature
	// evidence. The highest-scoring pair wins; ties resolve by
	// declaration order in the scoring tables.
	type score struct {
		lang     Language
		fw       Framework
		score    int
		evidence []string
	}
	var scores []score

	// --- PHP / Laravel-or-Symfony ---
	if exists(root, "composer.json") {
		ev := []string{"composer.json"}
		s := score{lang: LanguagePHP, score: 10, evidence: ev}

		// Inspect composer.json's require map for framework signals.
		req := composerRequires(filepath.Join(root, "composer.json"))
		switch {
		case req["laravel/framework"]:
			s.fw = FrameworkLaravel
			s.score += 30
			s.evidence = append(s.evidence, "composer.json requires laravel/framework")
		case req["symfony/framework-bundle"], req["symfony/symfony"]:
			s.fw = FrameworkSymfony
			s.score += 30
			s.evidence = append(s.evidence, "composer.json requires symfony/*")
		}
		if exists(root, "artisan") {
			s.fw = FrameworkLaravel
			s.score += 20
			s.evidence = append(s.evidence, "artisan script present")
		}
		if exists(root, "bootstrap/app.php") {
			s.fw = FrameworkLaravel
			s.score += 10
			s.evidence = append(s.evidence, "bootstrap/app.php")
		}
		scores = append(scores, s)
	}

	// --- Python / Django-or-FastAPI-or-Flask ---
	if exists(root, "requirements.txt") || exists(root, "pyproject.toml") || exists(root, "setup.py") {
		ev := []string{"python manifest"}
		s := score{lang: LanguagePython, score: 10, evidence: ev}
		if exists(root, "manage.py") {
			s.fw = FrameworkDjango
			s.score += 25
			s.evidence = append(s.evidence, "manage.py (django entrypoint)")
		}
		if exists(root, "app/main.py") || hasFile(root, "main.py", "from fastapi") {
			s.fw = FrameworkFastAPI
			s.score += 20
			s.evidence = append(s.evidence, "fastapi import detected")
		}
		if exists(root, "wsgi.py") && hasFileAnywhere(root, "from flask import") {
			s.fw = FrameworkFlask
			s.score += 15
			s.evidence = append(s.evidence, "flask import detected")
		}
		scores = append(scores, s)
	}

	// --- TypeScript/JavaScript / Express-or-Nest-or-Next ---
	if exists(root, "package.json") {
		ev := []string{"package.json"}
		s := score{lang: LanguageJavaScript, score: 8, evidence: ev}
		if exists(root, "tsconfig.json") {
			s.lang = LanguageTypeScript
			s.score += 5
			s.evidence = append(s.evidence, "tsconfig.json (typescript)")
		}
		req := npmDeps(filepath.Join(root, "package.json"))
		switch {
		case req["next"]:
			s.fw = FrameworkNextJS
			s.score += 25
			s.evidence = append(s.evidence, "package.json depends on next")
		case req["@nestjs/core"]:
			s.fw = FrameworkNestJS
			s.score += 25
			s.evidence = append(s.evidence, "package.json depends on @nestjs/core")
		case req["express"]:
			s.fw = FrameworkExpress
			s.score += 20
			s.evidence = append(s.evidence, "package.json depends on express")
		}
		scores = append(scores, s)
	}

	// --- Go ---
	if exists(root, "go.mod") {
		scores = append(scores, score{lang: LanguageGo, score: 15, evidence: []string{"go.mod"}})
	}

	// --- Ruby / Rails ---
	if exists(root, "Gemfile") {
		ev := []string{"Gemfile"}
		s := score{lang: LanguageRuby, score: 10, evidence: ev}
		if exists(root, "config/application.rb") || hasFile(root, "Gemfile", "gem 'rails'") {
			s.fw = FrameworkRails
			s.score += 20
			s.evidence = append(s.evidence, "rails marker present")
		}
		scores = append(scores, s)
	}

	// --- Rust ---
	if exists(root, "Cargo.toml") {
		scores = append(scores, score{lang: LanguageRust, score: 10, evidence: []string{"Cargo.toml"}})
	}

	// --- Java / Spring ---
	if exists(root, "pom.xml") || exists(root, "build.gradle") {
		ev := []string{"java build file"}
		s := score{lang: LanguageJava, score: 10, evidence: ev}
		if hasFileAnywhere(root, "org.springframework") {
			s.fw = FrameworkSpring
			s.score += 20
			s.evidence = append(s.evidence, "spring marker present")
		}
		scores = append(scores, s)
	}

	if len(scores) == 0 {
		det.Confidence = "none"
		return det
	}

	// Pick the highest scoring entry.
	sort.Slice(scores, func(i, j int) bool { return scores[i].score > scores[j].score })
	winner := scores[0]
	det.Language = winner.lang
	det.Framework = winner.fw
	det.Confidence = confidenceFor(winner.score)
	det.Evidence = winner.evidence
	det.CodeRoots = suggestCodeRoots(root, winner.lang, winner.fw)
	det.RequiredPackages = packagesFor(winner.lang, winner.fw)
	return det
}

// confidenceFor maps a raw score to one of four buckets the wizard
// surfaces as a "we're sure" / "ask a human" hint.
func confidenceFor(score int) string {
	switch {
	case score >= 30:
		return "high"
	case score >= 15:
		return "medium"
	case score >= 5:
		return "low"
	default:
		return "none"
	}
}

// suggestCodeRoots returns the directories under root that look like
// they hold application source code. We deliberately don't list every
// possible candidate — the UI shows these as defaults; user can edit.
func suggestCodeRoots(root string, lang Language, fw Framework) []string {
	candidates := []string{}
	switch fw {
	case FrameworkLaravel:
		candidates = []string{"app", "Modules"}
	case FrameworkSymfony:
		candidates = []string{"src"}
	case FrameworkDjango, FrameworkFastAPI, FrameworkFlask:
		candidates = []string{"app", "apps", "src"}
	case FrameworkNextJS:
		candidates = []string{"app", "pages", "src"}
	case FrameworkNestJS, FrameworkExpress:
		candidates = []string{"src"}
	case FrameworkRails:
		candidates = []string{"app", "lib"}
	}
	if len(candidates) == 0 {
		// No framework — common per-language roots.
		switch lang {
		case LanguageGo:
			candidates = []string{"cmd", "pkg", "internal"}
		case LanguagePython:
			candidates = []string{"src"}
		case LanguageTypeScript, LanguageJavaScript:
			candidates = []string{"src"}
		case LanguageRust:
			candidates = []string{"src"}
		case LanguageJava:
			candidates = []string{"src/main/java"}
		}
	}
	out := []string{}
	for _, c := range candidates {
		if exists(root, c) {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		// Fall back to the root itself — better than nothing.
		out = []string{"."}
	}
	return out
}

// packagesFor returns the language-ecosystem packages Lattice needs to
// index the detected stack. Today: the per-language SCIP indexer. As
// we add per-framework adapters, framework-specific packages join the
// list.
func packagesFor(lang Language, _ Framework) []Package {
	switch lang {
	case LanguagePHP:
		return []Package{{
			Name: "sourcegraph/scip-php", Manager: "composer",
			Reason: "SCIP indexer for PHP — used by `lattice extract` to build the call graph",
		}}
	case LanguagePython:
		return []Package{{
			Name: "scip-python", Manager: "npm",
			Reason: "SCIP indexer for Python — used by `lattice extract` to build the call graph",
		}}
	case LanguageTypeScript, LanguageJavaScript:
		return []Package{{
			Name: "@sourcegraph/scip-typescript", Manager: "npm",
			Reason: "SCIP indexer for TypeScript/JavaScript",
		}}
	case LanguageGo:
		return []Package{{
			Name: "github.com/sourcegraph/scip-go/cmd/scip-go@latest", Manager: "go",
			Reason: "SCIP indexer for Go",
		}}
	case LanguageJava:
		return []Package{{
			Name: "scip-java", Manager: "coursier",
			Reason: "SCIP indexer for Java",
		}}
	}
	return nil
}

// InstallCommand returns the shell command (and arguments) that
// installs the package via its manager. Returned as a slice so callers
// can pass it to exec.Command without re-parsing.
func InstallCommand(p Package) []string {
	switch p.Manager {
	case "composer":
		return []string{"composer", "global", "require", p.Name}
	case "npm":
		return []string{"npm", "install", "-g", p.Name}
	case "pip":
		return []string{"pip", "install", p.Name}
	case "go":
		return []string{"go", "install", p.Name}
	case "gem":
		return []string{"gem", "install", p.Name}
	case "cargo":
		return []string{"cargo", "install", p.Name}
	case "coursier":
		return []string{"coursier", "install", p.Name}
	}
	return nil
}

// --- helpers --------------------------------------------------------

func exists(root, rel string) bool {
	_, err := os.Stat(filepath.Join(root, rel))
	return err == nil
}

func hasFile(root, name, marker string) bool {
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), marker)
}

// hasFileAnywhere walks root (shallow, skipping vendor / node_modules)
// and returns true if any text file contains marker. Bounded to avoid
// scanning huge repos; we look only at the top two levels.
func hasFileAnywhere(root, marker string) bool {
	found := false
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		// Bound depth — detection should be cheap.
		rel, _ := filepath.Rel(root, path)
		depth := strings.Count(rel, string(os.PathSeparator))
		if depth > 2 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == "vendor" || name == "node_modules" || name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		// Read only small files — config typically <16KB.
		st, _ := d.Info()
		if st != nil && st.Size() > 32*1024 {
			return nil
		}
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), marker) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// composerRequires reads composer.json and returns the union of the
// require and require-dev maps as a presence-set.
func composerRequires(path string) map[string]bool {
	out := map[string]bool{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var parsed struct {
		Require    map[string]string `json:"require"`
		RequireDev map[string]string `json:"require-dev"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return out
	}
	for k := range parsed.Require {
		out[k] = true
	}
	for k := range parsed.RequireDev {
		out[k] = true
	}
	return out
}

// npmDeps reads a package.json and returns the union of dependencies
// and devDependencies as a presence-set.
func npmDeps(path string) map[string]bool {
	out := map[string]bool{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var parsed struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return out
	}
	for k := range parsed.Dependencies {
		out[k] = true
	}
	for k := range parsed.DevDependencies {
		out[k] = true
	}
	return out
}
