package confirm

// @module-feature confirm

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// BRDAt reads a BRD as of a commit. latticeRel is the lattice directory
// relative to the repository root.
func BRDAt(g Git, latticeRel, sha, id string) (*schema.BRD, error) {
	data, err := g.Show(sha, path.Join(latticeRel, "brds", id+".yaml"))
	if err != nil {
		return nil, err
	}
	var b schema.BRD
	if err := yaml.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// Git runs git commands in one repository. The commands confirm and prepare
// issue never read or write the user's index or working tree: they read
// objects by revision, and prepare writes objects and a new ref only.
type Git struct {
	Dir string
}

// Run runs one git command and returns its stdout.
func (g Git) Run(args ...string) ([]byte, error) { return g.RunEnv(nil, nil, args...) }

// RunEnv runs one git command with extra environment and standard input.
func (g Git) RunEnv(env []string, stdin []byte, args ...string) ([]byte, error) {
	full := append([]string{"-C", g.Dir, "-c", "core.quotePath=false", "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(append(os.Environ(), "GIT_OPTIONAL_LOCKS=0"), env...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return out, nil
}

// Line runs a command and returns its first output line.
func (g Git) Line(args ...string) (string, error) {
	out, err := g.Run(args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]), nil
}

// Show returns a file as of a revision.
func (g Git) Show(rev, p string) ([]byte, error) { return g.Run("show", rev+":"+p) }

// Top is the repository's top-level directory.
func (g Git) Top() (string, error) { return g.Line("rev-parse", "--show-toplevel") }

// Head is the commit HEAD points at.
func (g Git) Head() (string, error) { return g.Line("rev-parse", "HEAD") }

// DefaultBranch finds the branch decisions are merged to: origin's HEAD, else
// main, else master, else the current branch, else HEAD.
func DefaultBranch(g Git) string {
	if ref, err := g.Line("symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); err == nil && ref != "" {
		return strings.TrimPrefix(ref, "refs/remotes/")
	}
	for _, b := range []string{"main", "master"} {
		if _, err := g.Line("rev-parse", "--verify", "--quiet", "refs/heads/"+b); err == nil {
			return b
		}
	}
	if b, err := g.Line("symbolic-ref", "--quiet", "--short", "HEAD"); err == nil && b != "" {
		return b
	}
	return "HEAD"
}

// BaseName is a branch name without its remote prefix, for `gh pr create --base`.
func BaseName(branch string) string { return strings.TrimPrefix(branch, "origin/") }

// Found is a decision file seen in the repository.
type Found struct {
	Decision Decision
	// Path is the file's path from the repository root.
	Path string
	// AddedSHA is the commit that added the file on the default branch.
	AddedSHA string
	// OnDefault is false for a decision that exists only on a decision branch.
	OnDefault bool
	// Ref is the branch it was read from.
	Ref string
}

// Scan finds every decision file on the default branch, and those that exist
// only on decision/* branches (prepared, not merged). latticeRel is the
// lattice directory relative to the repository root. Unreadable files are
// reported in warnings and skipped.
func Scan(g Git, branch, latticeRel string) (found []Found, warnings []string, err error) {
	dir := path.Join(latticeRel, ApprovalsDir)
	seen := map[string]bool{}
	key := func(d Decision) string { return d.Stem() }

	out, lerr := g.Run("ls-tree", "-r", "--name-only", branch, "--", dir)
	if lerr == nil {
		for _, p := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if !strings.HasSuffix(p, ".yaml") {
				continue
			}
			data, err := g.Show(branch, p)
			if err != nil {
				warnings = append(warnings, err.Error())
				continue
			}
			d, err := ParseDecision(data)
			if err != nil {
				warnings = append(warnings, p+": "+err.Error())
				continue
			}
			sha, _ := g.Line("log", "--diff-filter=A", "--format=%H", "-n", "1", branch, "--", p)
			found = append(found, Found{Decision: d, Path: p, AddedSHA: sha, OnDefault: true, Ref: branch})
			seen[key(d)] = true
		}
	}

	refs, rerr := g.Run("for-each-ref", "--format=%(refname)", "refs/heads/decision", "refs/remotes/origin/decision")
	if rerr != nil {
		return found, warnings, nil
	}
	for _, ref := range strings.Fields(string(refs)) {
		names, err := g.Run("diff", "--name-only", "--diff-filter=A", branch, ref, "--", dir)
		if err != nil {
			continue
		}
		for _, p := range strings.Fields(string(names)) {
			data, err := g.Show(ref, p)
			if err != nil {
				continue
			}
			d, err := ParseDecision(data)
			if err != nil || seen[key(d)] {
				continue
			}
			seen[key(d)] = true
			found = append(found, Found{Decision: d, Path: p, OnDefault: false, Ref: ref})
		}
	}
	return found, warnings, nil
}
