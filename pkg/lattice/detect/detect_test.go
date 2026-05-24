package detect

import (
	"os"
	"path/filepath"
	"testing"
)

// scaffold lays down the named files (with empty contents unless a
// content is supplied) under a fresh temp dir. Used to fake a project
// shape for detection tests.
func scaffold(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDetectLaravel(t *testing.T) {
	root := scaffold(t, map[string]string{
		"composer.json":     `{"require": {"laravel/framework": "^10.0", "php": "^8.2"}}`,
		"artisan":           "",
		"bootstrap/app.php": "",
		"app/Http/Kernel.php": "",
	})
	d := Detect(root)
	if d.Language != LanguagePHP {
		t.Errorf("language = %q, want php", d.Language)
	}
	if d.Framework != FrameworkLaravel {
		t.Errorf("framework = %q, want laravel", d.Framework)
	}
	if d.Confidence != "high" {
		t.Errorf("confidence = %q, want high", d.Confidence)
	}
	if len(d.CodeRoots) == 0 || d.CodeRoots[0] != "app" {
		t.Errorf("code_roots = %v, want [app ...]", d.CodeRoots)
	}
	if len(d.RequiredPackages) == 0 || d.RequiredPackages[0].Name != "sourcegraph/scip-php" {
		t.Errorf("required_packages = %v, want scip-php", d.RequiredPackages)
	}
}

func TestDetectDjango(t *testing.T) {
	root := scaffold(t, map[string]string{
		"requirements.txt": "django==4.2\n",
		"manage.py":        "#!/usr/bin/env python",
		"app/__init__.py":  "",
	})
	d := Detect(root)
	if d.Language != LanguagePython || d.Framework != FrameworkDjango {
		t.Errorf("got %s/%s, want python/django", d.Language, d.Framework)
	}
}

func TestDetectNestJS(t *testing.T) {
	root := scaffold(t, map[string]string{
		"package.json":  `{"dependencies": {"@nestjs/core": "^10.0", "rxjs": "*"}}`,
		"tsconfig.json": "{}",
		"src/main.ts":   "",
	})
	d := Detect(root)
	if d.Language != LanguageTypeScript {
		t.Errorf("language = %q, want typescript", d.Language)
	}
	if d.Framework != FrameworkNestJS {
		t.Errorf("framework = %q, want nestjs", d.Framework)
	}
	if d.Confidence != "high" {
		t.Errorf("confidence = %q, want high", d.Confidence)
	}
}

func TestDetectNoLanguage(t *testing.T) {
	root := scaffold(t, map[string]string{"README.md": "# nothing"})
	d := Detect(root)
	if d.Confidence != "none" {
		t.Errorf("confidence = %q, want none for empty project", d.Confidence)
	}
}

func TestDetectGo(t *testing.T) {
	root := scaffold(t, map[string]string{
		"go.mod":       "module example.com/x\n\ngo 1.22\n",
		"main.go":      "package main",
		"cmd/x/main.go": "package main",
	})
	d := Detect(root)
	if d.Language != LanguageGo {
		t.Errorf("language = %q, want go", d.Language)
	}
}

func TestInstallCommandShape(t *testing.T) {
	p := Package{Name: "sourcegraph/scip-php", Manager: "composer"}
	got := InstallCommand(p)
	want := []string{"composer", "global", "require", "sourcegraph/scip-php"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDetectPicksHighestScoreOnMultiManifest(t *testing.T) {
	// A repo with both go.mod and package.json — go wins (15) over
	// JS-without-typescript (8). Keeps tied scores from masking the
	// dominant ecosystem.
	root := scaffold(t, map[string]string{
		"go.mod":       "module x\n",
		"package.json": `{}`,
		"main.go":      "package main",
	})
	d := Detect(root)
	if d.Language != LanguageGo {
		t.Errorf("got %s, expected go to win on score", d.Language)
	}
}
