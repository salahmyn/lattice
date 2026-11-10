package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/brd"
	"github.com/salahmyn/lattice/pkg/lattice/confirm"
	"github.com/salahmyn/lattice/pkg/lattice/contract"
	"github.com/salahmyn/lattice/pkg/lattice/results"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// head is the commit the fixture's evidence is stamped with.
const fixtureHead = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"

// v2Fixture is a small invented bookshop: three features, four BRDs in four
// states, a council review, a waiver, a second reader's note, and evidence
// stamped by CI run 4812. It is not a git repository.
type v2Fixture struct {
	root string
	dir  string // lattice/
	kg   schema.KnowledgeGraph
	srv  *Server
}

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newV2Fixture(t *testing.T) *v2Fixture {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "lattice")
	cache := filepath.Join(dir, ".cache")

	// CI run 4812: one test result, one break that survived.
	set, err := results.ParseJUnit([]byte(`<testsuite name="s">
	  <testcase classname="refunds" name="TestCap"/>
	  <testcase classname="checkout" name="TestPaid"/>
	  <testcase classname="ledger" name="TestPost"/></testsuite>`))
	if err != nil {
		t.Fatal(err)
	}
	set.Commit, set.CIRun = fixtureHead, "4812"
	data, _ := json.Marshal(set)
	writeFile(t, filepath.Join(cache, "results", "results.json"), string(data))
	writeFile(t, filepath.Join(cache, "results", "breaks.json"), `{"schema":"lattice.breaks/1","commit":"`+fixtureHead+`","ci_run":"4812","runs":[
	  {"promise":"checkout:INV-1","enforcer":"checkout.Pay","method":"early_return","baseline":"pass","outcome":"survived","verifiers":["checkout.TestPaid"]},
	  {"promise":"ledger:INV-1","enforcer":"ledger.Post","method":"early_return","baseline":"pass","outcome":"survived","verifiers":["ledger.TestPost"],"not_run_verifiers":["ledger.TestPost"]}]}`)
	writeFile(t, filepath.Join(dir, "audit-notes.yaml"), "- {promise: 'checkout:INV-1', reader: ana, verdict: confirmed_fake, note: the test never asserts}\n")
	writeFile(t, filepath.Join(dir, "waivers.yaml"), "waivers:\n  - {code: NO_LIVE, subject: 'refunds:INV-2', kind: out_of_reach, reason: the provider job runs outside this repo}\n")

	// What the platform recorded: refunds was confirmed at v2, legacy at v2.
	if err := confirm.SaveCache(dir, confirm.File{Contract: confirm.Schema, Commit: fixtureHead, GeneratedAt: "2026-11-01T00:00:00Z",
		Assurance: confirm.Assurance{State: "pass", Honesty: "fact", Code: "PLATFORM_READ"},
		Records: []confirm.Record{
			{ID: "brd.refunds", Version: 2, Action: "brd.approve", State: "confirmed", ApproverLogin: "owner", PR: 33, MergedSHA: "deadbeef"},
			{ID: "brd.legacy", Version: 2, Action: "brd.approve", State: "confirmed", ApproverLogin: "owner", PR: 12, MergedSHA: "feedface"},
		}}); err != nil {
		t.Fatal(err)
	}

	// A council reviewed brd.checkout v4: 2 of 2 seats approved.
	votes, _ := yaml.Marshal(brd.CouncilRecord{BRD: "brd.checkout", Version: 4, Votes: []schema.BRDCouncilVote{
		{Seat: "intent", Actor: "agent:intent", Verdict: "approve", Reason: "the intent is clear", At: "2026-10-30T09:00:00Z"},
		{Seat: "risk", Actor: "agent:risk", Verdict: "approve", Reason: "no tier-3 criterion", At: "2026-10-30T10:00:00Z"}}})
	writeFile(t, brd.VotesPath(dir, "brd.checkout", 4), string(votes))

	kg := schema.KnowledgeGraph{
		GeneratedFromCommit: fixtureHead,
		Features: []schema.Manifest{
			{ID: "refunds", Version: 1, Status: schema.StatusProduction, Purpose: "Gives a reader's money back.", ImplementsBRD: "brd.refunds",
				Owners: schema.Owners{Business: "commerce", Engineering: "team-payments"}, DependsOn: []string{"checkout"},
				Invariants: []schema.Invariant{{ID: "INV-1", Statement: "A refund never exceeds what was paid."}, {ID: "INV-2", Statement: "A refund reaches the provider within a day."}}},
			{ID: "checkout", Version: 1, Status: schema.StatusProduction, Purpose: "Turns a basket into a paid order.", ImplementsBRD: "brd.checkout",
				Owners:     schema.Owners{Business: "commerce", Engineering: "team-checkout"},
				Invariants: []schema.Invariant{{ID: "INV-1", Statement: "Only a paid basket becomes an order."}}},
			{ID: "ledger", Version: 1, Status: schema.StatusProduction, Purpose: "Keeps the books.", ImplementsBRD: "brd.legacy",
				Owners:     schema.Owners{Business: "finance", Engineering: "team-ledger"},
				Invariants: []schema.Invariant{{ID: "INV-1", Statement: "A posting is balanced."}}},
			{ID: "reports", Version: 1, Status: schema.StatusProduction, Purpose: "Monthly reports.", Owners: schema.Owners{Business: "finance", Engineering: "team-ledger"}},
		},
		BRDs: []schema.BRD{
			{ID: "brd.refunds", Version: 3, Status: schema.BRDApproved, Title: "Safe refunds", BusinessProblem: "A reader is charged for a book they sent back.",
				BusinessGoals: []string{"refund within a day"},
				UserScenarios: []schema.UserScenario{{ID: "US-1", Actor: "reader", Narrative: "I return a book and get my money back."}},
				SuccessCriteria: []schema.BRDCriterion{
					{ID: "SC-1", Statement: "A refund never exceeds the charge.", MapsToInvariant: schema.InvariantRefs{"refunds:INV-1"}, Tier: 2},
					{ID: "SC-2", Statement: "A refund reaches the provider within a day.", MapsToInvariant: schema.InvariantRefs{"refunds:INV-2"}, Tier: 3}},
				Constraints:   []schema.BRDConstraint{{Kind: schema.ConstraintLegal, Ref: "C-1", Note: "Refunds follow the published returns policy."}},
				ImplementsVia: []string{"refunds"},
				Approval:      &schema.BRDApproval{ApprovedBy: "owner", ApprovedAt: "2026-10-28T00:00:00Z", ApprovedVersion: 2},
				Provenance:    schema.BRDProvenance{Source: schema.BRDSourceHuman, AuthoredBy: "agent:impl-2"}},
			{ID: "brd.checkout", Version: 4, Status: schema.BRDApproved, Title: "Paid orders", BusinessProblem: "Unpaid baskets became orders.",
				SuccessCriteria: []schema.BRDCriterion{{ID: "SC-1", Statement: "Only a paid basket becomes an order.", MapsToInvariant: schema.InvariantRefs{"checkout:INV-1"}}},
				ImplementsVia:   []string{"checkout"},
				Approval: &schema.BRDApproval{ApprovedBy: "council:orders", ApprovedAt: "2026-10-30T11:00:00Z", ApprovedVersion: 4, Mandate: "orders",
					DelegatedBy: "owner", HumanReview: schema.HumanReviewPending,
					Decision: &schema.BRDCouncilDecision{Outcome: "approved", Summary: "Both seats approve."}}},
			{ID: "brd.catalogue", Version: 1, Status: schema.BRDDraft, Title: "Catalogue", BusinessProblem: "Readers cannot find books.",
				Provenance: schema.BRDProvenance{Source: schema.BRDSourceLLMFromCode, GeneratedAt: "2026-10-01T00:00:00Z"}},
			{ID: "brd.legacy", Version: 2, Status: schema.BRDApproved, Title: "Legacy ledger", BusinessProblem: "The books must balance.",
				SuccessCriteria: []schema.BRDCriterion{{ID: "SC-1", Statement: "A posting is balanced.", MapsToInvariant: schema.InvariantRefs{"ledger:INV-1"}, Tier: 3}},
				ImplementsVia:   []string{"ledger"},
				Approval:        &schema.BRDApproval{ApprovedBy: "owner", ApprovedAt: "2026-09-01T00:00:00Z", ApprovedVersion: 2}},
		},
		Symbols: []schema.GraphSymbol{
			{FQN: "refunds.Cap", Feature: "refunds", EnforcesInvariants: []string{"INV-1"}, File: "refunds/cap.go", Line: 12, Reach: schema.ReachEntry},
			{FQN: "ledger.Post", Feature: "ledger", EnforcesInvariants: []string{"INV-1"}, File: "ledger/post.go", Line: 5},
			{FQN: "checkout.Pay", Feature: "checkout", EnforcesInvariants: []string{"INV-1"}, File: "checkout/pay.go", Line: 8, Reach: schema.ReachEntry},
		},
		Tests: []schema.GraphSymbol{
			{FQN: "refunds.TestCap", Feature: "refunds", IsTest: true, Verifies: []string{"INV-1"}},
			{FQN: "checkout.TestPaid", Feature: "checkout", IsTest: true, Verifies: []string{"INV-1"}},
			{FQN: "ledger.TestPost", Feature: "ledger", IsTest: true, Verifies: []string{"INV-1"}},
		},
		EntryPoints: []schema.EntryPoint{{ID: "ep.post_refunds", Kind: schema.EntryPointKindHTTP,
			Trigger: schema.Trigger{Method: "POST", Path: "/refunds"}, Flow: []schema.FlowStep{{Feature: "checkout"}, {Feature: "refunds"}}}},
	}
	f := &v2Fixture{root: root, dir: dir, kg: kg}
	f.srv = New(&workspace.Workspace{LatticeDir: dir}, Options{
		GraphBuilder: func(context.Context) (schema.KnowledgeGraph, error) { return f.kg, nil },
	})
	return f
}

// get calls the handler as a client on this machine.
func (f *v2Fixture) get(t *testing.T, path string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = "127.0.0.1:41000"
	req.Host = "127.0.0.1:7070"
	w := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(w, req)
	return w.Code, w.Body.Bytes()
}

var v2Endpoints = []struct{ name, path string }{
	{"meta", "/api/v2/meta"},
	{"queue", "/api/v2/queue"},
	{"brds", "/api/v2/brds"},
	{"brd-packet", "/api/v2/brds/brd.refunds?version=3"},
	{"brd-council", "/api/v2/brds/brd.checkout?version=4"},
	{"feature", "/api/v2/features/refunds"},
	{"feature-no-promise", "/api/v2/features/reports"},
	{"health", "/api/v2/health"},
}

var tokenRe = regexp.MustCompile(`"csrf_token": "[0-9a-f]+"`)

// Each contract is frozen by a golden file. Run with UPDATE_GOLDEN=1 to
// rewrite them after a deliberate change.
// @verifies api:INV-1
func TestAPIV2Golden(t *testing.T) {
	f := newV2Fixture(t)
	for _, ep := range v2Endpoints {
		t.Run(ep.name, func(t *testing.T) {
			code, body := f.get(t, ep.path)
			if code != 200 {
				t.Fatalf("%s = %d\n%s", ep.path, code, body)
			}
			got := tokenRe.ReplaceAll(body, []byte(`"csrf_token": "<token>"`))
			golden := filepath.Join("testdata", "apiv2", ep.name+".golden.json")
			if os.Getenv("UPDATE_GOLDEN") != "" {
				writeFile(t, golden, string(got))
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("no golden file (UPDATE_GOLDEN=1 writes it): %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s differs from %s; run UPDATE_GOLDEN=1 go test ./pkg/lattice/ui -run APIV2Golden to see the diff\n%s", ep.path, golden, got)
			}
		})
	}
}

// Every payload carries its contract and the commit it describes, and every
// assurance value has the shape {state, honesty, ...}.
// @verifies api:INV-1
func TestAPIV2EnvelopeOnEveryPayload(t *testing.T) {
	f := newV2Fixture(t)
	want := map[string]string{"/api/v2/meta": contract.MetaV1, "/api/v2/queue": contract.QueueV1, "/api/v2/brds": contract.BRDV1,
		"/api/v2/brds/brd.refunds": contract.BRDV1, "/api/v2/features/refunds": contract.BRDV1, "/api/v2/health": contract.HealthV1}
	for path, tag := range want {
		_, body := f.get(t, path)
		var h contract.Header
		if err := json.Unmarshal(body, &h); err != nil || h.Contract != tag || h.Commit != fixtureHead || h.CIRun != "4812" || !h.EvidenceTied {
			t.Errorf("%s header = %+v (%v), want %s @ %s, CI run 4812, tied", path, h, err, tag, fixtureHead)
		}
	}
}

// No endpoint returns a pass for something UNPROVEN or unknown: every
// payload passes the lint, a scope verdict is never ASSURED, an unmeasured
// signal is unknown, and a field with no kernel source is NOT_COMPUTED.
// @verifies api:INV-2
func TestAPIV2NeverPassesUnknown(t *testing.T) {
	f := newV2Fixture(t)
	for _, ep := range v2Endpoints {
		_, body := f.get(t, ep.path)
		bad, err := contract.Lint(body, contract.LintOptions{})
		if err != nil || len(bad) > 0 {
			t.Errorf("%s breaks the floor: %v %v", ep.path, err, bad)
		}
	}

	var page contract.FeaturePage
	_, body := f.get(t, "/api/v2/features/refunds")
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	var p1 *contract.Promise
	for i := range page.Promises {
		if page.Promises[i].ID == "refunds:INV-1" {
			p1 = &page.Promises[i]
		}
	}
	if p1 == nil {
		t.Fatalf("no refunds:INV-1 in %+v", page.Promises)
	}
	sig := p1.Signals
	if sig.BreakTested.State != contract.StateUnknown || sig.Exercised.State != contract.StateUnknown {
		t.Errorf("no break run and no per-test coverage must read unknown: break %+v exercised %+v", sig.BreakTested, sig.Exercised)
	}
	if sig.Confirmed.State != contract.StateDrifted {
		t.Errorf("confirmed at v2, BRD at v3: want drifted, got %+v", sig.Confirmed)
	}
	if sig.NothingFell.State != contract.StateUnknown || sig.NothingFell.Code != contract.CodeNotComputed {
		t.Errorf("nothing_fell needs a base: %+v", sig.NothingFell)
	}
	if p1.Verdict.State != "UNPROVEN" || page.Verdict.State == "ASSURED" {
		t.Errorf("verdicts: promise %+v feature %+v", p1.Verdict, page.Verdict)
	}

	// The lint itself: each way to show missing evidence as a pass is caught.
	for name, bad := range map[string]string{
		"pass with a code":       `{"state":"pass","honesty":"fact","code":"NO_BREAK_RUN"}`,
		"unknown with no code":   `{"state":"unknown","honesty":"fact"}`,
		"not computed as pass":   `{"state":"pass","honesty":"fact","code":"NOT_COMPUTED"}`,
		"honesty missing":        `{"state":"pass","honesty":"hunch"}`,
		"a state the list lacks": `{"state":"green","honesty":"fact"}`,
		"ASSURED without a base": `{"verdict":{"state":"ASSURED","code":"ALL_SIGNALS","text":"x"}}`,
		"nested in an array":     `{"promises":[{"signals":{"live":{"state":"unknown","honesty":"upper_bound"}}}]}`,
	} {
		if got, _ := contract.Lint([]byte(bad), contract.LintOptions{}); len(got) == 0 {
			t.Errorf("lint missed: %s", name)
		}
	}
	if got, _ := contract.Lint([]byte(`{"state":"pass","honesty":"fact","code":"UNTRACED_LINES"}`), contract.LintOptions{}); len(got) != 0 {
		t.Errorf("an empty blind spot is a pass with a descriptive code: %v", got)
	}
	if got, _ := contract.Lint([]byte(`{"verdict":{"state":"ASSURED","code":"ALL_SIGNALS","text":"x"}}`), contract.LintOptions{AllowAssured: true}); len(got) != 0 {
		t.Errorf("a change report may read ASSURED: %v", got)
	}
}

// Fields the kernel has no source for are NOT_COMPUTED, never invented.
// @verifies api:INV-1
func TestAPIV2NotComputedFields(t *testing.T) {
	f := newV2Fixture(t)
	_, body := f.get(t, "/api/v2/brds/brd.refunds?version=3")
	var page contract.BRDPage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	for name, a := range map[string]contract.Assurance{
		"intent.story": page.Intent.Story, "sources": page.Sources, "blind_spot": page.BlindSpot,
		"packet.blind_spot": page.Packet.BlindSpot, "packet.diff.text_only": page.Packet.Diff.TextOnly,
		"promises[0].cases": page.Promises[0].Cases,
	} {
		if a.State != contract.StateUnknown || a.Code != contract.CodeNotComputed {
			t.Errorf("%s = %+v, want unknown NOT_COMPUTED", name, a)
		}
	}
	_, body = f.get(t, "/api/v2/queue")
	var q contract.Queue
	if err := json.Unmarshal(body, &q); err != nil || q.Proposals.Code != contract.CodeNotComputed {
		t.Errorf("proposals = %+v (%v)", q.Proposals, err)
	}
}

// The BRD page reads the plan's state machine from the BRD's record and
// confirm.json, and a typed approval is never CONFIRMED.
// @verifies api:INV-1
func TestAPIV2BRDStates(t *testing.T) {
	f := newV2Fixture(t)
	_, body := f.get(t, "/api/v2/brds")
	var list contract.BRDList
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	got := map[string]contract.BRDSummary{}
	for _, b := range list.BRDs {
		got[b.ID] = b
	}
	for id, want := range map[string]string{"brd.refunds": "DRIFTED", "brd.checkout": "PROPOSED", "brd.catalogue": "DRAFT", "brd.legacy": "CONFIRMED"} {
		if got[id].State != want {
			t.Errorf("%s = %s, want %s", id, got[id].State, want)
		}
	}
	if c := got["brd.checkout"].Chips; len(c) != 1 || c[0].Code != "REVIEWED_BY_COUNCIL" || c[0].Params["approve"] != float64(2) {
		t.Errorf("the council-reviewed BRD stays PROPOSED with a sub-chip: %+v", c)
	}

	// A review packet: the diff against v2 cannot be read without git, so the
	// hash says so rather than guess.
	_, body = f.get(t, "/api/v2/brds/brd.refunds?version=3")
	var page contract.BRDPage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	pk := page.Packet
	if pk == nil || pk.Version != 3 || pk.Diff.NeverConfirmed || pk.PacketHash != "" || pk.HashState.Code != "DIFF_BASE_UNREADABLE" {
		t.Errorf("packet = %+v", pk)
	}
	if page.Confirmation.ConfirmedVersion != 2 || page.Confirmation.Via != "PR #33" || page.Confirmation.ConfirmedBy != "owner" {
		t.Errorf("confirmation = %+v", page.Confirmation)
	}
	if len(page.Constraints) != 1 || !page.Constraints[0].Locked {
		t.Errorf("a legal constraint is locked: %+v", page.Constraints)
	}
	if page.Counts.Promises != 2 || page.Counts.OutOfReach != 1 || page.Counts.Unproven != 2 {
		t.Errorf("counts = %+v", page.Counts)
	}
	// a version that is not the current one cannot be opened as a packet
	if code, _ := f.get(t, "/api/v2/brds/brd.refunds?version=2"); code != 404 {
		t.Errorf("version 2 = %d, want 404", code)
	}

	// brd.catalogue has never been confirmed: its packet says so, with the
	// hash `lattice prepare` would pin.
	_, body = f.get(t, "/api/v2/brds/brd.catalogue?version=1")
	var cat contract.BRDPage
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatal(err)
	}
	if cat.Packet == nil || !cat.Packet.Diff.NeverConfirmed || len(cat.Packet.Diff.Structural) != 1 || cat.Packet.Diff.Structural[0] != "initial" ||
		!strings.HasPrefix(cat.Packet.PacketHash, "sha256:") {
		t.Errorf("catalogue packet = %+v", cat.Packet)
	}
}

// The queue ranks by tier, then blast radius, then age, and says why.
// @verifies api:INV-1
func TestAPIV2QueueRank(t *testing.T) {
	f := newV2Fixture(t)
	_, body := f.get(t, "/api/v2/queue")
	var q contract.Queue
	if err := json.Unmarshal(body, &q); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i, it := range q.Items {
		ids = append(ids, it.ID)
		if it.Rank != i+1 || it.Reason.Code == "" || it.Reason.Text == "" || it.Link == "" {
			t.Errorf("item %d lacks a rank, reason or link: %+v", i, it)
		}
		if i > 0 && it.Tier > q.Items[i-1].Tier {
			t.Errorf("tier must not rise down the queue: %s after %s", it.ID, q.Items[i-1].ID)
		}
	}
	for _, want := range []string{"brd_drifted:brd.refunds@3", "brd_awaiting:brd.checkout@4", "brd_awaiting:brd.catalogue@1", "confirmed_fake:checkout:INV-1", "promise_unknown_t3:ledger:INV-1"} {
		found := false
		for _, id := range ids {
			found = found || id == want
		}
		if !found {
			t.Errorf("queue lacks %s: %v", want, ids)
		}
	}
	for _, id := range ids {
		if id == "brd_awaiting:brd.legacy@2" || strings.Contains(id, "brd.legacy") {
			t.Errorf("a confirmed BRD needs no human: %v", ids)
		}
	}
	if q.Items[0].ID != "brd_drifted:brd.refunds@3" {
		t.Errorf("the tier-3 BRD ranks first: %v", ids)
	}
}

// The evidence matrix: a cell is present, partial, absent or stale, with its
// commit and whether CI stamped it; confirm.json has no ci_run, so that is null.
// @verifies api:INV-1
func TestAPIV2Health(t *testing.T) {
	f := newV2Fixture(t)
	_, body := f.get(t, "/api/v2/health")
	var h contract.Health
	if err := json.Unmarshal(body, &h); err != nil {
		t.Fatal(err)
	}
	rows := map[string]contract.HealthRow{}
	for _, r := range h.Rows {
		rows[r.Feature] = r
	}
	r := rows["refunds"]
	if r.Results.State != contract.StatePresent || r.Results.Commit != fixtureHead || r.Results.CIStamped == nil || !*r.Results.CIStamped {
		t.Errorf("results cell = %+v", r.Results)
	}
	if r.Coverage.State != contract.StateAbsent || r.Breaks.State != contract.StateAbsent {
		t.Errorf("no per-test coverage and no break run for refunds: %+v %+v", r.Coverage, r.Breaks)
	}
	if r.Confirm.CIStamped != nil {
		t.Errorf("confirm.json carries no ci_run, so ci_stamped is null: %+v", r.Confirm)
	}
	if l := rows["reports"]; l.Promises != 0 || l.Results.State != contract.StateAbsent {
		t.Errorf("reports declares no promise: %+v", l)
	}
	if b := rows["checkout"].Breaks; b.State != contract.StatePresent || b.Commit != fixtureHead {
		t.Errorf("checkout's break run: %+v", b)
	}
}

// meta says where the evidence came from, and issues the CSRF token only to a
// client on this machine.
// @verifies api:INV-3
func TestAPIV2Meta(t *testing.T) {
	f := newV2Fixture(t)
	_, body := f.get(t, "/api/v2/meta")
	var m contract.Meta
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if m.Evidence.Results.Tie.State != contract.StatePass || m.Evidence.Results.CIRun != "4812" || m.Evidence.Breaks.CIRun != "4812" {
		t.Errorf("results and breaks are tied to the head by CI run 4812: %+v", m.Evidence)
	}
	if m.Evidence.Coverage.Present || m.Evidence.Coverage.Tie.State != contract.StateUnknown {
		t.Errorf("no coverage file: %+v", m.Evidence.Coverage)
	}
	if m.Evidence.Confirm.Tie.Code != contract.CodeNotComputed || m.Evidence.AuditSnapshot.Tie.State != contract.StateUnknown {
		t.Errorf("confirm.json and the snapshot have no ci_run: %+v %+v", m.Evidence.Confirm, m.Evidence.AuditSnapshot)
	}
	if m.Dirty != nil {
		t.Errorf("not a git repository: dirty is null, got %v", *m.Dirty)
	}
	if !m.Prepare.Enabled || m.Prepare.CSRFToken == "" {
		t.Errorf("a loopback client is issued the token: %+v", m.Prepare)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/meta", nil)
	req.RemoteAddr, req.Host = "203.0.113.9:5000", "127.0.0.1:7070"
	w := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(w, req)
	if strings.Contains(w.Body.String(), "csrf_token") || !strings.Contains(w.Body.String(), `"enabled": false`) {
		t.Errorf("a remote client is issued no token: %s", w.Body.String())
	}
}

// --- prepare ---

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// prepareFixture is a git repository with one proposed BRD, served by a Server.
func prepareFixture(t *testing.T, actor string) (*Server, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	gitIn(t, root, "init", "-q", "-b", "main")
	gitIn(t, root, "config", "user.email", "dev@example.com")
	gitIn(t, root, "config", "user.name", "dev")
	writeFile(t, filepath.Join(root, "lattice", "config.yaml"), "agentic:\n  llm:\n    enabled: false\n")
	writeFile(t, filepath.Join(root, ".gitignore"), "lattice/.cache/\n")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-q", "-m", "base")
	head := gitIn(t, root, "rev-parse", "HEAD")

	kg := schema.KnowledgeGraph{GeneratedFromCommit: head,
		Features: []schema.Manifest{{ID: "shop", Version: 1, Status: schema.StatusProduction, ImplementsBRD: "brd.shop",
			Invariants: []schema.Invariant{{ID: "INV-1", Statement: "A price is positive."}}}},
		BRDs: []schema.BRD{{ID: "brd.shop", Version: 1, Status: schema.BRDProposed, Title: "Shop", BusinessProblem: "Sell books.",
			SuccessCriteria: []schema.BRDCriterion{{ID: "SC-1", Statement: "A price is positive.", MapsToInvariant: schema.InvariantRefs{"shop:INV-1"}}}}},
	}
	srv := New(&workspace.Workspace{LatticeDir: filepath.Join(root, "lattice")}, Options{
		Actor:        actor,
		GraphBuilder: func(context.Context) (schema.KnowledgeGraph, error) { return kg, nil },
	})
	return srv, root
}

func postPrepare(srv *Server, remote string, hdr map[string]string, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v2/prepare", strings.NewReader(body))
	req.RemoteAddr, req.Host = remote, "127.0.0.1:7070"
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func tokenOf(t *testing.T, srv *Server) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/meta", nil)
	req.RemoteAddr, req.Host = "127.0.0.1:5000", "127.0.0.1:7070"
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	var m contract.Meta
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || m.Prepare.CSRFToken == "" {
		t.Fatalf("no token: %v %s", err, w.Body.String())
	}
	return m.Prepare.CSRFToken
}

func refusedCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var r struct {
		Refused string `json:"refused"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &r)
	return r.Refused
}

const approveBody = `{"action":"brd.approve","id":"brd.shop","version":1}`

// prepare is refused from a non-loopback address, and without the CSRF token
// /api/v2/meta issued, and from another origin, and behind a proxy; and a
// refusal creates no branch.
// @verifies api:INV-3
func TestAPIV2PrepareRefusals(t *testing.T) {
	srv, root := prepareFixture(t, "")
	token := tokenOf(t, srv)
	ok := map[string]string{csrfHeader: token}

	cases := []struct {
		name   string
		remote string
		hdr    map[string]string
		body   string
		status int
		code   string
	}{
		{"remote address", "203.0.113.9:5000", ok, approveBody, 403, "NOT_LOOPBACK"},
		{"remote address with the token", "198.51.100.7:1", ok, approveBody, 403, "NOT_LOOPBACK"},
		{"behind a proxy", "127.0.0.1:5000", map[string]string{csrfHeader: token, "X-Forwarded-For": "203.0.113.9"}, approveBody, 403, "NOT_LOOPBACK"},
		{"no token", "127.0.0.1:5000", nil, approveBody, 403, "CSRF_REQUIRED"},
		{"a wrong token", "127.0.0.1:5000", map[string]string{csrfHeader: "0000"}, approveBody, 403, "CSRF_REQUIRED"},
		{"another origin", "127.0.0.1:5000", map[string]string{csrfHeader: token, "Origin": "https://evil.example"}, approveBody, 403, "CSRF_ORIGIN"},
		{"a cross-site fetch", "127.0.0.1:5000", map[string]string{csrfHeader: token, "Sec-Fetch-Site": "cross-site"}, approveBody, 403, "CSRF_ORIGIN"},
		{"not json", "127.0.0.1:5000", map[string]string{csrfHeader: token, "Content-Type": "text/plain"}, approveBody, 415, "BAD_REQUEST"},
		{"an unknown field", "127.0.0.1:5000", ok, `{"action":"brd.approve","confirmed":true}`, 400, "BAD_REQUEST"},
	}
	for _, c := range cases {
		w := postPrepare(srv, c.remote, c.hdr, c.body)
		if w.Code != c.status || refusedCode(t, w) != c.code {
			t.Errorf("%s: %d %s, want %d %s\n%s", c.name, w.Code, refusedCode(t, w), c.status, c.code, w.Body.String())
		}
	}
	if out := gitIn(t, root, "branch", "--list", "decision/*"); out != "" {
		t.Errorf("a refusal wrote a branch: %s", out)
	}
}

// With the token, from this machine, prepare writes one decision branch the
// CLI would, returns lattice.prepare/1 and confirms nothing; the kernel's own
// refusals come back whole; a stale packet is refused; an agent is refused.
// @verifies api:INV-3
func TestAPIV2PrepareWritesABranchAndConfirmsNothing(t *testing.T) {
	srv, root := prepareFixture(t, "")
	hdr := map[string]string{csrfHeader: tokenOf(t, srv)}

	// The review packet of the version, then a decision pinned to a hash that moved.
	w := postPrepare(srv, "127.0.0.1:5000", hdr, `{"action":"brd.approve","id":"brd.shop","version":1,"packet_hash":"sha256:stale"}`)
	if w.Code != 422 || refusedCode(t, w) != "PACKET_STALE" {
		t.Fatalf("a stale packet: %d %s", w.Code, w.Body.String())
	}
	if w := postPrepare(srv, "127.0.0.1:5000", hdr, `{"action":"brd.approve","id":"brd.shop"}`); refusedCode(t, w) != "VERSION_REQUIRED" {
		t.Errorf("the kernel's refusal is returned as it is: %s", w.Body.String())
	}

	// Read the packet hash the page shows, and prepare against it.
	req := httptest.NewRequest(http.MethodGet, "/api/v2/brds/brd.shop?version=1", nil)
	req.RemoteAddr, req.Host = "127.0.0.1:5000", "127.0.0.1:7070"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var page contract.BRDPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || page.Packet == nil || page.Packet.PacketHash == "" {
		t.Fatalf("packet: %v %s", err, rec.Body.String())
	}

	w = postPrepare(srv, "127.0.0.1:5000", hdr, `{"action":"brd.approve","id":"brd.shop","version":1,"packet_hash":"`+page.Packet.PacketHash+`"}`)
	if w.Code != 200 {
		t.Fatalf("prepare = %d %s", w.Code, w.Body.String())
	}
	var res struct {
		Contract   string   `json:"contract"`
		Branch     string   `json:"branch"`
		PacketHash string   `json:"packet_hash"`
		Command    string   `json:"command"`
		Files      []string `json:"files"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.Contract != "lattice.prepare/1" || res.Branch != "decision/brd.shop-v1" || res.PacketHash != page.Packet.PacketHash ||
		!strings.Contains(res.Command, "gh pr create") || len(res.Files) != 1 {
		t.Errorf("result = %+v", res)
	}
	// The hash the page showed is the hash the decision pinned: one computation.
	if show := gitIn(t, root, "show", "decision/brd.shop-v1:lattice/decisions/approvals/brd.shop-v1.yaml"); !strings.Contains(show, page.Packet.PacketHash) {
		t.Errorf("the decision file does not pin the packet hash:\n%s", show)
	}
	// Nothing confirms: no confirm.json, no remote, the checkout untouched.
	if _, err := os.Stat(filepath.Join(root, "lattice", ".cache", "confirm.json")); err == nil {
		t.Error("prepare wrote confirm.json")
	}
	if gitIn(t, root, "remote") != "" || gitIn(t, root, "rev-parse", "--abbrev-ref", "HEAD") != "main" || gitIn(t, root, "status", "--porcelain") != "" {
		t.Error("prepare must touch nothing but a local branch")
	}
	// The same decision again is refused: the branch exists.
	if w := postPrepare(srv, "127.0.0.1:5000", hdr, `{"action":"brd.approve","id":"brd.shop","version":1}`); refusedCode(t, w) != "BRANCH_EXISTS" {
		t.Errorf("second prepare: %s", w.Body.String())
	}

	// An agent never prepares a decision.
	agent, _ := prepareFixture(t, "agent:impl-2")
	ah := map[string]string{csrfHeader: tokenOf(t, agent)}
	if w := postPrepare(agent, "127.0.0.1:5000", ah, approveBody); w.Code != 422 || refusedCode(t, w) != "AGENT_PREPARER" {
		t.Errorf("agent: %d %s", w.Code, w.Body.String())
	}
}

// Reading the API changes nothing on disk.
// @verifies api:INV-4
func TestAPIV2ReadsOnly(t *testing.T) {
	f := newV2Fixture(t)
	before := snapshotTree(t, f.root)
	for _, ep := range v2Endpoints {
		f.get(t, ep.path)
	}
	if after := snapshotTree(t, f.root); before != after {
		t.Errorf("a GET changed the workspace:\n%s\n--\n%s", before, after)
	}
}

func snapshotTree(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			data, _ := os.ReadFile(p)
			b.WriteString(p + " " + string(data) + "\n")
		}
		return nil
	})
	return b.String()
}

// /api/v1 is as it was: the overview still answers with its own shape.
func TestAPIV1Unchanged(t *testing.T) {
	f := newV2Fixture(t)
	code, body := f.get(t, "/api/v1/overview")
	var m map[string]interface{}
	if code != 200 || json.Unmarshal(body, &m) != nil || m["counts"] == nil || m["contract"] != nil {
		t.Errorf("/api/v1/overview = %d %s", code, body)
	}
}
