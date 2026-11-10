package confirm

// @module-feature confirm

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// PullRequest is what the platform says about the request that merged a commit.
type PullRequest struct {
	Number int
	// Author is the login that opened the request.
	Author string
	Merged bool
	// HeadSHA is the head of the request's branch when it merged: the last
	// commit its reviewers could have seen.
	HeadSHA string
	// MergeSHA is the commit the merge produced on the base branch.
	MergeSHA string
}

// Review is one review on a pull request, oldest first.
type Review struct {
	User string
	// UserType is the platform's account type: "User", "Bot", ...
	UserType string
	// State is APPROVED, CHANGES_REQUESTED, DISMISSED, COMMENTED or PENDING.
	State string
	// CommitID is the head commit the review was made on.
	CommitID string
}

// Platform is the code host. The one implementation that talks to a network
// is GitHub; tests use a fake. A nil request with a nil error means the
// platform knows no pull request for the commit.
type Platform interface {
	PullRequestForCommit(ctx context.Context, sha string) (*PullRequest, error)
	Reviews(ctx context.Context, number int) ([]Review, error)
}

// Verdict states of a record.
const (
	StateConfirmed = "confirmed" // a human approved the merged head and the packet still matches
	StateStale     = "stale"     // merged and approved, but the packet no longer matches
	StatePending   = "pending"   // prepared, not merged, or no qualifying approval
	StateUnknown   = "unknown"   // nothing to judge: no platform data
)

// Outcome is what the platform says about one decision commit.
type Outcome struct {
	State    string
	Code     string
	Text     string
	Params   map[string]interface{}
	Approver string
	PR       int
	Merged   string
}

// IsBot reports whether a login or account type names a bot.
func IsBot(login, userType string) bool {
	return strings.HasSuffix(strings.ToLower(login), "[bot]") || strings.EqualFold(userType, "Bot")
}

// Verify decides whether the pull request that merged commit sha carries a
// human approval. A decision counts only if an approving review exists from a
// user other than the author, that user is not a bot or an agent, and the
// review is on the head that was merged. Branch protection should also
// require a current review, but this check does not depend on it.
// @enforces confirm:INV-1, confirm:INV-2
func Verify(ctx context.Context, p Platform, sha string) Outcome {
	pr, err := p.PullRequestForCommit(ctx, sha)
	if err != nil {
		return Outcome{State: StateUnknown, Code: "PLATFORM_ERROR", Text: "the platform could not be read: " + err.Error()}
	}
	if pr == nil {
		return Outcome{State: StatePending, Code: "NO_PR", Text: "no pull request is known for commit " + short(sha)}
	}
	out := Outcome{PR: pr.Number, Merged: pr.MergeSHA}
	if out.Merged == "" {
		out.Merged = sha
	}
	if !pr.Merged {
		out.State, out.Code, out.Text = StatePending, "NOT_MERGED", fmt.Sprintf("PR #%d is not merged", pr.Number)
		return out
	}
	reviews, err := p.Reviews(ctx, pr.Number)
	if err != nil {
		out.State, out.Code, out.Text = StateUnknown, "PLATFORM_ERROR", "the reviews could not be read: "+err.Error()
		return out
	}

	// A reviewer's last verdict stands; comments and pending drafts are not verdicts.
	last := map[string]Review{}
	var order []string
	for _, r := range reviews {
		switch strings.ToUpper(r.State) {
		case "APPROVED", "CHANGES_REQUESTED", "DISMISSED":
		default:
			continue
		}
		key := strings.ToLower(r.User)
		if _, ok := last[key]; !ok {
			order = append(order, key)
		}
		last[key] = r
	}
	sort.Strings(order)

	rejected := map[string][]string{}
	for _, key := range order {
		r := last[key]
		if strings.ToUpper(r.State) != "APPROVED" {
			continue
		}
		switch {
		case strings.EqualFold(r.User, pr.Author):
			rejected["SELF_APPROVAL"] = append(rejected["SELF_APPROVAL"], r.User)
		case IsBot(r.User, r.UserType):
			rejected["BOT_APPROVAL"] = append(rejected["BOT_APPROVAL"], r.User)
		case schema.IsAgentActor(r.User):
			rejected["AGENT_APPROVAL"] = append(rejected["AGENT_APPROVAL"], r.User)
		case !strings.EqualFold(r.CommitID, pr.HeadSHA):
			rejected["STALE_HEAD"] = append(rejected["STALE_HEAD"], r.User)
		default:
			out.State, out.Code, out.Approver = StateConfirmed, "APPROVED", r.User
			out.Text = fmt.Sprintf("approved by %s on the merged head of PR #%d", r.User, pr.Number)
			return out
		}
	}

	out.State = StatePending
	for _, code := range []string{"STALE_HEAD", "SELF_APPROVAL", "BOT_APPROVAL", "AGENT_APPROVAL"} {
		if who := rejected[code]; len(who) > 0 {
			out.Code, out.Params = code, map[string]interface{}{"users": who, "pr": pr.Number}
			out.Text = map[string]string{
				"STALE_HEAD":     "the approval is not on the head that was merged (" + short(pr.HeadSHA) + ")",
				"SELF_APPROVAL":  "the author approved their own request",
				"BOT_APPROVAL":   "the approver is a bot",
				"AGENT_APPROVAL": "the approver is an agent actor",
			}[code] + ": " + strings.Join(who, ", ")
			return out
		}
	}
	out.Code, out.Text = "NO_APPROVAL", fmt.Sprintf("PR #%d has no approving review", pr.Number)
	return out
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
