// Package skills embeds the shipped Lattice agent skills and provides
// enumeration and export. Skills are markdown folders that external AI agents
// load to learn how to use Lattice; the framework ships them but never
// "runs" them.
package skills

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed all:lattice
var embedded embed.FS

// Info describes one shipped skill.
type Info struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// List enumerates every shipped skill, sorted by id.
func List() []Info {
	var out []Info
	entries, err := embedded.ReadDir("lattice")
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := "lattice/" + e.Name()
		info := Info{ID: id, Name: e.Name()}
		if data, err := embedded.ReadFile("lattice/" + e.Name() + "/SKILL.md"); err == nil {
			info.Name, info.Description = parseFrontmatter(string(data), e.Name())
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Export copies a skill folder (id form "lattice/<name>") to destDir,
// preserving the "lattice/<name>" namespace under destDir.
func Export(id, destDir string) error {
	src := "lattice/" + strings.TrimPrefix(id, "lattice/")
	return fs.WalkDir(embedded, src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(destDir, filepath.FromSlash(id), rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := embedded.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// ExportAll copies every shipped skill into destDir (used by `lattice init`).
func ExportAll(destDir string) error {
	for _, s := range List() {
		if err := Export(s.ID, destDir); err != nil {
			return err
		}
	}
	return nil
}

// parseFrontmatter pulls name and description from a SKILL.md YAML header.
func parseFrontmatter(content, fallbackName string) (name, description string) {
	name = fallbackName
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return name, description
	}
	for _, ln := range lines[1:] {
		if strings.TrimSpace(ln) == "---" {
			break
		}
		if v, ok := strings.CutPrefix(ln, "name:"); ok {
			name = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(ln, "description:"); ok {
			description = strings.TrimSpace(v)
		}
	}
	return name, description
}
