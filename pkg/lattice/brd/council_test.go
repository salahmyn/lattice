package brd

import (
	"os"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func councilAutonomy() config.Autonomy {
	return config.Autonomy{
		DefaultMode: "council",
		Mandates: []config.Mandate{{
			ID: "m-council", Grants: config.GrantBRDApprove, TierMax: 2,
			DelegatedBy: "owner@example.com", Expires: "2027-01-01",
			Council: &config.Council{Seats: []string{"intent", "evidence", "risk"}},
		}},
	}
}

func councilBRD() schema.BRD {
	return schema.BRD{
		ID: "brd.refunds", Version: 3, Status: schema.BRDProposed, Title: "Refunds",
		SuccessCriteria: []schema.BRDCriterion{{ID: "SC-1", Statement: "a"}, {ID: "SC-2", Statement: "b", Tier: 2}},
		Provenance:      schema.BRDProvenance{Source: schema.BRDSourceLLMFromCode, AuthoredBy: "agent-writer"},
	}
}

func vote(seat, actor, verdict string) schema.BRDCouncilVote {
	return schema.BRDCouncilVote{Seat: seat, Actor: actor, Verdict: verdict, Reason: seat + " checked"}
}

func allApprove() []schema.BRDCouncilVote {
	return []schema.BRDCouncilVote{
		vote("intent", "agent-a", schema.VerdictApprove),
		vote("evidence", "agent-b", schema.VerdictApprove),
		vote("risk", "agent-c", schema.VerdictApprove),
	}
}

const today = "2026-10-24"

func TestAssessApprovesUnanimousCouncil(t *testing.T) {
	got := Assess(councilBRD(), councilAutonomy(), "m-council", allApprove(), today)
	if !got.Approved || len(got.Blockers) != 0 {
		t.Fatalf("want approved, got %+v", got)
	}
}

// One "changes" vote sends the BRD back; it is not a blocker.
func TestAssessChangesVote(t *testing.T) {
	votes := allApprove()
	votes[2] = vote("risk", "agent-c", schema.VerdictChanges)
	got := Assess(councilBRD(), councilAutonomy(), "m-council", votes, today)
	if got.Approved || len(got.Blockers) != 0 || len(got.Changes) != 1 || got.Changes[0].Seat != "risk" {
		t.Fatalf("want changes requested by risk, got %+v", got)
	}
}

func TestAssessBlockers(t *testing.T) {
	cases := map[string]struct {
		brd     func(*schema.BRD)
		auto    func(*config.Autonomy)
		mandate string
		votes   func([]schema.BRDCouncilVote) []schema.BRDCouncilVote
		want    string
	}{
		"gated mode": {auto: func(a *config.Autonomy) { a.DefaultMode = "gated" }, want: "keeps BRD approval with a human"},
		"no mode":    {auto: func(a *config.Autonomy) { a.DefaultMode = "" }, want: "keeps BRD approval with a human"},
		"unpinned":   {mandate: "m-self-made", want: "not pinned"},
		"expired":    {auto: func(a *config.Autonomy) { a.Mandates[0].Expires = "2026-01-01" }, want: "does not cover"},
		"over tier_max": {
			auto: func(a *config.Autonomy) { a.Mandates[0].TierMax = 1 }, want: "does not cover",
		},
		"tier 3": {brd: func(b *schema.BRD) { b.SuccessCriteria[0].Tier = 3 }, want: "human-only: criterion SC-1 is tier 3"},
		"financial constraint": {
			brd:  func(b *schema.BRD) { b.Constraints = []schema.BRDConstraint{{Kind: schema.ConstraintFinancial}} },
			want: "human-only: it has a financial constraint",
		},
		"missing seat": {
			votes: func(v []schema.BRDCouncilVote) []schema.BRDCouncilVote { return v[:2] }, want: "seat risk has not voted",
		},
		"author on the council": {
			votes: func(v []schema.BRDCouncilVote) []schema.BRDCouncilVote {
				v[0].Actor = "agent-writer"
				return v
			},
			want: "who wrote this version",
		},
		"one agent, two seats": {
			votes: func(v []schema.BRDCouncilVote) []schema.BRDCouncilVote {
				v[1].Actor = "agent-a"
				return v
			},
			want: "holds both seat intent and seat evidence",
		},
		"no delegating human": {auto: func(a *config.Autonomy) { a.Mandates[0].DelegatedBy = "" }, want: "names no delegated_by"},
		"majority quorum": {
			auto: func(a *config.Autonomy) { a.Mandates[0].Council.Quorum = "majority" }, want: "only unanimous",
		},
	}
	for name, tc := range cases {
		b, auto, mandate, votes := councilBRD(), councilAutonomy(), "m-council", allApprove()
		if tc.brd != nil {
			tc.brd(&b)
		}
		if tc.auto != nil {
			tc.auto(&auto)
		}
		if tc.mandate != "" {
			mandate = tc.mandate
		}
		if tc.votes != nil {
			votes = tc.votes(votes)
		}
		got := Assess(b, auto, mandate, votes, today)
		if got.Approved || !strings.Contains(strings.Join(got.Blockers, "; "), tc.want) {
			t.Errorf("%s: want a blocker containing %q, got %+v", name, tc.want, got)
		}
	}
}

// Votes are stored per version, one per seat; a re-vote replaces the seat's
// earlier vote, and a seat off the council is refused.
func TestRecordVote(t *testing.T) {
	dir := t.TempDir()
	b := councilBRD()
	council := *councilAutonomy().Mandates[0].Council
	for _, v := range []schema.BRDCouncilVote{
		vote("risk", "agent-c", schema.VerdictChanges),
		vote("intent", "agent-a", schema.VerdictApprove),
		vote("risk", "agent-c", schema.VerdictApprove),
	} {
		if err := RecordVote(dir, b, council, v); err != nil {
			t.Fatal(err)
		}
	}
	got, err := LoadVotes(dir, b.ID, b.Version)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Seat != "intent" || got[1].Seat != "risk" || got[1].Verdict != schema.VerdictApprove {
		t.Errorf("votes = %+v, want intent then risk (re-voted approve)", got)
	}
	if err := RecordVote(dir, b, council, vote("legal", "agent-d", schema.VerdictApprove)); err == nil {
		t.Error("a seat off the council should be refused")
	}
	if err := RecordVote(dir, b, council, schema.BRDCouncilVote{Seat: "risk", Actor: "agent-c", Verdict: "approve"}); err == nil {
		t.Error("a vote without a reason should be refused")
	}
	b.Version = 4
	if v, _ := LoadVotes(dir, b.ID, b.Version); len(v) != 0 {
		t.Error("votes belong to one version; a new version starts empty")
	}
}

// Once the human overturns a version, no seat may vote on it again; a new
// version starts a fresh record.
func TestOverturnLocksTheVersion(t *testing.T) {
	dir := t.TempDir()
	b := councilBRD()
	council := *councilAutonomy().Mandates[0].Council
	if err := RecordVote(dir, b, council, vote("intent", "agent-a", schema.VerdictApprove)); err != nil {
		t.Fatal(err)
	}
	if err := MarkOverturned(dir, b.ID, b.Version, Overturn{By: "owner@example.com", Reason: "too vague"}); err != nil {
		t.Fatal(err)
	}
	if err := RecordVote(dir, b, council, vote("risk", "agent-c", schema.VerdictApprove)); err == nil || !strings.Contains(err.Error(), "overturned") {
		t.Errorf("a vote on an overturned version should be refused, got %v", err)
	}
	rec, _ := LoadRecord(dir, b.ID, b.Version)
	if rec.Overturned == nil || len(rec.Votes) != 1 {
		t.Errorf("record = %+v, want the vote kept and the overturn recorded", rec)
	}
	b.Version++
	if err := RecordVote(dir, b, council, vote("risk", "agent-c", schema.VerdictApprove)); err != nil {
		t.Errorf("a revised version can be reviewed again: %v", err)
	}
}

// A new round on the same version goes on top and keeps the earlier one;
// a review section joins the current round, above the earlier ones.
func TestSummaryKeepsRounds(t *testing.T) {
	dir := t.TempDir()
	b := councilBRD()
	if _, err := WriteSummary(dir, b, "# round 1\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteSummary(dir, b, "# round 2\n"); err != nil {
		t.Fatal(err)
	}
	if err := AppendSummary(dir, b.ID, b.Version, "## Acknowledged\n"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(SummaryPath(dir, b.ID, b.Version))
	got := string(data)
	r2, ack, r1 := strings.Index(got, "# round 2"), strings.Index(got, "## Acknowledged"), strings.Index(got, "# round 1")
	if r2 < 0 || ack < r2 || r1 < ack || !strings.Contains(got, "## Earlier round") {
		t.Errorf("summary order wrong:\n%s", got)
	}
}
