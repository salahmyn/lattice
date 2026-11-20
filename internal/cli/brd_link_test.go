package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/brd"
	"github.com/salahmyn/lattice/pkg/lattice/confirm"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// linkRepo copies the sample project and adds an approved BRD whose scope is
// checkout (both halves) and wallet (the feature's half only), and a draft
// nobody reviewed.
func linkRepo(t *testing.T) string {
	t.Helper()
	repo := copyTree(t, "../../examples/sample-project")
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(repo, "lattice", rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("brds/brd.payments.yaml", `id: brd.payments
version: 2
status: approved
title: Payments
business_problem: Customers pay and get paid back.
success_criteria:
  - id: SC-1
    statement: A balance never goes negative.
    maps_to_invariant: wallet:INV-1
implements_via:
  - checkout
approval:
  approved_by: owner@example.com
  approved_at: "2026-01-01T00:00:00Z"
  approved_version: 2
`)
	write("brds/brd.ledger.yaml", `id: brd.ledger
version: 1
status: draft
title: Ledger
business_problem: Every movement of money is recorded.
`)
	setBRD := func(rel, id string) {
		t.Helper()
		p := filepath.Join(repo, "lattice", rel)
		m, err := schema.LoadManifest(p)
		if err != nil {
			t.Fatal(err)
		}
		m.ImplementsBRD = id
		if err := schema.SaveCanonical(p, *m); err != nil {
			t.Fatal(err)
		}
	}
	setBRD("features/checkout.yaml", "brd.payments")
	setBRD("features/wallet.yaml", "brd.payments")
	return repo
}

// linkState is what a link must keep or change: the BRD on disk, the packet
// hash a decision pins, the codes validate raises for it, and the confirmed
// state the audit gives its promise.
type linkState struct {
	brd       schema.BRD
	packet    string
	codes     map[string]bool
	confirmed string
}

func readLinkState(t *testing.T, repo, id, promise string) linkState {
	t.Helper()
	brds, _ := brd.LoadAll(filepath.Join(repo, "lattice", "brds"), filepath.Join(repo, "lattice"))
	b := brd.Find(brds, id)
	if b == nil {
		t.Fatalf("no BRD %s", id)
	}
	var feats []schema.Manifest
	for _, rel := range []string{"features/checkout.yaml", "features/wallet.yaml", "features/billing.yaml", "features/checkout/refund.yaml"} {
		m, err := schema.LoadManifest(filepath.Join(repo, "lattice", rel))
		if err != nil {
			t.Fatal(err)
		}
		feats = append(feats, *m)
	}
	st := linkState{brd: *b, codes: map[string]bool{}}
	st.packet = confirm.PacketHash(confirm.PacketInput{BRD: *b, Diff: []string{"initial"}, Declared: confirm.DeclaredBy(feats, id)})

	out, _ := run(t, repo, newValidateCommand)
	var vr struct {
		Violations []schema.Violation `json:"violations"`
	}
	if err := json.Unmarshal([]byte(out), &vr); err != nil {
		t.Fatalf("validate output: %v\n%s", err, out)
	}
	for _, v := range vr.Violations {
		if strings.HasPrefix(v.Subject, id+"|") || v.Location != nil && v.Location.File == b.SourcePath {
			st.codes[v.Code] = true
		}
	}

	out, _ = run(t, repo, newAuditCommand)
	var ar struct {
		Rows []struct {
			Promise string `json:"promise"`
			Signals struct {
				Confirmed struct {
					Value  string `json:"value"`
					Detail string `json:"detail"`
				} `json:"confirmed"`
			} `json:"signals"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &ar); err != nil {
		t.Fatalf("audit output: %v\n%s", err, out)
	}
	for _, r := range ar.Rows {
		if r.Promise == promise {
			st.confirmed = r.Signals.Confirmed.Value + ": " + r.Signals.Confirmed.Detail
		}
	}
	if st.confirmed == "" {
		t.Fatalf("audit has no row for %s:\n%s", promise, out)
	}
	return st
}

func linkJSON(t *testing.T, repo string, args ...string) linkChange {
	t.Helper()
	out, err := run(t, repo, newBRDCommand, append([]string{"link"}, args...)...)
	if err != nil {
		t.Fatalf("brd link %v: %v\n%s", args, err, out)
	}
	var c linkChange
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		t.Fatalf("brd link output: %v\n%s", err, out)
	}
	return c
}

// Completing a link the feature already declares keeps the approved BRD's
// version, its approval, its packet hash and its audit standing; adding a
// feature neither half named bumps the version and voids the approval.
// @verifies brds:INV-10
// @verifies confirm:INV-8
func TestBRDLinkReconcileKeepsApproval(t *testing.T) {
	repo := linkRepo(t)
	before := readLinkState(t, repo, "brd.payments", "wallet:INV-1")
	if !before.codes[schema.CodeBRDLinkOneSided] || before.codes[schema.CodeBRDDrift] {
		t.Fatalf("setup: want BRD_LINK_ONE_SIDED and no BRD_DRIFT, got %v", before.codes)
	}

	c := linkJSON(t, repo, "brd.payments", "wallet")
	if c.Change != linkReconciled || c.Bumped || c.Version != 2 {
		t.Fatalf("link wallet: want reconciled at v2, got %+v", c)
	}
	after := readLinkState(t, repo, "brd.payments", "wallet:INV-1")
	if after.brd.Version != 2 || after.brd.Approval == nil || after.brd.Approval.ApprovedVersion != 2 || after.brd.Status != schema.BRDApproved {
		t.Fatalf("reconcile changed the version or approval: %+v %+v", after.brd, after.brd.Approval)
	}
	if strings.Join(after.brd.ImplementsVia, ",") != "checkout,wallet" {
		t.Fatalf("implements_via = %v", after.brd.ImplementsVia)
	}
	if after.packet != before.packet {
		t.Fatalf("reconcile changed the packet hash: %s -> %s", before.packet, after.packet)
	}
	if after.codes[schema.CodeBRDDrift] || after.codes[schema.CodeBRDLinkOneSided] {
		t.Fatalf("after reconcile: %v", after.codes)
	}
	if after.confirmed != before.confirmed {
		t.Fatalf("reconcile changed the audit's confirmed state: %q -> %q", before.confirmed, after.confirmed)
	}

	// billing names no BRD and the BRD does not list it: a scope change.
	c = linkJSON(t, repo, "brd.payments", "billing")
	if c.Change != linkScope || !c.Bumped || c.Version != 3 {
		t.Fatalf("link billing: want scope change bumped to v3, got %+v", c)
	}
	scoped := readLinkState(t, repo, "brd.payments", "wallet:INV-1")
	if scoped.brd.Version != 3 || !scoped.codes[schema.CodeBRDDrift] {
		t.Fatalf("scope change: want v3 with BRD_DRIFT, got v%d %v", scoped.brd.Version, scoped.codes)
	}
	if scoped.packet == after.packet {
		t.Fatal("scope change kept the packet hash")
	}
	if scoped.confirmed == after.confirmed || !strings.HasPrefix(scoped.confirmed, "drifted") {
		t.Fatalf("scope change: want the audit to read drifted, got %q", scoped.confirmed)
	}

	// A draft nobody reviewed keeps its version on a scope change.
	c = linkJSON(t, repo, "brd.ledger", "checkout.refund")
	if c.Change != linkScope || c.Bumped || c.Version != 1 {
		t.Fatalf("link to an unreviewed draft: want scope change at v1, got %+v", c)
	}
}

// --reconcile completes every mirrored one-sided link without moving a
// version, and leaves a contested one for a human.
// @verifies brds:INV-10
func TestBRDLinkReconcileAll(t *testing.T) {
	repo := linkRepo(t)
	// brd.ledger lists billing, which names no BRD (mirrored), and wallet,
	// which names brd.payments (contested).
	p := filepath.Join(repo, "lattice", "brds", "brd.ledger.yaml")
	data, _ := os.ReadFile(p)
	if err := os.WriteFile(p, append(data, []byte("implements_via:\n  - billing\n  - wallet\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, repo, newBRDCommand, "link", "--reconcile")
	if err != nil {
		t.Fatalf("brd link --reconcile: %v\n%s", err, out)
	}
	var res struct {
		Changes []linkChange `json:"changes"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("output: %v\n%s", err, out)
	}
	got := map[string]string{}
	for _, c := range res.Changes {
		got[c.BRD+"|"+c.Feature] = c.Change
		if c.Bumped || c.LeftBumped {
			t.Errorf("--reconcile bumped a version: %+v", c)
		}
	}
	want := map[string]string{
		"brd.payments|wallet": linkReconciled,
		"brd.ledger|billing":  linkReconciled,
		"brd.ledger|wallet":   linkSkipped,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: want %s, got %q (all: %v)", k, v, got[k], got)
		}
	}

	pay := readLinkState(t, repo, "brd.payments", "wallet:INV-1")
	if pay.brd.Version != 2 || strings.Join(pay.brd.ImplementsVia, ",") != "checkout,wallet" || pay.codes[schema.CodeBRDDrift] {
		t.Fatalf("brd.payments after --reconcile: v%d %v %v", pay.brd.Version, pay.brd.ImplementsVia, pay.codes)
	}
	led := readLinkState(t, repo, "brd.ledger", "wallet:INV-1")
	if led.brd.Version != 1 || !led.codes[schema.CodeBRDLinkOneSided] {
		t.Fatalf("brd.ledger: want v1 with the contested link still flagged, got v%d %v", led.brd.Version, led.codes)
	}
	billing, err := schema.LoadManifest(filepath.Join(repo, "lattice", "features", "billing.yaml"))
	if err != nil || billing.ImplementsBRD != "brd.ledger" {
		t.Fatalf("billing implements_brd = %q (%v)", billing.ImplementsBRD, err)
	}
	wallet, _ := schema.LoadManifest(filepath.Join(repo, "lattice", "features", "wallet.yaml"))
	if wallet.ImplementsBRD != "brd.payments" {
		t.Fatalf("--reconcile moved the contested feature: %q", wallet.ImplementsBRD)
	}
}
