package confirm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// fake is a Platform with no network: canned pull requests and reviews.
type fake struct {
	pr      *PullRequest
	reviews []Review
	err     error
	calls   int
}

func (f *fake) PullRequestForCommit(context.Context, string) (*PullRequest, error) {
	f.calls++
	return f.pr, f.err
}
func (f *fake) Reviews(context.Context, int) ([]Review, error) { return f.reviews, f.err }

func merged(author, head string) *PullRequest {
	return &PullRequest{Number: 12, Author: author, Merged: true, HeadSHA: head, MergeSHA: "m1"}
}

func approval(user, commit string) Review {
	return Review{User: user, UserType: "User", State: "APPROVED", CommitID: commit}
}

// A decision counts only for an approving review from a user other than the
// author, who is not a bot, on the head that was merged.
// @verifies confirm:INV-1
// @verifies confirm:INV-2
func TestVerify(t *testing.T) {
	cases := []struct {
		name     string
		pr       *PullRequest
		reviews  []Review
		state    string
		code     string
		approver string
	}{
		{"approving non-author human", merged("dev", "h2"), []Review{approval("ana", "h2")}, StateConfirmed, "APPROVED", "ana"},
		{"self-approval", merged("dev", "h2"), []Review{approval("dev", "h2")}, StatePending, "SELF_APPROVAL", ""},
		{"self-approval, other case", merged("Dev", "h2"), []Review{approval("dEV", "h2")}, StatePending, "SELF_APPROVAL", ""},
		{"bot by suffix", merged("dev", "h2"), []Review{approval("greptile[bot]", "h2")}, StatePending, "BOT_APPROVAL", ""},
		{"bot by type", merged("dev", "h2"), []Review{{User: "robot", UserType: "Bot", State: "APPROVED", CommitID: "h2"}}, StatePending, "BOT_APPROVAL", ""},
		{"agent actor login", merged("dev", "h2"), []Review{approval("agent-approver", "h2")}, StatePending, "AGENT_APPROVAL", ""},
		{"stale head", merged("dev", "h2"), []Review{approval("ana", "h1")}, StatePending, "STALE_HEAD", ""},
		{"stale head, then a bot on the head", merged("dev", "h2"), []Review{approval("ana", "h1"), approval("x[bot]", "h2")}, StatePending, "STALE_HEAD", ""},
		{"a second human on the head still counts", merged("dev", "h2"), []Review{approval("dev", "h2"), approval("bo", "h2")}, StateConfirmed, "APPROVED", "bo"},
		{"comment only", merged("dev", "h2"), []Review{{User: "ana", State: "COMMENTED", CommitID: "h2"}}, StatePending, "NO_APPROVAL", ""},
		{"approval then changes requested", merged("dev", "h2"), []Review{approval("ana", "h2"), {User: "ana", State: "CHANGES_REQUESTED", CommitID: "h2"}}, StatePending, "NO_APPROVAL", ""},
		{"approval dismissed", merged("dev", "h2"), []Review{approval("ana", "h2"), {User: "ana", State: "DISMISSED", CommitID: "h2"}}, StatePending, "NO_APPROVAL", ""},
		{"not merged", &PullRequest{Number: 12, Author: "dev", HeadSHA: "h2"}, []Review{approval("ana", "h2")}, StatePending, "NOT_MERGED", ""},
		{"no pull request", nil, nil, StatePending, "NO_PR", ""},
	}
	for _, c := range cases {
		o := Verify(context.Background(), &fake{pr: c.pr, reviews: c.reviews}, "abc")
		if o.State != c.state || o.Code != c.code || o.Approver != c.approver {
			t.Errorf("%s: got %s/%s/%q (%s), want %s/%s/%q", c.name, o.State, o.Code, o.Approver, o.Text, c.state, c.code, c.approver)
		}
	}
	if o := Verify(context.Background(), &fake{err: errors.New("boom")}, "abc"); o.State != StateUnknown {
		t.Errorf("platform error must read unknown, got %s", o.State)
	}
}

func decision(action string) Decision {
	return Decision{Action: action, ID: "brd.x", Version: 2, PacketHash: "sha256:aa", PreparedBy: "Dev <d@example.com>", PreparedAt: "2026-01-01T00:00:00Z"}
}

var fixedNow = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

func foundOne(d Decision) []Found {
	return []Found{{Decision: d, Path: "lattice/decisions/approvals/" + d.Stem() + ".yaml", AddedSHA: "c0ffee", OnDefault: true, Ref: "main"}}
}

// A merged, approved decision is confirmed while its packet hash matches and
// stale once it does not.
// @verifies confirm:INV-3
func TestComputePacketHash(t *testing.T) {
	plat := &fake{pr: merged("dev", "h2"), reviews: []Review{approval("ana", "h2")}}
	in := Input{Commit: "head1", Branch: "main", Found: foundOne(decision(ActionApprove)), Platform: plat, Now: fixedNow}

	in.Packet = func(Decision) (string, bool) { return "sha256:aa", true }
	r := Compute(context.Background(), in).Records[0]
	if r.State != StateConfirmed || r.ApproverLogin != "ana" || r.PR != 12 || r.MergedSHA != "m1" || r.VerifiedAt == "" {
		t.Fatalf("matching hash: %+v", r)
	}

	in.Packet = func(Decision) (string, bool) { return "sha256:bb", true }
	r = Compute(context.Background(), in).Records[0]
	if r.State != StateStale || r.Assurance.Code != "PACKET_MISMATCH" {
		t.Fatalf("changed hash must read stale: %+v", r)
	}

	// The BRD moved past the version: the hash cannot be compared and the
	// decision stays what the platform said; the audit reads it as drifted.
	in.Packet = func(Decision) (string, bool) { return "", false }
	if r = Compute(context.Background(), in).Records[0]; r.State != StateConfirmed {
		t.Fatalf("version moved on: %+v", r)
	}
}

func TestPacketHashIgnoresStatusAndApproval(t *testing.T) {
	b := schema.BRD{ID: "brd.x", Version: 2, Status: schema.BRDDraft, Title: "t",
		SuccessCriteria: []schema.BRDCriterion{{ID: "SC-1", Statement: "s", MapsToInvariant: schema.InvariantRefs{"f:INV-1"}}}}
	base := PacketHash(PacketInput{BRD: b, Diff: []string{"initial"}})
	approved := b
	approved.Status, approved.Approval = schema.BRDApproved, &schema.BRDApproval{ApprovedBy: "x", ApprovedVersion: 2}
	if got := PacketHash(PacketInput{BRD: approved, Diff: []string{"initial"}}); got != base {
		t.Error("status and approval block must not change the packet hash")
	}
	edited := b
	edited.SuccessCriteria = []schema.BRDCriterion{{ID: "SC-1", Statement: "changed", MapsToInvariant: schema.InvariantRefs{"f:INV-1"}}}
	if PacketHash(PacketInput{BRD: edited, Diff: []string{"initial"}}) == base {
		t.Error("a statement edit must change the packet hash")
	}
	if PacketHash(PacketInput{BRD: b, Diff: []string{"+criterion SC-2"}}) == base {
		t.Error("the diff must change the packet hash")
	}
	facts := []PromiseFacts{{Promise: "f:INV-1", Statement: "s", Tier: 1, Enforcers: []string{"a"}}}
	if PacketHash(PacketInput{BRD: b, Promises: facts, Diff: []string{"initial"}}) == base {
		t.Error("promise facts must change the packet hash")
	}
}

func TestStructuralDiff(t *testing.T) {
	prev := schema.BRD{SuccessCriteria: []schema.BRDCriterion{
		{ID: "SC-1", Statement: "a", Tier: 1, MapsToInvariant: schema.InvariantRefs{"f:INV-1"}},
		{ID: "SC-2", Statement: "b"}}}
	cur := schema.BRD{SuccessCriteria: []schema.BRDCriterion{
		{ID: "SC-1", Statement: "a2", Tier: 3, MapsToInvariant: schema.InvariantRefs{"f:INV-2"}},
		{ID: "SC-3", Statement: "c"}},
		Constraints: []schema.BRDConstraint{{Kind: schema.ConstraintLegal, Ref: "C-1"}}}
	got := strings.Join(StructuralDiff(&prev, cur), "|")
	for _, want := range []string{"~statement SC-1", "~tier SC-1 1->3", "~mapping SC-1", "-criterion SC-2", "+criterion SC-3", "+constraint legal|C-1|"} {
		if !strings.Contains(got, want) {
			t.Errorf("diff %q misses %q", got, want)
		}
	}
	if d := StructuralDiff(nil, cur); len(d) != 1 || d[0] != "initial" {
		t.Errorf("no previous version: %v", d)
	}
	if d := StructuralDiff(&cur, cur); len(d) != 1 || d[0] != "none" {
		t.Errorf("no change: %v", d)
	}
}

// Prepared decisions that are not merged are pending; with no platform, the
// decisions on the default branch are unknown, never confirmed; an offline
// run with a missing cache reads unknown.
// @verifies confirm:INV-4
func TestUnknownIsNeverConfirmed(t *testing.T) {
	d := decision(ActionApprove)
	f := Compute(context.Background(), Input{Commit: "h", Found: []Found{{Decision: d, OnDefault: false, Ref: "decision/x"}}, Platform: &fake{}, Now: fixedNow})
	if f.Records[0].State != StatePending || f.Records[0].Assurance.Code != "NOT_MERGED" {
		t.Errorf("unmerged: %+v", f.Records[0])
	}

	f = Compute(context.Background(), Input{Commit: "h", Found: foundOne(d), Now: fixedNow})
	if f.Records[0].State != StateUnknown || f.Assurance.Code != "NO_PLATFORM" {
		t.Errorf("no platform: %+v / %+v", f.Records[0], f.Assurance)
	}

	off := Offline(nil, "h")
	if off.Assurance.State != StateUnknown || off.Assurance.Code != "NO_CACHE" || len(off.Records) != 0 || !off.Offline {
		t.Errorf("offline, no cache: %+v", off)
	}

	plat := &fake{pr: merged("dev", "h2"), reviews: []Review{approval("ana", "h2")}}
	online := Compute(context.Background(), Input{Commit: "h", Found: foundOne(d), Platform: plat, Now: fixedNow})
	if got := Offline(&online, "h2"); got.Records[0].State != StateConfirmed || !got.Offline {
		t.Errorf("offline with cache: %+v", got)
	}
}

// A decision already verified at the same commit is not asked about again.
func TestCacheSkipsThePlatform(t *testing.T) {
	d := decision(ActionApprove)
	plat := &fake{pr: merged("dev", "h2"), reviews: []Review{approval("ana", "h2")}}
	first := Compute(context.Background(), Input{Commit: "h", Found: foundOne(d), Platform: plat, Now: fixedNow})
	calls := plat.calls
	second := Compute(context.Background(), Input{Commit: "h", Found: foundOne(d), Platform: plat, Cache: &first, Now: fixedNow})
	if plat.calls != calls || second.Records[0].ApproverLogin != "ana" {
		t.Errorf("platform asked again (%d -> %d): %+v", calls, plat.calls, second.Records[0])
	}
	Compute(context.Background(), Input{Commit: "h", Found: foundOne(d), Platform: plat, Cache: &first, Refresh: true, Now: fixedNow})
	if plat.calls == calls {
		t.Error("--refresh must ask the platform")
	}
}

func TestCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if LoadCache(dir) != nil {
		t.Fatal("no file, no cache")
	}
	f := Compute(context.Background(), Input{Commit: "h", Found: foundOne(decision(ActionApprove)), Now: fixedNow})
	if err := SaveCache(dir, f); err != nil {
		t.Fatal(err)
	}
	if got := LoadCache(dir); got == nil || got.Commit != "h" || len(got.Records) != 1 {
		t.Fatalf("loaded %+v", got)
	}
	if err := os.WriteFile(CachePath(dir), []byte(`{"contract":"other/1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if LoadCache(dir) != nil {
		t.Error("another schema must not load")
	}
}

// The shape of lattice.confirm/1 is frozen by a golden file.
func TestConfirmContractGolden(t *testing.T) {
	plat := &fake{pr: merged("dev", "h2"), reviews: []Review{approval("ana", "h2")}}
	ack := decision(ActionPromiseConfirm)
	ack.Rows = []Row{{Promise: "f:INV-1"}}
	found := append(foundOne(decision(ActionApprove)), foundOne(ack)...)
	found = append(found, Found{Decision: Decision{Action: ActionReturn, ID: "brd.y", Version: 1}, Ref: "decision/brd.y-v1.return"})
	f := Compute(context.Background(), Input{Commit: "deadbeef", Branch: "main", Found: found, Platform: plat, Now: fixedNow,
		Packet: func(d Decision) (string, bool) { return d.PacketHash, true }})
	got, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "testdata/confirm.golden.json", append(got, '\n'))
}

func golden(t *testing.T, rel string, got []byte) {
	t.Helper()
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(rel, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("%v (run with UPDATE_GOLDEN=1)", err)
	}
	if string(want) != string(got) {
		t.Errorf("%s changed; the contract is frozen. got:\n%s", rel, got)
	}
}

func TestDecisionFile(t *testing.T) {
	d := decision(ActionApprove)
	d.Rows = []Row{{Promise: "f:INV-1", Statement: "s", Tier: 2, Class: "unverified"}}
	data, err := d.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseDecision(data)
	if err != nil || back.ID != d.ID || back.Version != 2 || back.PacketHash != d.PacketHash || len(back.Rows) != 1 {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	if d.Branch() != "decision/brd.x-v2" || d.RelPath() != "decisions/approvals/brd.x-v2.yaml" {
		t.Errorf("approval names: %s %s", d.Branch(), d.RelPath())
	}
	for _, a := range []string{ActionReturn, ActionAck, ActionOverturn} {
		x := d
		x.Action = a
		if x.Stem() == d.Stem() {
			t.Errorf("%s must not share the approval's file", a)
		}
	}
	if _, err := ParseDecision([]byte("action: nope\nid: a\nversion: 1\n")); err == nil {
		t.Error("an unknown action must not parse")
	}
}

func TestGitHubClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/commits/abc/pulls"):
			_, _ = w.Write([]byte(`[{"number":3,"user":{"login":"dev","type":"User"},"merged_at":null,"merge_commit_sha":"x","head":{"sha":"hh"}},
				{"number":4,"user":{"login":"dev","type":"User"},"merged_at":"2026-01-01T00:00:00Z","merge_commit_sha":"abc","head":{"sha":"h4"}}]`))
		case strings.HasSuffix(r.URL.Path, "/pulls/4/reviews"):
			_, _ = w.Write([]byte(`[{"user":{"login":"ana","type":"User"},"state":"APPROVED","commit_id":"h4"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	g := GitHub{Token: "tok", Owner: "o", Repo: "r", BaseURL: srv.URL}
	o := Verify(context.Background(), g, "abc")
	if o.State != StateConfirmed || o.Approver != "ana" || o.PR != 4 {
		t.Errorf("%+v", o)
	}
	if o := Verify(context.Background(), GitHub{Owner: "o", Repo: "r", BaseURL: srv.URL}, "abc"); o.State != StateUnknown {
		t.Errorf("an unauthorised read must read unknown, got %s", o.State)
	}
	if owner, repo, ok := ParseGitHubRemote("git@github.com:acme/widgets.git"); !ok || owner != "acme" || repo != "widgets" {
		t.Errorf("ssh remote: %s %s %v", owner, repo, ok)
	}
	if owner, repo, ok := ParseGitHubRemote("https://github.com/acme/widgets"); !ok || owner != "acme" || repo != "widgets" {
		t.Errorf("https remote: %s %s %v", owner, repo, ok)
	}
}

// Scan finds a decision on the default branch with the commit that added it,
// and a decision that exists only on a decision branch.
func TestScan(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	g := Git{Dir: dir}
	run := func(args ...string) {
		t.Helper()
		if _, err := g.RunEnv([]string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com"}, nil, args...); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main")
	write("lattice/config.yaml", "x: 1\n")
	run("add", "-A")
	run("commit", "-q", "-m", "init")

	merged := decision(ActionApprove)
	data, _ := merged.Marshal()
	write("lattice/"+merged.RelPath(), string(data))
	run("add", "-A")
	run("commit", "-q", "-m", "decision")
	addedAt, _ := g.Head()

	run("checkout", "-q", "-b", "decision/brd.y-v1")
	prepared := Decision{Action: ActionApprove, ID: "brd.y", Version: 1, PacketHash: "sha256:cc", PreparedBy: "d", PreparedAt: "2026-01-01T00:00:00Z"}
	data, _ = prepared.Marshal()
	write("lattice/"+prepared.RelPath(), string(data))
	run("add", "-A")
	run("commit", "-q", "-m", "prepared")
	run("checkout", "-q", "main")

	found, warnings, err := Scan(g, "main", "lattice")
	if err != nil || len(warnings) != 0 {
		t.Fatalf("scan: %v %v", err, warnings)
	}
	byID := map[string]Found{}
	for _, f := range found {
		byID[f.Decision.ID] = f
	}
	if f := byID["brd.x"]; !f.OnDefault || f.AddedSHA != addedAt {
		t.Errorf("merged decision: %+v (want added %s)", f, addedAt)
	}
	if f := byID["brd.y"]; f.OnDefault || f.Ref != "refs/heads/decision/brd.y-v1" {
		t.Errorf("prepared decision: %+v", f)
	}
	if got := DefaultBranch(g); got != "main" {
		t.Errorf("default branch %s", got)
	}
	if b, err := BRDAt(g, "lattice", "HEAD", "none"); err == nil || b != nil {
		t.Error("a missing BRD must not load")
	}
}
