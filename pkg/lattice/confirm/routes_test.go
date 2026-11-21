package confirm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature"
	"github.com/salahmyn/lattice/pkg/lattice/confirm/signature/sigtest"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

var (
	bothRoutes    = config.Confirm{}
	signatureOnly = config.Confirm{Routes: config.Routes{config.RouteSignature}}
	reviewOnly    = config.Confirm{Routes: config.Routes{config.RouteReview}}
)

// computeRoutes is sigFixture.compute under a confirm.routes setting.
func (f *sigFixture) computeRoutes(p Platform, routes config.Confirm) File {
	f.t.Helper()
	found, _, err := Scan(Git{Dir: f.r.Dir}, "main", "lattice")
	if err != nil {
		f.t.Fatal(err)
	}
	return Compute(context.Background(), Input{Commit: "head", Branch: "main", Found: found, Platform: p, Now: fixedNow,
		Packet: func(d Decision) (string, bool) { return d.PacketHash, true },
		Signed: &signature.Repo{Dir: f.r.Dir, LatticeRel: "lattice", Ref: "main", Repository: sigtest.Repository},
		Routes: routes})
}

func writeConfig(t *testing.T, latticeDir, body string) {
	t.Helper()
	if err := os.MkdirAll(latticeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(latticeDir, config.ConfigFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// confirm.routes only narrows. With routes: [signature] a decision confirmed
// only by a review reads pending, ROUTE_NOT_ACCEPTED, with the approver kept
// for display; by default it reads confirmed, unchanged. A decision confirmed
// by signature reads confirmed under both settings, and with routes: [review]
// only a review confirms. A reader applies the setting to a file written
// before it changed, and an unreadable config.yaml reads unknown.
// @verifies confirm:INV-22
func TestRoutesOnlyNarrow(t *testing.T) {
	plat := &fake{pr: merged("dev", "h2"), reviews: []Review{approval("ana", "h2")}}
	in := Input{Commit: "h", Branch: "main", Found: foundOne(decision(ActionApprove)), Platform: plat, Now: fixedNow,
		Packet: func(d Decision) (string, bool) { return d.PacketHash, true }}

	def := Compute(context.Background(), in)
	if r := def.Records[0]; r.State != StateConfirmed || r.Via != ViaReview || r.ApproverLogin != "ana" {
		t.Fatalf("default routes, review: %+v", r)
	}
	in.Routes = bothRoutes
	if both := Compute(context.Background(), in); !reflect.DeepEqual(both.Records, def.Records) {
		t.Errorf("an empty routes list is the default:\n got %+v\nwant %+v", both.Records, def.Records)
	}

	in.Routes = signatureOnly
	sig := Compute(context.Background(), in)
	r := sig.Records[0]
	if r.State != StatePending || r.Assurance.Code != CodeRouteNotAccepted || r.Via != ViaReview || r.ApproverLogin != "ana" || r.PR != 12 {
		t.Fatalf("routes: [signature], review only: %+v", r)
	}
	if !strings.Contains(r.Assurance.Text, "accepts only signature") || !strings.Contains(r.Assurance.Text, "approved by ana on PR #12") {
		t.Errorf("the reason names the approval and the routes: %q", r.Assurance.Text)
	}
	if got := r.Assurance.Params["routes"]; !reflect.DeepEqual(got, []string{"signature"}) {
		t.Errorf("params routes: %v", got)
	}
	if txt := sig.Text(); strings.Contains(txt, "human") || !strings.Contains(txt, "[ROUTE_NOT_ACCEPTED]") {
		t.Errorf("text must not read human:\n%s", txt)
	}
	// A misspelt route accepts nothing: it narrows, never widens.
	in.Routes = config.Confirm{Routes: config.Routes{"reviews"}}
	if r := Compute(context.Background(), in).Records[0]; r.State != StatePending || r.Assurance.Code != CodeRouteNotAccepted {
		t.Errorf("an unknown route must not confirm: %+v", r)
	}
	// A stale review is not shown as a confirmation that drifted.
	in.Routes, in.Packet = signatureOnly, func(Decision) (string, bool) { return "sha256:bb", true }
	if r := Compute(context.Background(), in).Records[0]; r.State != StatePending || r.Assurance.Code != CodeRouteNotAccepted {
		t.Errorf("a stale review under routes: [signature]: %+v", r)
	}

	// A reader applies the setting to a file written by default.
	dir := filepath.Join(t.TempDir(), "lattice")
	if err := SaveCache(dir, def); err != nil {
		t.Fatal(err)
	}
	if back := LoadCache(dir); back.Records[0].State != StateConfirmed {
		t.Fatalf("no config: %+v", back.Records[0])
	}
	writeConfig(t, dir, "confirm:\n  routes: [signature]\n")
	back := LoadCache(dir)
	if r := back.Records[0]; r.State != StatePending || r.Assurance.Code != CodeRouteNotAccepted {
		t.Errorf("a reader under routes: [signature]: %+v", r)
	}
	if off := Offline(back, "h"); off.Records[0].State != StatePending {
		t.Errorf("--offline reads the narrowed verdict: %+v", off.Records[0])
	}
	writeConfig(t, dir, "confirm:\n  routes: signature\n")
	if r := LoadCache(dir).Records[0]; r.State != StatePending {
		t.Errorf("a scalar route: %+v", r)
	}
	writeConfig(t, dir, "confirm: [\n")
	if r := LoadCache(dir).Records[0]; r.State != StateUnknown || r.Assurance.Code != CodeRoutesUnreadable {
		t.Errorf("an unreadable config.yaml reads unknown: %+v", r)
	}
	writeConfig(t, dir, "confirm:\n  routes: [review, signature]\n")
	if r := LoadCache(dir).Records[0]; r.State != StateConfirmed {
		t.Errorf("both routes listed: %+v", r)
	}

	// The signature route: confirmed under both settings, not under [review].
	f := newSigFixture(t, sigtest.SKEd25519, nil)
	for name, routes := range map[string]config.Confirm{"default": bothRoutes, "signature": signatureOnly} {
		if r := f.record(f.computeRoutes(hostListing("ana", f.key), routes), "brd.fees"); r.State != StateConfirmed || r.Via != ViaSignature || r.ApproverLogin != "ana" {
			t.Errorf("signature under %s: %+v", name, r)
		}
	}
	host := hostListing("ana", f.key)
	r = f.record(f.computeRoutes(host, reviewOnly), "brd.fees")
	if r.State != StatePending || r.Assurance.Code != CodeRouteNotAccepted || r.Via != ViaSignature || !strings.Contains(r.Assurance.Text, "accepts only review") {
		t.Errorf("signature under routes: [review]: %+v", r)
	}
	host.pr, host.reviews = merged("dev", "h2"), []Review{approval("bo", "h2")}
	if r := f.record(f.computeRoutes(host, reviewOnly), "brd.fees"); r.State != StateConfirmed || r.Via != ViaReview || r.ApproverLogin != "bo" {
		t.Errorf("signed and reviewed under routes: [review]: %+v", r)
	}
	// Signed and reviewed under [signature]: the signature confirms.
	if r := f.record(f.computeRoutes(host, signatureOnly), "brd.fees"); r.State != StateConfirmed || r.Via != ViaSignature {
		t.Errorf("signed and reviewed under routes: [signature]: %+v", r)
	}
	// A failing signature and an approving review under [signature]: the
	// signature's own failure is the verdict, never the review.
	soft := newSigFixture(t, sigtest.Software, nil)
	sh := hostListing("ana", soft.key)
	sh.pr, sh.reviews = merged("dev", "h2"), []Review{approval("bo", "h2")}
	if r := soft.record(soft.computeRoutes(sh, signatureOnly), "brd.fees"); r.State != StatePending || r.Via != ViaSignature || r.Assurance.Code != signature.CodeSoftwareKey {
		t.Errorf("a software key and a review under routes: [signature]: %+v", r)
	}
}

// confirm --unsigned lists a decision approved by review that routes:
// [signature] does not accept, as resting on a review, with the sign command.
// @verifies confirm:INV-19
// @verifies confirm:INV-22
func TestUnsignedListsReviewNotAccepted(t *testing.T) {
	plat := &fake{pr: merged("dev", "h2"), reviews: []Review{approval("ana", "h2")}}
	d := decision(ActionApprove)
	found := foundOne(d)
	file := Compute(context.Background(), Input{Commit: "h", Branch: "main", Found: found, Platform: plat, Now: fixedNow,
		Packet: func(d Decision) (string, bool) { return d.PacketHash, true }, Routes: signatureOnly})
	brds := []schema.BRD{{ID: "brd.x", Version: 2, Status: schema.BRDApproved,
		Approval: &schema.BRDApproval{ApprovedBy: "ana@example.com", ApprovedVersion: 2}}}
	l := ListUnsigned(UnsignedInput{BRDs: brds, Found: found, Cache: &file})
	if len(l.Decisions) != 1 {
		t.Fatalf("one decision awaits a signature, the review: %+v", l.Decisions)
	}
	u := l.Decisions[0]
	if u.Rests != RestsReview || u.By != "ana" || u.PR != 12 || u.DecisionPath != found[0].Path ||
		strings.Join(u.Commands, "|") != "lattice sign "+found[0].Path || !strings.Contains(u.Text, "confirm.routes does not accept") {
		t.Errorf("the review-only decision: %+v", u)
	}
	// A pending decision with no approval is not listed.
	file.Records[0].Assurance.Code = "NO_APPROVAL"
	if l := ListUnsigned(UnsignedInput{BRDs: brds[:0], Found: found, Cache: &file}); len(l.Decisions) != 0 {
		t.Errorf("a pending decision without an approval is not listed: %+v", l.Decisions)
	}
}

// collabHost is a platform that also lists collaborators.
type collabHost struct {
	fake
	collabs []Collaborator
	err     error
	asks    int
}

func (c *collabHost) Collaborators(context.Context) ([]Collaborator, error) {
	c.asks++
	return c.collabs, c.err
}

// The sole-approver note: a pull request opened by a bot or agent account and
// approved by the repository's only human with write access carries
// SOLE_HUMAN_APPROVER, and the verdict is the same as without it. A human
// author, a second human writer, an unreadable collaborator list, or a
// platform that cannot list collaborators gives none.
// @verifies confirm:INV-23
func TestSoleHumanApproverNote(t *testing.T) {
	writers := []Collaborator{{Login: "Ana", Type: "User", Write: true}, {Login: "reader", Type: "User"},
		{Login: "release-agent[bot]", Type: "Bot", Write: true}, {Login: "agent-impl", Type: "User", Write: true}}
	host := func(author, kind string, collabs []Collaborator, err error) *collabHost {
		h := &collabHost{collabs: collabs, err: err}
		h.pr = merged(author, "h2")
		h.pr.AuthorType = kind
		h.reviews = []Review{approval("ana", "h2")}
		return h
	}
	found := append(foundOne(decision(ActionApprove)), foundOne(decision(ActionPromiseConfirm))...)
	run := func(p Platform) File {
		return Compute(context.Background(), Input{Commit: "h", Branch: "main", Found: found, Platform: p, Now: fixedNow,
			Packet: func(d Decision) (string, bool) { return d.PacketHash, true }})
	}

	bot := host("release-agent[bot]", "Bot", writers, nil)
	file := run(bot)
	r := file.Records[0]
	if len(r.Notes) != 1 || r.Notes[0].Code != CodeSoleHumanApprover || r.PRAuthor != "release-agent[bot]" || !r.PRAuthorBot {
		t.Fatalf("a bot-opened PR approved by the sole human writer: %+v", r)
	}
	if bot.asks != 1 {
		t.Errorf("the collaborators are asked once per run, not per decision: %d", bot.asks)
	}
	for _, want := range []string{"proves only that an account other than the author approved", "confirm.routes: [signature]", "release-agent[bot]"} {
		if !strings.Contains(r.Notes[0].Text, want) {
			t.Errorf("the note must say %q: %q", want, r.Notes[0].Text)
		}
	}
	if txt := file.Text(); !strings.Contains(txt, "human (approved by ana on PR #12)") || !strings.Contains(txt, "[SOLE_HUMAN_APPROVER]") ||
		strings.Contains(txt, "opened by release-agent[bot], a bot or agent account: it prepared") {
		t.Errorf("text:\n%s", txt)
	}
	// The note is information: the verdict is the one a platform without a
	// collaborator list gives.
	plain := run(&fake{pr: bot.pr, reviews: bot.reviews})
	for i := range file.Records {
		got := file.Records[i]
		if len(plain.Records[i].Notes) != 0 {
			t.Fatalf("no collaborator list, no note: %+v", plain.Records[i])
		}
		got.Notes = nil
		if !reflect.DeepEqual(got, plain.Records[i]) {
			t.Errorf("the note changed the verdict:\n got %+v\nwant %+v", got, plain.Records[i])
		}
	}
	if r := run(host("agent-impl", "User", writers, nil)).Records[0]; len(r.Notes) != 1 || !r.PRAuthorBot {
		t.Errorf("an agent account's PR: %+v", r)
	}

	for name, p := range map[string]*collabHost{
		"human author":        host("dev", "User", writers, nil),
		"two human writers":   host("release-agent[bot]", "Bot", append(writers, Collaborator{Login: "bo", Type: "User", Write: true}), nil),
		"another sole writer": host("release-agent[bot]", "Bot", []Collaborator{{Login: "bo", Type: "User", Write: true}, {Login: "ana", Type: "User"}}, nil),
		"unreadable list":     host("release-agent[bot]", "Bot", writers, errors.New("403 Forbidden")),
		"empty list":          host("release-agent[bot]", "Bot", nil, nil),
	} {
		if r := run(p).Records[0]; len(r.Notes) != 0 || r.State != StateConfirmed {
			t.Errorf("%s: no note, the verdict unchanged: %+v", name, r)
		}
	}
	// Under routes: [signature] the review does not confirm, so no note.
	if r := Compute(context.Background(), Input{Commit: "h", Found: found, Platform: host("release-agent[bot]", "Bot", writers, nil), Now: fixedNow,
		Routes: signatureOnly}).Records[0]; len(r.Notes) != 0 {
		t.Errorf("a review not accepted carries no note: %+v", r)
	}
	// A cached verdict keeps its author, so the note is made again.
	again := Compute(context.Background(), Input{Commit: "h", Found: found, Platform: host("release-agent[bot]", "Bot", writers, nil), Cache: &file, Now: fixedNow,
		Packet: func(d Decision) (string, bool) { return d.PacketHash, true }})
	if r := again.Records[0]; !strings.HasSuffix(r.Assurance.Text, "(cached)") || len(r.Notes) != 1 {
		t.Errorf("a cached verdict: %+v", r)
	}
}

func TestGitHubCollaborators(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/collaborators" || r.URL.Query().Get("affiliation") != "all" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "Must have push access", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`[{"login":"ana","type":"User","permissions":{"admin":true,"maintain":true,"push":true,"triage":true,"pull":true}},
			{"login":"bo","type":"User","permissions":{"pull":true}},
			{"login":"cy","type":"User","permissions":{"maintain":true}},
			{"login":"ci[bot]","type":"Bot","permissions":{"push":true}}]`))
	}))
	defer srv.Close()
	got, err := GitHub{Token: "tok", Owner: "o", Repo: "r", BaseURL: srv.URL}.Collaborators(context.Background())
	want := []Collaborator{{"ana", "User", true}, {"bo", "User", false}, {"cy", "User", true}, {"ci[bot]", "Bot", true}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("collaborators: %+v %v", got, err)
	}
	if _, err := (GitHub{Owner: "o", Repo: "r", BaseURL: srv.URL}).Collaborators(context.Background()); err == nil {
		t.Error("a forbidden list must be an error, never an empty list")
	}
}

// The shape of a ROUTE_NOT_ACCEPTED record and a SOLE_HUMAN_APPROVER note
// adds fields to lattice.confirm/1 and renames none; it is frozen too.
func TestConfirmRoutesContractGolden(t *testing.T) {
	p := &collabHost{collabs: []Collaborator{{Login: "ana", Type: "User", Write: true}}}
	p.pr = merged("release-agent[bot]", "h2")
	p.pr.AuthorType = "Bot"
	p.reviews = []Review{approval("ana", "h2")}
	in := Input{Commit: "deadbeef", Branch: "main", Found: foundOne(decision(ActionApprove)), Platform: p, Now: fixedNow,
		Packet: func(d Decision) (string, bool) { return d.PacketHash, true }}
	noted := Compute(context.Background(), in).Records[0]
	in.Routes = signatureOnly
	narrowed := Compute(context.Background(), in).Records[0]
	narrowed.ID = "brd.y"
	f := File{Contract: Schema, Commit: "deadbeef", Branch: "main", GeneratedAt: "2026-01-02T03:04:05Z",
		Assurance: Assurance{State: "pass", Honesty: "fact", Code: "PLATFORM_READ", Text: "approvals read from the platform"},
		Records:   []Record{noted, narrowed}}
	got, err := json.MarshalIndent(Seal(f, Stamp{CIRun: "4812", Platform: "github", Repository: "acme/shop"}), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "testdata/confirm-routes.golden.json", append(got, '\n'))
}
