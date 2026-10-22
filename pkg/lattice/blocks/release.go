package blocks

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// ParseRelease reads a release/v1 file: the version of a repo that is deployed.
// Unknown fields are allowed; a release needs a version or a commit.
func ParseRelease(data []byte) (schema.Release, error) {
	var r struct {
		Repo        string `json:"repo"`
		Version     string `json:"version"`
		Commit      string `json:"commit"`
		CommittedAt string `json:"committedAt"`
		DeployedAt  string `json:"deployedAt"`
		Run         string `json:"run"`
		Source      string `json:"source"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return schema.Release{}, fmt.Errorf("release/v1: %w", err)
	}
	if r.Version == "" && r.Commit == "" {
		return schema.Release{}, fmt.Errorf("release/v1: needs version or commit")
	}
	if r.Version == "" {
		r.Version = r.Commit
	}
	return schema.Release{Repo: r.Repo, Version: r.Version, Commit: r.Commit, CommittedAt: r.CommittedAt,
		DeployedAt: r.DeployedAt, Run: r.Run, Source: r.Source}, nil
}

// ParseSnapshot reads the stamp of a source read from a directory: the repo,
// branch and commit the snapshot was taken from. Unknown fields are allowed.
func ParseSnapshot(data []byte) (schema.SourceStamp, error) {
	var s struct {
		Repo       string `json:"repo"`
		Branch     string `json:"branch"`
		Commit     string `json:"commit"`
		CommitDate string `json:"commitDate"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return schema.SourceStamp{}, fmt.Errorf("snapshot stamp: %w", err)
	}
	if s.Commit == "" {
		return schema.SourceStamp{}, fmt.Errorf("snapshot stamp: needs commit")
	}
	return schema.SourceStamp{Repo: s.Repo, Branch: s.Branch, Commit: s.Commit, CommitDate: s.CommitDate,
		State: schema.FreshSnapshot}, nil
}

// Freshness says how head stands against rel in clone, with read-only git. A
// release whose commit is not in the clone is unknown, not an error. When the
// release resolves, rel.Commit is set to the full commit.
func Freshness(clone, head string, rel *schema.Release) (state string, behind, ahead int) {
	if rel == nil {
		return schema.FreshNoRelease, 0, 0
	}
	ref := rel.Commit
	if ref == "" {
		ref = rel.Version
	}
	if head == "" || strings.HasPrefix(ref, "-") {
		return schema.FreshUnknown, 0, 0
	}
	out, err := runGit(clone, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return schema.FreshUnknown, 0, 0
	}
	rel.Commit = strings.TrimSpace(string(out))
	count := func(rng string) (int, error) {
		out, err := runGit(clone, "rev-list", "--count", rng)
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(strings.TrimSpace(string(out)))
	}
	behind, err1 := count(head + ".." + rel.Commit)
	ahead, err2 := count(rel.Commit + ".." + head)
	switch {
	case err1 != nil || err2 != nil:
		return schema.FreshUnknown, 0, 0
	case behind > 0:
		return schema.FreshBehind, behind, ahead
	case ahead > 0:
		return schema.FreshAhead, 0, ahead
	}
	return schema.FreshCurrent, 0, 0
}
