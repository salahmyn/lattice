package prepare

// @module-feature confirm

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/confirm"
)

// identity reads the git identity the preparer would commit as. It is
// display only: a typed string is not an approver.
func identity(g confirm.Git) (name, email string) {
	out, err := g.Line("var", "GIT_AUTHOR_IDENT")
	if err == nil {
		if i, j := strings.Index(out, "<"), strings.Index(out, ">"); i > 0 && j > i {
			name, email = strings.TrimSpace(out[:i]), out[i+1:j]
		}
	}
	if name == "" {
		name = "lattice prepare"
	}
	if email == "" {
		email = "prepare@lattice.invalid"
	}
	return name, email
}

// commitBranch writes files onto a new branch that starts at HEAD, with git
// plumbing only: hash-object, a temporary index, write-tree, commit-tree and
// update-ref. The user's index, working tree and HEAD are never read for
// writing, so the checkout is untouched and nothing is switched. The branch
// must not exist yet.
// @enforces confirm:INV-5
func commitBranch(g confirm.Git, branch, message, name, email string, files map[string][]byte) (string, error) {
	tmp, err := os.MkdirTemp("", "lattice-prepare-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	env := []string{
		"GIT_INDEX_FILE=" + filepath.Join(tmp, "index"),
		"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + email,
		"GIT_COMMITTER_NAME=" + name, "GIT_COMMITTER_EMAIL=" + email,
	}
	if _, err := g.RunEnv(env, nil, "read-tree", "HEAD"); err != nil {
		return "", err
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		blob, err := g.RunEnv(env, files[p], "hash-object", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		if _, err := g.RunEnv(env, nil, "update-index", "--add", "--cacheinfo",
			fmt.Sprintf("100644,%s,%s", strings.TrimSpace(string(blob)), p)); err != nil {
			return "", err
		}
	}
	tree, err := g.RunEnv(env, nil, "write-tree")
	if err != nil {
		return "", err
	}
	commit, err := g.RunEnv(env, nil, "commit-tree", strings.TrimSpace(string(tree)), "-p", "HEAD", "-m", message)
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(commit))
	// An empty old value means the ref must not exist yet.
	if _, err := g.Run("update-ref", "refs/heads/"+branch, sha, ""); err != nil {
		return "", err
	}
	return sha, nil
}

func branchExists(g confirm.Git, branch string) bool {
	_, err := g.Line("rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// fileAt returns a file's content at a revision, and whether it exists there.
func fileAt(g confirm.Git, rev, file string) ([]byte, bool) {
	if _, err := g.Line("cat-file", "-e", rev+":"+file); err != nil {
		return nil, false
	}
	data, err := g.Show(rev, file)
	return data, err == nil
}

func validBranch(g confirm.Git, branch string) bool {
	_, err := g.Run("check-ref-format", "--branch", branch)
	return err == nil
}

// shellQuote single-quotes s for a POSIX shell.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
