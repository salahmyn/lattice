// Package preflight checks, in seconds and without running any test, what a
// long command needs before it starts: the executables its configured
// commands name, the directories they run in, a minimum Node version a
// package.json declares, a Docker daemon for a command that runs docker,
// and the TCP services the config lists. Every
// requirement comes from configuration; nothing is guessed from the stack.
//
// Requirements are derived per gate (Runtime for runs-clean and demonstrate,
// Break for break, Runners for check) and checked together, so one refusal
// lists every problem.
package preflight

// @module-feature cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Problem codes.
const (
	CodeExecNotFound  = "EXEC_NOT_FOUND"
	CodePathNotFound  = "PATH_NOT_FOUND"
	CodeVersionTooOld = "VERSION_TOO_OLD"
	CodeServiceDown   = "SERVICE_UNREACHABLE"
	CodeBadAddress    = "SERVICE_BAD_ADDRESS"
	CodeDirtyTree     = "DIRTY_TREE"
	CodeDockerDown    = "DOCKER_UNAVAILABLE"
)

// Kind is what a requirement checks.
type Kind string

// Requirement kinds.
const (
	KindExecutable Kind = "executable"
	KindPath       Kind = "path"
	KindVersion    Kind = "version"
	KindService    Kind = "service"
	KindDocker     Kind = "docker"
)

// DialTimeout bounds each TCP reachability check.
const DialTimeout = 2 * time.Second

// versionTimeout bounds `node --version` and the Docker daemon check.
const versionTimeout = 5 * time.Second

// Requirement is one thing a gate needs, and the config key that declares it.
type Requirement struct {
	Kind Kind
	// Key is the config key the requirement comes from, e.g.
	// break.runners[0].command.
	Key string
	// Target is the executable, the path, the tool whose version is checked
	// (node), the docker executable whose daemon is checked, or the
	// host:port of a service.
	Target string
	// Dir resolves a relative Target (an executable named by path, or a
	// directory).
	Dir string
	// Min is the minimum version, for KindVersion.
	Min string
	// Source says where the minimum is declared, for KindVersion.
	Source string
	// Name labels a service.
	Name string
}

// Result is the outcome of one requirement.
type Result struct {
	Kind   Kind   `json:"kind"`
	Key    string `json:"key"`
	Target string `json:"target"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Code   string `json:"code,omitempty"`
	Next   string `json:"next,omitempty"`
}

// Problem is one failed requirement as a refusal lists it.
type Problem struct {
	Code   string `json:"code"`
	Key    string `json:"key"`
	Detail string `json:"detail"`
	Next   string `json:"next"`
}

// Check runs every requirement and returns one result each, in order.
// Service dials run concurrently, so a refusal costs one dial timeout at
// most, not one per service.
func Check(ctx context.Context, reqs []Requirement) []Result {
	if ctx == nil {
		ctx = context.Background()
	}
	out := make([]Result, len(reqs))
	node := &nodeVersion{}
	daemon := map[string]*dockerDaemon{}
	var wg sync.WaitGroup
	for i, r := range reqs {
		if r.Kind == KindService {
			wg.Add(1)
			go func(i int, r Requirement) {
				defer wg.Done()
				out[i] = checkService(ctx, r)
			}(i, r)
			continue
		}
		switch r.Kind {
		case KindExecutable:
			out[i] = checkExecutable(r)
		case KindPath:
			out[i] = checkPath(r)
		case KindVersion:
			out[i] = checkVersion(ctx, r, node)
		case KindDocker:
			dd := daemon[r.Target]
			if dd == nil {
				dd = &dockerDaemon{}
				daemon[r.Target] = dd
			}
			out[i] = checkDocker(ctx, r, dd)
		default:
			out[i] = Result{Kind: r.Kind, Key: r.Key, Target: r.Target, OK: true, Detail: "not checked"}
		}
	}
	wg.Wait()
	return out
}

// Problems returns the failed results as problems, in order.
func Problems(results []Result) []Problem {
	var ps []Problem
	for _, r := range results {
		if !r.OK {
			ps = append(ps, Problem{Code: r.Code, Key: r.Key, Detail: r.Detail, Next: r.Next})
		}
	}
	return ps
}

// LookupExecutable resolves tool the way a command would: by PATH lookup, or,
// when the name contains a path separator, as a file relative to dir.
func LookupExecutable(tool, dir string) (string, error) {
	if !strings.ContainsRune(tool, '/') && filepath.Base(tool) == tool {
		return exec.LookPath(tool)
	}
	p := tool
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, tool)
	}
	st, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("not found at %s", p)
	}
	if st.IsDir() {
		return "", fmt.Errorf("%s is a directory", p)
	}
	if st.Mode()&0o111 == 0 {
		return "", fmt.Errorf("%s is not executable", p)
	}
	return p, nil
}

func checkExecutable(r Requirement) Result {
	res := Result{Kind: r.Kind, Key: r.Key, Target: r.Target}
	path, err := LookupExecutable(r.Target, r.Dir)
	if err == nil {
		res.OK, res.Detail = true, r.Target+" found: "+path
		return res
	}
	res.Code = CodeExecNotFound
	if strings.ContainsRune(r.Target, '/') {
		res.Detail = r.Target + ": " + err.Error()
		res.Next = "create " + r.Target + " (or fix the path in " + r.Key + ")"
		return res
	}
	res.Detail = r.Target + " is not on PATH"
	res.Next = installHint(r.Target, r.Key)
	return res
}

func checkPath(r Requirement) Result {
	res := Result{Kind: r.Kind, Key: r.Key, Target: r.Target}
	p := r.Target
	if !filepath.IsAbs(p) {
		p = filepath.Join(r.Dir, p)
	}
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		res.OK, res.Detail = true, "directory exists: "+p
		return res
	}
	res.Code = CodePathNotFound
	res.Detail = "directory " + r.Target + " does not exist (" + p + ")"
	res.Next = "create " + r.Target + " or fix " + r.Key
	return res
}

func checkService(ctx context.Context, r Requirement) Result {
	res := Result{Kind: r.Kind, Key: r.Key, Target: r.Target}
	label := r.Target
	if r.Name != "" {
		label = r.Name + " (" + r.Target + ")"
	}
	if _, _, err := net.SplitHostPort(r.Target); err != nil {
		res.Code = CodeBadAddress
		res.Detail = fmt.Sprintf("%s is not host:port: %v", label, err)
		res.Next = "write " + r.Key + " as host:port, e.g. localhost:5432"
		return res
	}
	d := net.Dialer{Timeout: DialTimeout}
	conn, err := d.DialContext(ctx, "tcp", r.Target)
	if err != nil {
		res.Code = CodeServiceDown
		res.Detail = fmt.Sprintf("%s does not answer on TCP: %v", label, err)
		res.Next = "start " + label + " before running this command, or fix " + r.Key
		return res
	}
	_ = conn.Close()
	res.OK, res.Detail = true, label+" answers"
	return res
}

// nodeVersion runs `node --version` once per Check.
type nodeVersion struct {
	once    sync.Once
	version string
	err     error
}

func (n *nodeVersion) get(ctx context.Context) (string, error) {
	n.once.Do(func() {
		path, err := exec.LookPath("node")
		if err != nil {
			n.err = err
			return
		}
		cctx, cancel := context.WithTimeout(ctx, versionTimeout)
		defer cancel()
		out, err := exec.CommandContext(cctx, path, "--version").Output()
		if err != nil {
			n.err = err
			return
		}
		n.version = strings.TrimSpace(string(out))
	})
	return n.version, n.err
}

func checkVersion(ctx context.Context, r Requirement, node *nodeVersion) Result {
	res := Result{Kind: r.Kind, Key: r.Key, Target: r.Target}
	have, err := node.get(ctx)
	if err != nil {
		// A missing node is the executable check's to report; an unreadable
		// version is not a reason to refuse.
		res.OK, res.Detail = true, "node version not read: "+err.Error()
		return res
	}
	if compareVersions(have, r.Min) >= 0 {
		res.OK, res.Detail = true, fmt.Sprintf("node %s meets >=%s (%s)", have, r.Min, r.Source)
		return res
	}
	res.Code = CodeVersionTooOld
	res.Detail = fmt.Sprintf("node %s is older than >=%s, which %s declares", have, r.Min, r.Source)
	res.Next = "install Node " + r.Min + " or newer (for example `nvm install " + majorOf(r.Min) + "`) and put it first on PATH"
	return res
}

// dockerDaemon asks the daemon for its version once per executable and
// Check.
type dockerDaemon struct {
	once    sync.Once
	path    string
	version string
	err     error
}

func (d *dockerDaemon) get(ctx context.Context, exe, dir string) (string, string, error) {
	d.once.Do(func() {
		path, err := LookupExecutable(exe, dir)
		if err != nil {
			d.err = err
			return
		}
		d.path = path
		args := []string{"version", "--format", "{{.Server.Version}}"}
		if filepath.Base(exe) == "docker-compose" {
			// docker-compose has no daemon query of its own; ls asks the
			// daemon for its projects.
			args = []string{"ls", "--quiet"}
		}
		cctx, cancel := context.WithTimeout(ctx, versionTimeout)
		defer cancel()
		cmd := exec.CommandContext(cctx, path, args...)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			msg := firstLine(stderr.String())
			if cctx.Err() != nil {
				msg = "no answer within " + versionTimeout.String()
			}
			if msg == "" {
				msg = err.Error()
			}
			d.err = errors.New(msg)
			return
		}
		d.version = firstLine(string(out))
	})
	return d.path, d.version, d.err
}

func checkDocker(ctx context.Context, r Requirement, dd *dockerDaemon) Result {
	res := Result{Kind: r.Kind, Key: r.Key, Target: r.Target}
	path, version, err := dd.get(ctx, r.Target, r.Dir)
	switch {
	case path == "" && err != nil:
		// A missing docker is the executable check's to report.
		res.OK, res.Detail = true, "docker daemon not checked: "+err.Error()
	case err != nil:
		res.Code = CodeDockerDown
		res.Detail = "the Docker daemon does not answer " + r.Target + " (" + err.Error() + ")"
		res.Next = "start Docker (the daemon, or Docker Desktop) until `docker info` works, or change " + r.Key
	default:
		res.OK, res.Detail = true, "the Docker daemon answers"
		if version != "" {
			res.Detail += " (server " + version + ")"
		}
	}
	return res
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

// hints name an install step for common runner executables.
var hints = map[string]string{
	"node":     "install Node.js",
	"npx":      "install Node.js (npx ships with npm)",
	"npm":      "install Node.js (npm ships with it)",
	"pnpm":     "install pnpm (`corepack enable` or `npm install -g pnpm`)",
	"yarn":     "install yarn (`corepack enable` or `npm install -g yarn`)",
	"deno":     "install Deno (https://deno.land)",
	"bun":      "install Bun (https://bun.sh)",
	"python":   "install Python, or name python3 in the command",
	"python3":  "install Python 3",
	"pytest":   "install pytest (`pip install pytest`), or run it as `python3 -m pytest`",
	"docker":   "install Docker and make sure the docker CLI is on PATH",
	"go":       "install Go",
	"php":      "install PHP",
	"composer": "install Composer",
}

func installHint(tool, key string) string {
	if h, ok := hints[tool]; ok {
		return h + ", or change " + key
	}
	return "install " + tool + " or put it on PATH, or change " + key
}
