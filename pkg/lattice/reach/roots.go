package reach

// @module-feature reachability

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// Root is a node the runtime starts from, with why.
type Root struct {
	FQN    string `json:"fqn"`
	Reason string `json:"reason"`
}

// EntryFile is a module a build configuration names as an entry point.
type EntryFile struct {
	// File is the module's graph path.
	File string `json:"file"`
	// Reason names the configuration: "package.json bin", "index.html".
	Reason string `json:"reason"`
	// API is true for a library entry (package.json main, module,
	// exports): its exported symbols are called from outside the
	// workspace. A script entry (bin, an HTML module script) roots only
	// its top-level code.
	API bool `json:"api,omitempty"`
}

// RootInput is everything roots come from besides the graph itself.
type RootInput struct {
	EntryPoints []schema.EntryPoint
	EntryFiles  []EntryFile
	Declared    []config.CoverageRoot
}

// Roots collects the graph's roots. Automatic roots are the handlers of
// declared and detected entry points, the entry files named by build
// configuration, symbols and nodes an adapter marks as run by the runtime
// (Go's func main, SQL triggers and policies, cron.schedule bodies, Deno
// serve handlers). Declared roots come from coverage.roots. Nothing in
// between is guessed: a file is not a root because of its name.
// @enforces reachability:INV-3
func (g *Graph) Roots(in RootInput) []Root {
	seen := map[string]bool{}
	var out []Root
	add := func(fqn, reason string) {
		if fqn == "" || seen[fqn] {
			return
		}
		if _, ok := g.nodes[fqn]; !ok {
			return
		}
		seen[fqn] = true
		out = append(out, Root{FQN: fqn, Reason: reason})
	}
	for _, ep := range in.EntryPoints {
		add(ep.Handler.Symbol, "entry point "+ep.ID)
	}
	fqns := make([]string, 0, len(g.nodes))
	for f := range g.nodes {
		fqns = append(fqns, f)
	}
	sort.Strings(fqns)
	for _, f := range fqns {
		n := g.nodes[f]
		if n.root == "" || n.isTest {
			continue
		}
		add(f, n.root)
		if n.root == "main" {
			add(g.tops[n.mod], "main") // the main package is initialised before main runs
		}
	}
	byFile := map[string][]int{}
	for mi, m := range g.modules {
		byFile[m.File] = append(byFile[m.File], mi)
	}
	rootModule := func(mi int, reason string, api bool) {
		add(g.tops[mi], reason)
		if !api {
			return
		}
		for _, s := range g.modules[mi].Symbols {
			if s.Exported && !s.IsTest { // exported functions, types, and their exported methods
				add(s.FQN, reason)
			}
		}
		for _, f := range g.reexported(mi) {
			add(f, reason)
		}
	}
	for _, ef := range in.EntryFiles {
		for _, mi := range byFile[ef.File] {
			rootModule(mi, ef.Reason, ef.API)
		}
	}
	for _, d := range in.Declared {
		reason := "coverage.roots"
		if d.Reason != "" {
			reason += ": " + d.Reason
		}
		if d.Symbol != "" {
			for _, f := range fqns {
				if n := g.nodes[f]; n.symbol && !n.isTest {
					if ok, _ := doublestar.Match(d.Symbol, f); ok {
						add(f, reason)
					}
				}
			}
		}
		if d.File != "" {
			for mi, m := range g.modules {
				if ok, _ := doublestar.Match(d.File, m.File); ok {
					rootModule(mi, reason, true)
				}
			}
		}
	}
	return out
}

// reexported returns the symbols a module re-exports from other modules
// (export { a } from, export * from), so a library entry roots the API it
// forwards.
func (g *Graph) reexported(mi int) []string {
	b := &builder{g: g}
	var out []string
	for _, im := range g.modules[mi].Imports {
		if im.Export == "" || im.Module == "" {
			continue
		}
		if im.Export == "*" {
			for _, xi := range g.byNS[im.Module] {
				for _, s := range g.modules[xi].Symbols {
					if s.Exported && !s.IsTest {
						out = append(out, s.FQN)
					}
				}
			}
			continue
		}
		if f, _ := b.member(im.Module, im.Member, 0); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// CodeRoot is a code root as the graph's file paths see it: Abs on disk,
// and the prefix its files carry in the graph ("" for a single root).
type CodeRoot struct {
	Abs    string
	Prefix string
}

var moduleScript = regexp.MustCompile(`(?is)<script\b[^>]*>`)
var attr = regexp.MustCompile(`(?i)\b(type|src)\s*=\s*["']([^"']*)["']`)

// EntryFiles reads the build configuration beside the parsed modules: a
// package.json's main, module, exports and bin, and an index.html's
// <script type="module" src>. Only directories that hold (or contain)
// parsed modules are looked at, so excluded trees stay excluded. A named
// file that was not parsed (a build output) is mapped back to its source
// (dist/index.js -> src/index.ts, via tsconfig's outDir and rootDir when
// they are set); a file that maps to no parsed module is skipped.
// @enforces reachability:INV-3
func EntryFiles(roots []CodeRoot, modules []ir.Module) []EntryFile {
	parsed := map[string]bool{}
	for _, m := range modules {
		parsed[m.File] = true
	}
	var out []EntryFile
	seen := map[string]bool{}
	for _, cr := range roots {
		dirs := map[string]bool{}
		for _, m := range modules {
			rel := m.File
			if cr.Prefix != "" {
				if !strings.HasPrefix(rel, cr.Prefix+"/") {
					continue
				}
				rel = strings.TrimPrefix(rel, cr.Prefix+"/")
			}
			for d := path.Dir(rel); ; d = path.Dir(d) {
				if dirs[d] {
					break
				}
				dirs[d] = true
				if d == "." || d == "/" {
					break
				}
			}
		}
		list := make([]string, 0, len(dirs))
		for d := range dirs {
			list = append(list, d)
		}
		sort.Strings(list)
		graphPath := func(rel string) string {
			if cr.Prefix != "" {
				return cr.Prefix + "/" + rel
			}
			return rel
		}
		emit := func(dir, target, reason string, api bool) {
			rel := path.Clean(path.Join(dir, target))
			f := sourceOf(cr.Abs, dir, rel, func(p string) bool { return parsed[graphPath(p)] })
			if f == "" {
				return
			}
			key := graphPath(f) + "|" + reason
			if seen[key] {
				return
			}
			seen[key] = true
			out = append(out, EntryFile{File: graphPath(f), Reason: reason, API: api})
		}
		for _, d := range list {
			abs := filepath.Join(cr.Abs, filepath.FromSlash(d))
			if pkg, err := os.ReadFile(filepath.Join(abs, "package.json")); err == nil {
				for _, e := range packageEntries(pkg) {
					emit(d, e.target, "package.json "+e.field, e.field != "bin")
				}
			}
			if html, err := os.ReadFile(filepath.Join(abs, "index.html")); err == nil {
				for _, src := range moduleScripts(string(html)) {
					emit(d, strings.TrimPrefix(src, "/"), "index.html", false)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Reason < out[j].Reason
	})
	return out
}

type pkgEntry struct{ field, target string }

// packageEntries lists the files a package.json names as entries.
func packageEntries(data []byte) []pkgEntry {
	var pkg map[string]json.RawMessage
	if json.Unmarshal(data, &pkg) != nil {
		return nil
	}
	var out []pkgEntry
	var leaves func(field string, raw json.RawMessage)
	leaves = func(field string, raw json.RawMessage) {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			if s != "" && !strings.Contains(s, "*") {
				out = append(out, pkgEntry{field, s})
			}
			return
		}
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) == nil {
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if k == "types" || k == "typings" {
					continue
				}
				leaves(field, m[k])
			}
			return
		}
		var list []json.RawMessage
		if json.Unmarshal(raw, &list) == nil {
			for _, x := range list {
				leaves(field, x)
			}
		}
	}
	for _, field := range []string{"main", "module", "exports", "bin"} {
		if raw, ok := pkg[field]; ok {
			leaves(field, raw)
		}
	}
	return out
}

// moduleScripts returns the src of each <script type="module"> tag.
func moduleScripts(html string) []string {
	var out []string
	for _, tag := range moduleScript.FindAllString(html, -1) {
		var typ, src string
		for _, m := range attr.FindAllStringSubmatch(tag, -1) {
			switch strings.ToLower(m[1]) {
			case "type":
				typ = m[2]
			case "src":
				src = m[2]
			}
		}
		if strings.EqualFold(typ, "module") && src != "" && !strings.Contains(src, "://") {
			out = append(out, src)
		}
	}
	return out
}

var jsExts = []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"}

// sourceOf maps a file a build configuration names to a parsed module:
// the file itself, the same path with a TypeScript or JavaScript
// extension, or, for a build output, its source under tsconfig's rootDir
// (or src/ when the output sits under dist/, build/, lib/ or out/).
func sourceOf(rootAbs, dir, rel string, parsed func(string) bool) string {
	try := func(p string) string {
		if parsed(p) {
			return p
		}
		stem := strings.TrimSuffix(p, path.Ext(p))
		for _, ext := range jsExts {
			if parsed(stem + ext) {
				return stem + ext
			}
		}
		for _, ext := range jsExts {
			if parsed(path.Join(p, "index"+ext)) {
				return path.Join(p, "index"+ext)
			}
		}
		return ""
	}
	if f := try(rel); f != "" {
		return f
	}
	inDir := strings.TrimPrefix(rel, strings.TrimSuffix(dir, "/")+"/")
	if dir == "." {
		inDir = rel
	}
	outDir, rootDir := tsconfigDirs(filepath.Join(rootAbs, filepath.FromSlash(dir), "tsconfig.json"))
	if outDir != "" && strings.HasPrefix(inDir, outDir+"/") {
		if f := try(path.Join(dir, rootDir, strings.TrimPrefix(inDir, outDir+"/"))); f != "" {
			return f
		}
	}
	for _, out := range []string{"dist", "build", "lib", "out"} {
		if strings.HasPrefix(inDir, out+"/") {
			if f := try(path.Join(dir, "src", strings.TrimPrefix(inDir, out+"/"))); f != "" {
				return f
			}
		}
	}
	return ""
}

var jsonComment = regexp.MustCompile(`(?m)^\s*//.*$`)

// tsconfigDirs returns compilerOptions.outDir and rootDir (cleaned, "."
// for none) of a tsconfig.json, or "" when it cannot be read.
func tsconfigDirs(file string) (outDir, rootDir string) {
	data, err := os.ReadFile(file)
	if err != nil {
		return "", ""
	}
	var cfg struct {
		CompilerOptions struct {
			OutDir  string `json:"outDir"`
			RootDir string `json:"rootDir"`
		} `json:"compilerOptions"`
	}
	if json.Unmarshal(jsonComment.ReplaceAll(data, nil), &cfg) != nil || cfg.CompilerOptions.OutDir == "" {
		return "", ""
	}
	root := path.Clean(strings.TrimPrefix(cfg.CompilerOptions.RootDir, "./"))
	if cfg.CompilerOptions.RootDir == "" {
		root = "."
	}
	return path.Clean(strings.TrimPrefix(cfg.CompilerOptions.OutDir, "./")), root
}
