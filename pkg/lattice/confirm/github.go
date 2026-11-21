package confirm

// @module-feature confirm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature"
)

// GitHub is the GitHub REST client: the only Platform that uses the network.
type GitHub struct {
	Token   string
	Owner   string
	Repo    string
	BaseURL string // default https://api.github.com
	Client  *http.Client
}

// ParseGitHubRemote reads owner and repo from a remote URL: https://github.com/o/r(.git)
// or git@github.com:o/r(.git).
func ParseGitHubRemote(remote string) (owner, repo string, ok bool) {
	return signature.ParseGitHubRemote(remote)
}

type ghUser struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

func (g GitHub) get(ctx context.Context, p string, out interface{}) error {
	base := g.BaseURL
	if base == "" {
		base = "https://api.github.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+p, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "lattice-confirm")
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	c := g.Client
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", p, resp.Status)
	}
	return json.Unmarshal(body, out)
}

// PullRequestForCommit lists the pull requests that contain the commit and
// prefers the merged one whose merge commit it is.
func (g GitHub) PullRequestForCommit(ctx context.Context, sha string) (*PullRequest, error) {
	var prs []struct {
		Number         int     `json:"number"`
		User           ghUser  `json:"user"`
		MergedAt       *string `json:"merged_at"`
		MergeCommitSHA string  `json:"merge_commit_sha"`
		Head           struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	p := fmt.Sprintf("/repos/%s/%s/commits/%s/pulls", url.PathEscape(g.Owner), url.PathEscape(g.Repo), url.PathEscape(sha))
	if err := g.get(ctx, p, &prs); err != nil {
		return nil, err
	}
	pick := -1
	for i, pr := range prs {
		if pr.MergedAt == nil {
			continue
		}
		if strings.EqualFold(pr.MergeCommitSHA, sha) {
			pick = i
			break
		}
		if pick < 0 {
			pick = i
		}
	}
	if pick < 0 && len(prs) > 0 {
		pick = 0
	}
	if pick < 0 {
		return nil, nil
	}
	pr := prs[pick]
	return &PullRequest{Number: pr.Number, Author: pr.User.Login, AuthorType: pr.User.Type, Merged: pr.MergedAt != nil,
		HeadSHA: pr.Head.SHA, MergeSHA: pr.MergeCommitSHA}, nil
}

// PullAuthor names who opened the pull request that merged commit sha, and
// their account type; "" when no request is known.
func (g GitHub) PullAuthor(ctx context.Context, sha string) (string, string, error) {
	pr, err := g.PullRequestForCommit(ctx, sha)
	if err != nil || pr == nil {
		return "", "", err
	}
	return pr.Author, pr.AuthorType, nil
}

// Reviews lists a pull request's reviews, oldest first.
func (g GitHub) Reviews(ctx context.Context, number int) ([]Review, error) {
	var all []Review
	for page := 1; page <= 20; page++ {
		var rs []struct {
			User     ghUser `json:"user"`
			State    string `json:"state"`
			CommitID string `json:"commit_id"`
		}
		p := fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews?per_page=100&page=%d", url.PathEscape(g.Owner), url.PathEscape(g.Repo), number, page)
		if err := g.get(ctx, p, &rs); err != nil {
			return nil, err
		}
		for _, r := range rs {
			all = append(all, Review{User: r.User.Login, UserType: r.User.Type, State: r.State, CommitID: r.CommitID})
		}
		if len(rs) < 100 {
			break
		}
	}
	return all, nil
}

// SigningKeys lists a user's SSH signing keys (GET /users/{login}/ssh_signing_keys),
// in authorized_keys form. These are the keys GitHub holds for signing, not
// its authentication keys.
func (g GitHub) SigningKeys(ctx context.Context, login string) ([]string, error) {
	var all []string
	for page := 1; page <= 10; page++ {
		var ks []struct {
			Key string `json:"key"`
		}
		p := fmt.Sprintf("/users/%s/ssh_signing_keys?per_page=100&page=%d", url.PathEscape(login), page)
		if err := g.get(ctx, p, &ks); err != nil {
			return nil, err
		}
		for _, k := range ks {
			all = append(all, k.Key)
		}
		if len(ks) < 100 {
			break
		}
	}
	return all, nil
}

// Collaborators lists the repository's collaborators, direct or through an
// organization (GET /repos/{owner}/{repo}/collaborators?affiliation=all),
// with whether each can push. The endpoint needs push access; without it
// the error is returned and no note is made.
func (g GitHub) Collaborators(ctx context.Context) ([]Collaborator, error) {
	var all []Collaborator
	for page := 1; page <= 10; page++ {
		var cs []struct {
			Login       string `json:"login"`
			Type        string `json:"type"`
			Permissions struct {
				Admin    bool `json:"admin"`
				Maintain bool `json:"maintain"`
				Push     bool `json:"push"`
			} `json:"permissions"`
		}
		p := fmt.Sprintf("/repos/%s/%s/collaborators?affiliation=all&per_page=100&page=%d", url.PathEscape(g.Owner), url.PathEscape(g.Repo), page)
		if err := g.get(ctx, p, &cs); err != nil {
			return nil, err
		}
		for _, c := range cs {
			all = append(all, Collaborator{Login: c.Login, Type: c.Type,
				Write: c.Permissions.Push || c.Permissions.Maintain || c.Permissions.Admin})
		}
		if len(cs) < 100 {
			return all, nil
		}
	}
	return nil, fmt.Errorf("more than 1000 collaborators")
}
