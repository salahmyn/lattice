package prepare

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
	"github.com/salahmyn/lattice/pkg/lattice/brd"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/confirm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

type fixture struct {
	t   *testing.T
	dir string
	g   confirm.Git
	kg  schema.KnowledgeGraph
	cfg config.Config
}

var gitEnv = []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	"GIT_AUTHOR_NAME=Dev", "GIT_AUTHOR_EMAIL=dev@example.com", "GIT_COMMITTER_NAME=Dev", "GIT_COMMITTER_EMAIL=dev@example.com"}

func (f *fixture) git(args ...string) string {
	f.t.Helper()
	out, err := f.g.RunEnv(gitEnv, nil, args...)
	if err != nil {
		f.t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func (f *fixture) write(rel, body string) {
	f.t.Helper()
	p := filepath.Join(f.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func criterion(id string, tier int, refs ...string) schema.BRDCriterion {
	return schema.BRDCriterion{ID: id, Statement: "statement " + id, Tier: tier, MapsToInvariant: schema.InvariantRefs(refs)}
}

// newFixture is a git repository holding lattice/ with two BRDs: brd.x (v1,
// promises f:INV-1 enforced and f:INV-2 not) and brd.y (f:INV-3, enforced).
func newFixture(t *testing.T) *fixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for _, kv := range gitEnv {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	f := &fixture{t: t, dir: t.TempDir()}
	f.g = confirm.Git{Dir: f.dir}
	f.git("init", "-q", "-b", "main")

	f.kg = schema.KnowledgeGraph{
		GeneratedFromCommit: "abc",
		Features: []schema.Manifest{{ID: "f", Version: 1, Status: schema.StatusProduction, Invariants: []schema.Invariant{
			{ID: "INV-1", Statement: "one"}, {ID: "INV-2", Statement: "two"}, {ID: "INV-3", Statement: "three"}, {ID: "INV-4", Statement: "four"}}}},
		BRDs: []schema.BRD{
			{ID: "brd.x", Version: 1, Status: schema.BRDDraft, Title: "x", SuccessCriteria: []schema.BRDCriterion{
				criterion("SC-1", 1, "f:INV-1"), criterion("SC-2", 1, "f:INV-2")}},
			{ID: "brd.y", Version: 1, Status: schema.BRDDraft, Title: "y", SuccessCriteria: []schema.BRDCriterion{criterion("SC-1", 1, "f:INV-3")}},
			{ID: "brd.z", Version: 1, Status: schema.BRDDraft, Title: "z", SuccessCriteria: []schema.BRDCriterion{criterion("SC-1", 3, "f:INV-4")}},
		},
	}
	for _, n := range []string{"INV-1", "INV-3", "INV-4"} {
		f.kg.Symbols = append(f.kg.Symbols, schema.GraphSymbol{FQN: "f.Enf" + n, File: "f.go", Line: 3, Feature: "f", EnforcesInvariants: []string{n}})
	}
	for _, b := range f.kg.BRDs {
		f.saveBRD(b)
	}
	f.write("lattice/config.yaml", "agentic:\n  llm:\n    enabled: false\n")
	f.commit("init")
	return f
}

func (f *fixture) saveBRD(b schema.BRD) {
	f.t.Helper()
	data, err := schema.MarshalCanonical(b)
	if err != nil {
		f.t.Fatal(err)
	}
	f.write("lattice/brds/"+b.ID+".yaml", string(data))
}

func (f *fixture) commit(msg string) {
	f.t.Helper()
	f.git("add", "-A")
	f.git("commit", "-q", "-m", msg)
}

func (f *fixture) env(actor string) Env {
	return Env{
		Git: f.g, LatticeDir: filepath.Join(f.dir, "lattice"), KG: f.kg,
		Report: audit.Compute(audit.Sources{KG: f.kg}), Config: f.cfg, Actor: actor,
		Now: func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	}
}

func (f *fixture) brd(id string) *schema.BRD { return brd.Find(f.kg.BRDs, id) }

func (f *fixture) setBRD(b schema.BRD) {
	for i := range f.kg.BRDs {
		if f.kg.BRDs[i].ID == b.ID {
			f.kg.BRDs[i] = b
		}
	}
}

// state is everything a prepare must leave alone.
func (f *fixture) state() string {
	index, _ := os.ReadFile(filepath.Join(f.dir, ".git", "index"))
	return strings.Join([]string{
		f.git("rev-parse", "HEAD"), f.git("symbolic-ref", "HEAD"), f.git("status", "--porcelain", "--untracked-files=all"),
		f.git("ls-files", "-s"), string(index), f.git("for-each-ref", "--format=%(refname)", "refs/remotes", "refs/tags"),
	}, "\n--\n")
}

func approveReq(id string, v int) Request {
	return Request{Action: confirm.ActionApprove, ID: id, Version: v}
}

// prepare writes the decision branch from HEAD and leaves the checkout, the
// index and the working tree exactly as they were.
// @verifies confirm:INV-5
func TestPrepareWritesBranchAndLeavesCheckoutAlone(t *testing.T) {
	f := newFixture(t)
	// An unrelated edit outside lattice/ and an untracked file must survive.
	f.write("README.md", "notes\n")
	f.write("scratch.txt", "untracked\n")
	before := f.state()
	head := f.git("rev-parse", "HEAD")

	res, ref := Run(f.env(""), approveReq("brd.x", 1))
	if ref != nil {
		t.Fatalf("refused: %+v", ref)
	}
	if after := f.state(); after != before {
		t.Fatalf("the checkout changed:\n--- before\n%s\n--- after\n%s", before, after)
	}
	if b, _ := os.ReadFile(filepath.Join(f.dir, "scratch.txt")); string(b) != "untracked\n" {
		t.Error("the working tree changed")
	}

	// The name is the BRD, the version and the first 8 digits of the packet.
	stem := "brd.x-v1-" + strings.TrimPrefix(res.PacketHash, "sha256:")[:8]
	branch, file := "decision/"+stem, "lattice/decisions/approvals/"+stem+".yaml"
	if res.Branch != branch || res.Commit != head || res.Base != "main" {
		t.Errorf("result %+v", res)
	}
	if got := f.git("rev-parse", "refs/heads/"+branch+"^"); got != head {
		t.Errorf("the branch must start at HEAD: parent %s, HEAD %s", got, head)
	}
	if got := f.git("diff", "--name-only", head, branch); got != file {
		t.Errorf("the branch changes %q", got)
	}
	d, err := confirm.ParseDecision([]byte(f.git("show", branch+":"+file)))
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != confirm.ActionApprove || d.Version != 1 || d.PacketHash != res.PacketHash || len(d.Rows) != 2 ||
		!strings.Contains(d.PreparedBy, "dev@example.com") || d.PreparedAt != "2026-01-02T03:04:05Z" {
		t.Errorf("decision file %+v", d)
	}
	if !strings.HasPrefix(res.Command, "gh pr create --head "+branch+" --base main ") || !strings.Contains(res.Command, "--title ") {
		t.Errorf("command %q", res.Command)
	}
	// The BRD file on the branch is the one at HEAD: prepare decides, it does not edit.
	if f.git("rev-parse", branch+":lattice/brds/brd.x.yaml") != f.git("rev-parse", "HEAD:lattice/brds/brd.x.yaml") {
		t.Error("the BRD file differs on the branch")
	}
}

// The same inputs give the same packet hash; a change to the BRD does not.
func TestPacketHashIsDeterministic(t *testing.T) {
	f := newFixture(t)
	a, _ := Run(f.env(""), approveReq("brd.x", 1))
	if a == nil {
		t.Fatal("first prepare refused")
	}
	f.git("branch", "-D", a.Branch)
	b, _ := Run(f.env(""), approveReq("brd.x", 1))
	if b == nil || a.PacketHash != b.PacketHash || a.Branch != b.Branch {
		t.Fatalf("hashes differ: %+v %+v", a, b)
	}
	x := *f.brd("brd.x")
	x.SuccessCriteria[0].Statement = "changed"
	f.setBRD(x)
	f.saveBRD(x)
	f.commit("edit")
	c, _ := Run(f.env(""), approveReq("brd.x", 1))
	if c == nil || c.PacketHash == a.PacketHash || c.Branch == a.Branch {
		t.Fatalf("an edited BRD must change the packet hash and the name: %+v", c)
	}
}

func councilFixture(t *testing.T, votes []schema.BRDCouncilVote) (*fixture, schema.BRD) {
	f := newFixture(t)
	f.cfg.Autonomy = config.Autonomy{DefaultMode: "council", Mandates: []config.Mandate{{
		ID: "m1", Grants: config.GrantBRDApprove, TierMax: 2, DelegatedBy: "owner@example.com",
		Council: &config.Council{Seats: []string{"intent", "risk"}},
	}}}
	b := *f.brd("brd.x")
	b.Status = schema.BRDApproved
	b.Provenance = schema.BRDProvenance{Source: schema.BRDSourceHuman, AuthoredBy: "author@example.com"}
	b.Approval = &schema.BRDApproval{ApprovedBy: "council:m1", ApprovedAt: "2026-01-01T00:00:00Z", ApprovedVersion: 1,
		Mandate: "m1", DelegatedBy: "owner@example.com", HumanReview: schema.HumanReviewPending,
		Decision: &schema.BRDCouncilDecision{Outcome: "approved", Summary: "s", Votes: votes}}
	f.setBRD(b)
	rec := brd.CouncilRecord{BRD: "brd.x", Version: 1, Votes: votes}
	data, err := yaml.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	f.write("lattice/decisions/brd-council/brd.x-v1.votes.yaml", string(data))
	f.commit("votes")
	return f, b
}

func vote(seat, actor, verdict string) schema.BRDCouncilVote {
	return schema.BRDCouncilVote{Seat: seat, Actor: actor, Verdict: verdict, Reason: "because"}
}

// Every refusal code, from the kernel's rules.
// @verifies confirm:INV-5
func TestRefusals(t *testing.T) {
	f := newFixture(t)
	env := f.env("")

	type tc struct {
		name string
		env  Env
		req  Request
		want string
	}
	machine := *f.brd("brd.x")
	machine.Provenance = schema.BRDProvenance{Source: schema.BRDSourceLLMFromCode, AuthoredBy: "agent:impl-2"}
	machine.Constraints = []schema.BRDConstraint{{Kind: schema.ConstraintLegal, Ref: "C-1"}}
	machineFix := newFixture(t)
	machineFix.setBRD(machine)

	superseded := newFixture(t)
	sb := *superseded.brd("brd.x")
	sb.Status = schema.BRDSuperseded
	superseded.setBRD(sb)

	known := schema.BRDCouncilVote{}
	_ = known
	twoVotes := []schema.BRDCouncilVote{vote("intent", "agent:a", "approve"), vote("risk", "agent:b", "approve")}
	council, _ := councilFixture(t, twoVotes)
	incomplete, _ := councilFixture(t, twoVotes[:1])
	humanSeats, _ := councilFixture(t, []schema.BRDCouncilVote{vote("intent", "seat@example.com", "approve"), vote("risk", "dev@example.com", "approve")})
	moved, mb := councilFixture(t, twoVotes)
	mb.Version = 2
	moved.setBRD(mb)
	tier3, t3 := councilFixture(t, twoVotes)
	t3.SuccessCriteria[0].Tier = 3
	tier3.setBRD(t3)
	_ = tier3

	// The branch exists and the lattice directory is dirty.
	dirty := newFixture(t)
	dirty.write("lattice/brds/brd.x.yaml", "id: brd.x\nversion: 9\n")

	waiver := func(w confirm.WaiverEntry) Request { return Request{Action: confirm.ActionWaiverAdd, Waiver: &w} }
	okW := confirm.WaiverEntry{Code: "NO_BREAK_RUN", Subject: "f:INV-1", Kind: "no_tool", Reason: "no mutation tool for this language"}
	with := func(w confirm.WaiverEntry, fn func(*confirm.WaiverEntry)) Request { fn(&w); return waiver(w) }

	cases := []tc{
		{"unknown action", env, Request{Action: "nope"}, "UNKNOWN_ACTION"},
		{"BRD not found", env, approveReq("brd.nope", 1), "BRD_NOT_FOUND"},
		{"version required", env, approveReq("brd.x", 0), "VERSION_REQUIRED"},
		{"version mismatch", env, approveReq("brd.x", 7), "VERSION_MISMATCH"},
		{"superseded", superseded.env(""), approveReq("brd.x", 1), "BRD_SUPERSEDED"},
		{"regulated, machine written", machineFix.env(""), approveReq("brd.x", 1), "REGULATED_MACHINE_WRITTEN"},
		{"agent preparer", env, approveReq("brd.x", 1), ""},
		{"return needs a reason", env, Request{Action: confirm.ActionReturn, ID: "brd.x"}, "REASON_REQUIRED"},
		{"return names a stranger row", env, Request{Action: confirm.ActionReturn, ID: "brd.x", Reason: "r", Refs: []string{"f:INV-3"}}, "PROMISE_NOT_FOUND"},
		{"overturn needs a reason", env, Request{Action: confirm.ActionOverturn, ID: "brd.x"}, "REASON_REQUIRED"},
		{"ack without a council approval", env, Request{Action: confirm.ActionAck, ID: "brd.x"}, "NO_COUNCIL_APPROVAL"},
		{"ack after the version moved", moved.env(""), Request{Action: confirm.ActionAck, ID: "brd.x"}, "VERSION_MOVED"},
		{"ack with incomplete votes", incomplete.env(""), Request{Action: confirm.ActionAck, ID: "brd.x"}, "VOTES_INCOMPLETE"},
		{"ack by a seat's actor", council.env("agent:a"), Request{Action: confirm.ActionAck, ID: "brd.x"}, "AGENT_PREPARER"},
		{"ack by the author", council.env("author@example.com"), Request{Action: confirm.ActionAck, ID: "brd.x"}, "COUNCIL_TOUCHED"},
		{"ack by a seat's actor, not an agent", humanSeats.env("seat@example.com"), Request{Action: confirm.ActionAck, ID: "brd.x"}, "COUNCIL_TOUCHED"},
		{"ack by a seat's git identity", humanSeats.env(""), Request{Action: confirm.ActionAck, ID: "brd.x"}, "COUNCIL_TOUCHED"},
		{"promise confirm, no refs", env, Request{Action: confirm.ActionPromiseConfirm}, "REFS_REQUIRED"},
		{"promise confirm, unknown promise", env, Request{Action: confirm.ActionPromiseConfirm, Refs: []string{"f:INV-9"}}, "PROMISE_NOT_FOUND"},
		{"promise confirm across BRDs", env, Request{Action: confirm.ActionPromiseConfirm, Refs: []string{"f:INV-1", "f:INV-3"}}, "BATCH_MULTI_BRD"},
		{"promise confirm, no enforcer", env, Request{Action: confirm.ActionPromiseConfirm, Refs: []string{"f:INV-1", "f:INV-2"}}, "NO_ENFORCER"},
		{"waiver, no entry", env, Request{Action: confirm.ActionWaiverAdd}, "WAIVER_REQUIRED"},
		{"waiver, unknown kind", env, with(okW, func(w *confirm.WaiverEntry) { w.Kind = "because" }), "UNKNOWN_WAIVER_KIND"},
		{"waiver, no reason", env, with(okW, func(w *confirm.WaiverEntry) { w.Reason = "" }), "REASON_REQUIRED"},
		{"waiver, ADR required", env, with(okW, func(w *confirm.WaiverEntry) { w.Kind = "out_of_reach" }), "ADR_REQUIRED"},
		{"waiver, ADR missing", env, with(okW, func(w *confirm.WaiverEntry) { w.Kind = "out_of_reach"; w.ADR = "ADR-0042" }), "ADR_NOT_FOUND"},
		{"waiver, tier 3", env, with(okW, func(w *confirm.WaiverEntry) { w.Subject = "f:INV-4" }), "WAIVER_TIER3"},
		{"waiver, no such subject", env, with(okW, func(w *confirm.WaiverEntry) { w.Subject = "f:INV-99" }), "SUBJECT_NOT_FOUND"},
		{"dirty lattice", dirty.env(""), approveReq("brd.x", 1), "DIRTY_LATTICE"},
	}
	// Tier-3 BRD and council: a council never carries a tier-3 criterion.
	cases = append(cases, tc{"ack of a tier-3 BRD", tier3.env(""), Request{Action: confirm.ActionAck, ID: "brd.x"}, "COUNCIL_HUMAN_ONLY"})

	for _, c := range cases {
		if c.name == "agent preparer" {
			c.env.Actor = "agent:impl-1"
			c.want = "AGENT_PREPARER"
		}
		res, ref := Run(c.env, c.req)
		if ref == nil {
			t.Errorf("%s: got a branch %v, want refusal %s", c.name, res, c.want)
			continue
		}
		if ref.Refused != c.want {
			t.Errorf("%s: refused %s (%s), want %s", c.name, ref.Refused, ref.Text, c.want)
		}
		if ref.Contract != Schema || ref.Text == "" || ref.Commit == "" {
			t.Errorf("%s: incomplete refusal %+v", c.name, ref)
		}
	}
	// A refusal writes nothing.
	for _, fx := range []*fixture{f, machineFix, superseded, council} {
		if out := fx.git("for-each-ref", "refs/heads/decision"); out != "" {
			t.Errorf("a refusal left a branch: %s", out)
		}
	}
	if brd.MaxTier(t3) != 3 {
		t.Fatal("fixture: tier 3")
	}
}

// A human's ack and a human waiver both reach a branch; the waiver goes to
// the protected waivers file and the list only grows by reviewed additions.
func TestPrepareAckWaiverAndConfirm(t *testing.T) {
	f, _ := councilFixture(t, []schema.BRDCouncilVote{vote("intent", "agent:a", "approve"), vote("risk", "agent:b", "approve")})
	res, ref := Run(f.env(""), Request{Action: confirm.ActionAck, ID: "brd.x"})
	if ref != nil {
		t.Fatalf("ack refused: %+v", ref)
	}
	if !strings.HasPrefix(res.Branch, "decision/brd.x-v1.ack-") || len(res.Files) != 1 || res.Files[0] != "lattice/decisions/approvals/"+strings.TrimPrefix(res.Branch, "decision/")+".yaml" {
		t.Errorf("ack branch %s, files %v", res.Branch, res.Files)
	}
	d, _ := confirm.ParseDecision([]byte(f.git("show", res.Branch+":"+res.Files[0])))
	if d.VotesHash == "" || d.Action != confirm.ActionAck {
		t.Errorf("ack decision %+v", d)
	}

	// Promise confirm of enforced promises in one BRD.
	pc, ref := Run(f.env(""), Request{Action: confirm.ActionPromiseConfirm, Refs: []string{"f:INV-1"}})
	if ref != nil {
		t.Fatalf("promise confirm refused: %+v", ref)
	}
	pd, _ := confirm.ParseDecision([]byte(f.git("show", pc.Branch+":"+pc.Files[0])))
	if pd.ID != "brd.x" || len(pd.Rows) != 1 || pd.Rows[0].Promise != "f:INV-1" || !strings.Contains(pc.Branch, ".confirm-") {
		t.Errorf("promise decision %+v on %s", pd, pc.Branch)
	}

	// A waiver writes lattice/waivers.yaml next to its decision file.
	w := confirm.WaiverEntry{Code: "NO_BREAK_RUN", Subject: "f:INV-1", Kind: "no_tool", Reason: "no mutation tool for this language"}
	wr, ref := Run(f.env(""), Request{Action: confirm.ActionWaiverAdd, Waiver: &w})
	if ref != nil {
		t.Fatalf("waiver refused: %+v", ref)
	}
	if len(wr.Files) != 2 || !contains(wr.Files, "lattice/waivers.yaml") {
		t.Errorf("waiver files %v", wr.Files)
	}
	var list confirm.Waivers
	if err := yaml.Unmarshal([]byte(f.git("show", wr.Branch+":lattice/waivers.yaml")), &list); err != nil || len(list.Waivers) != 1 || list.Waivers[0].Kind != "no_tool" {
		t.Errorf("waivers.yaml %+v %v", list, err)
	}

	// A duplicate waiver is refused once the first one is on HEAD.
	f.git("checkout", "-q", wr.Branch)
	if _, ref := Run(f.env(""), Request{Action: confirm.ActionWaiverAdd, Waiver: &w}); ref == nil || ref.Refused != "WAIVER_DUPLICATE" {
		t.Errorf("duplicate: %+v", ref)
	}
}

// The shape of lattice.prepare/1 is frozen by golden files.
func TestPrepareContractGolden(t *testing.T) {
	f := newFixture(t)
	res, ref := Run(f.env(""), approveReq("brd.x", 1))
	if ref != nil {
		t.Fatal(ref)
	}
	res.Commit = "<head>"
	golden(t, "testdata/prepare.golden.json", res)

	machine := *f.brd("brd.y")
	machine.Provenance = schema.BRDProvenance{AuthoredBy: "agent:impl-2"}
	machine.Constraints = []schema.BRDConstraint{{Kind: schema.ConstraintLegal, Ref: "C-1"}}
	f.setBRD(machine)
	_, ref = Run(f.env(""), approveReq("brd.y", 1))
	if ref == nil {
		t.Fatal("expected a refusal")
	}
	ref.Commit = "<head>"
	golden(t, "testdata/refusal.golden.json", ref)
}

func golden(t *testing.T, rel string, v interface{}) {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(rel, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("%v (run with UPDATE_GOLDEN=1)", err)
	}
	if string(want) != buf.String() {
		t.Errorf("%s changed; the contract is frozen. got:\n%s", rel, buf.String())
	}
}

// A decision's name comes from its packet. Preparing the same packet twice
// reports the branch it already wrote; once that decision is on HEAD it is
// never written again; and after the promise changes, re-confirming writes a
// second branch with a new file, leaving the first decision file as it was.
// A branch of the same name that holds something else is refused.
// @verifies confirm:INV-7
func TestPrepareNeverOverwritesADecision(t *testing.T) {
	f := newFixture(t)
	// A decision file with a name from before names carried the packet.
	legacy := "lattice/decisions/approvals/brd.x-v1.confirm-legacy.yaml"
	f.write(legacy, "action: promise.confirm\nid: brd.x\nversion: 1\npacket_hash: sha256:00\nrows:\n  - promise: f:INV-1\nprepared_by: Dev <dev@example.com>\nprepared_at: \"2025-12-01T00:00:00Z\"\n")
	f.commit("an earlier confirmation")
	req := Request{Action: confirm.ActionPromiseConfirm, Refs: []string{"f:INV-1"}}

	first, ref := Run(f.env(""), req)
	if ref != nil {
		t.Fatalf("first prepare refused: %+v", ref)
	}
	if !strings.HasSuffix(first.Branch, "-"+strings.TrimPrefix(first.PacketHash, "sha256:")[:8]) || len(first.Files) != 1 {
		t.Fatalf("the name must carry the packet: %+v", first)
	}
	tip := f.git("rev-parse", first.Branch)
	if f.git("show", first.Branch+":"+legacy) != f.git("show", "HEAD:"+legacy) {
		t.Error("the earlier decision file changed on the new branch")
	}

	// The same packet again: the same branch, untouched, and no error.
	again, ref := Run(f.env(""), req)
	if ref != nil || again.Branch != first.Branch || again.Files[0] != first.Files[0] || !strings.HasPrefix(again.Note, "Already prepared") {
		t.Fatalf("a second prepare of the same packet must be idempotent: %+v %+v", again, ref)
	}
	if f.git("rev-parse", first.Branch) != tip {
		t.Error("the second prepare moved the branch")
	}

	// Merged, the branch deleted: the same packet is never written again.
	f.git("merge", "-q", "--ff-only", first.Branch)
	f.git("branch", "-D", first.Branch)
	recorded := f.git("show", "HEAD:"+first.Files[0])
	if _, ref := Run(f.env(""), req); ref == nil || ref.Refused != "DECISION_EXISTS" {
		t.Fatalf("a decision on HEAD must not be prepared again: %+v", ref)
	}

	// The promise is edited, as CONFIRMATION_VOIDED says, and confirmed again.
	f.kg.Features[0].Invariants[0].Statement = "one, edited"
	second, ref := Run(f.env(""), req)
	if ref != nil {
		t.Fatalf("re-confirm refused: %+v", ref)
	}
	if second.Branch == first.Branch || second.Files[0] == first.Files[0] || second.PacketHash == first.PacketHash {
		t.Fatalf("a new packet needs a new branch and file: %+v vs %+v", second, first)
	}
	if got := f.git("show", second.Branch+":"+first.Files[0]); got != recorded {
		t.Errorf("the first decision file changed on the second branch:\n%s\nwant\n%s", got, recorded)
	}
	if got := f.git("diff", "--name-only", "HEAD", second.Branch); got != second.Files[0] {
		t.Errorf("the second branch must only add its own file: %q", got)
	}

	// A branch of the same name that holds another decision is refused.
	f.git("branch", "-f", second.Branch, "HEAD")
	if _, ref := Run(f.env(""), req); ref == nil || ref.Refused != "BRANCH_EXISTS" {
		t.Errorf("a same-named branch without this decision: %+v", ref)
	}
}
