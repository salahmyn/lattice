package preflight

// @module-feature cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/config"
)

// Runtime is what `lattice runs-clean` needs: the executables of the
// runtime commands, their Node minimum, and runtime.services. dir is the
// directory the commands run in. An unconfigured runtime needs nothing.
func Runtime(rt config.Runtime, dir string) []Requirement {
	d := deriver{}
	first := true // the steps run in order: a later one may use what an earlier one made
	for _, c := range []struct{ key, cmd string }{
		{"runtime.clean_install", rt.CleanInstall},
		{"runtime.build", rt.Build},
		{"runtime.boot", rt.Boot},
	} {
		if c.cmd != "" {
			d.shell(c.key, c.cmd, dir, first)
			first = false
		}
	}
	d.services("runtime.services", rt.Services)
	return d.reqs
}

// Break is what `lattice break` needs: the executables of break.setup, of
// each runner and of each break.commands template, the runners' dirs, their
// Node minimum, and break.services. base is the directory that holds
// lattice/ in the main checkout; break runs the same commands from the same
// place in a worktree of it.
func Break(b config.Break, base string) []Requirement {
	d := deriver{}
	for i, s := range b.Setup {
		d.shell(fmt.Sprintf("break.setup[%d]", i), s, base, i == 0)
	}
	// A setup command may make what a runner names by path; with none, the
	// worktree has what the checkout has.
	paths := len(b.Setup) == 0
	d.runners(b, base, paths)
	for _, lang := range sortedKeys(b.Commands) {
		if c := strings.TrimSpace(b.Commands[lang]); c != "" {
			d.shell("break.commands."+lang, c, base, paths)
		}
	}
	d.services("break.services", b.Services)
	return d.reqs
}

// Runners is what `lattice check` needs when it lists the touched verifiers
// by break.runners: each runner's executable, its dir and its Node minimum.
// check runs neither break.setup nor a test, so the rest of break: is not
// its requirement. An executable a runner names by relative path is checked
// only when no break.setup command could make it.
func Runners(b config.Break, base string) []Requirement {
	d := deriver{}
	d.runners(b, base, len(b.Setup) == 0)
	return d.reqs
}

func (d *deriver) runners(b config.Break, base string, paths bool) {
	for i, r := range b.Runners {
		if len(r.Command) == 0 {
			continue
		}
		dir := base
		if r.Dir != "" {
			key := fmt.Sprintf("break.runners[%d].dir", i)
			d.add(Requirement{Kind: KindPath, Key: key, Target: r.Dir, Dir: base})
			dir = filepath.Join(base, r.Dir)
		}
		d.argv(fmt.Sprintf("break.runners[%d].command", i), r.Command[0], dir, paths)
	}
}

// deriver collects requirements in config order, without duplicates.
type deriver struct {
	reqs []Requirement
	seen map[string]bool
	// corepack is set once a command runs corepack, which puts pnpm and yarn
	// on PATH.
	corepack bool
}

func (d *deriver) add(r Requirement) {
	if d.seen == nil {
		d.seen = map[string]bool{}
	}
	k := string(r.Kind) + "\x00" + r.Key + "\x00" + r.Target + "\x00" + r.Dir
	if d.seen[k] {
		return
	}
	d.seen[k] = true
	d.reqs = append(d.reqs, r)
}

// shell adds the executables a shell command line runs. paths says whether
// nothing has run before the line that could make a file it names by
// relative path; within the line, only its first command qualifies.
func (d *deriver) shell(key, line, dir string, paths bool) {
	for i, w := range commandWords(line) {
		d.argv(key, w, dir, paths && i == 0)
	}
}

// argv adds one executable that a command runs. A relative path-named
// executable is checked only when paths is set.
func (d *deriver) argv(key, exe, dir string, paths bool) {
	if !checkable(exe) {
		return
	}
	if strings.ContainsRune(exe, '/') && !filepath.IsAbs(exe) && !paths {
		return
	}
	if (exe == "pnpm" || exe == "yarn") && d.corepack {
		return
	}
	if exe == "corepack" {
		d.corepack = true
	}
	d.add(Requirement{Kind: KindExecutable, Key: key, Target: exe, Dir: dir})
	if dockerTools[filepath.Base(exe)] {
		d.add(Requirement{Kind: KindDocker, Key: key, Target: exe, Dir: dir})
	}
	if nodeTools[filepath.Base(exe)] {
		if min, src, ok := enginesNode(dir); ok {
			d.add(Requirement{Kind: KindVersion, Key: key, Target: "node", Min: min, Source: src, Dir: dir})
		}
	}
}

func (d *deriver) services(key string, ss []config.Service) {
	for i, s := range ss {
		d.add(Requirement{Kind: KindService, Key: fmt.Sprintf("%s[%d].address", key, i),
			Target: strings.TrimSpace(s.Address), Name: s.Name})
	}
}

// dockerTools are executables that need a running Docker daemon.
var dockerTools = map[string]bool{"docker": true, "docker-compose": true}

// nodeTools are executables that run on the Node found on PATH.
var nodeTools = map[string]bool{"node": true, "npx": true, "npm": true, "pnpm": true, "yarn": true}

// checkable reports whether an executable name can be checked as written: a
// name carrying a placeholder, a variable or a glob is resolved only when
// the command runs.
func checkable(exe string) bool {
	return exe != "" && !strings.ContainsAny(exe, "${}`*?[]<>=~()")
}

// enginesNode reads the lower bound of engines.node from dir/package.json.
// Only a bound it can read for certain counts: >=X, ^X, ~X or a bare X
// (with optional .Y.Z or .x); a range with || or any other form is not
// checked.
func enginesNode(dir string) (min, source string, ok bool) {
	p := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(p)
	if err != nil {
		return "", "", false
	}
	var pkg struct {
		Engines map[string]string `json:"engines"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return "", "", false
	}
	min, ok = lowerBound(pkg.Engines["node"])
	return min, "package.json engines.node in " + dir, ok
}

func lowerBound(rng string) (string, bool) {
	rng = strings.TrimSpace(rng)
	if rng == "" || strings.Contains(rng, "||") {
		return "", false
	}
	first := strings.Fields(rng)[0]
	switch {
	case strings.HasPrefix(first, ">="):
		first = strings.TrimSpace(first[2:])
		if first == "" {
			if f := strings.Fields(rng); len(f) > 1 {
				first = f[1]
			}
		}
	case strings.HasPrefix(first, "^"), strings.HasPrefix(first, "~"):
		first = first[1:]
	case strings.HasPrefix(first, ">"), strings.HasPrefix(first, "<"), strings.HasPrefix(first, "="):
		return "", false
	}
	first = strings.TrimPrefix(first, "v")
	var parts []string
	for _, p := range strings.Split(first, ".") {
		if p == "x" || p == "X" || p == "*" {
			break
		}
		if _, err := strconv.Atoi(p); err != nil {
			return "", false
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 || len(parts) > 3 {
		return "", false
	}
	return strings.Join(parts, "."), true
}

// compareVersions compares dotted numeric versions ("v18.2.0", "18"): -1, 0
// or 1. Missing parts are 0; a pre-release suffix is ignored.
func compareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func versionParts(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	for i, p := range strings.SplitN(v, ".", 3) {
		n, _ := strconv.Atoi(p)
		out[i] = n
	}
	return out
}

func majorOf(v string) string {
	if i := strings.IndexByte(v, '.'); i >= 0 {
		return v[:i]
	}
	return v
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
