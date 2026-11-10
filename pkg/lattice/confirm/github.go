package confirm

// @module-feature confirm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
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
	m := regexp.MustCompile(`github\.com[:/]+([^/]+)/([^/]+?)(?:\.git)?/?$`).FindStringSubmatch(strings.TrimSpace(remote))
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
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
	return &PullRequest{Number: pr.Number, Author: pr.User.Login, Merged: pr.MergedAt != nil,
		HeadSHA: pr.Head.SHA, MergeSHA: pr.MergeCommitSHA}, nil
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
