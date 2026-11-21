package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runPromote(t *testing.T, dir, actor string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	io := &IO{Repo: dir, JSON: true, Out: &out, Err: &out, Actor: actor}
	cmd := newFeatureCommand(io)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs(append([]string{"promote"}, args...))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.Execute()
	return out.String(), err
}

// Without --by, promote names the production entry points and the flag
// caveat and writes neither the manifest nor the ledger.
// @verifies cli:INV-7
func TestFeaturePromotePrintsAndWritesNothing(t *testing.T) {
	dir := driftFixture(t)
	manifest := filepath.Join(dir, "lattice/features/shop/checkout.yaml")
	before := readFile(t, manifest)

	out, err := runPromote(t, dir, "test", "shop.checkout")
	if err != nil {
		t.Fatalf("promote: %v\n%s", err, out)
	}
	var rep promoteReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("output: %v\n%s", err, out)
	}
	if len(rep.EntryPoints) != 1 || rep.EntryPoints[0].ID != "shop.orders_route" || rep.EntryPoints[0].Handler != "src.route.ordersRoute" {
		t.Errorf("entry points = %+v", rep.EntryPoints)
	}
	if len(rep.Criteria) != 1 || rep.Criteria[0].Criterion != "SC-1" {
		t.Errorf("criteria = %+v", rep.Criteria)
	}
	if rep.Promoted || !strings.Contains(rep.Note, "Lattice cannot see runtime flags") {
		t.Errorf("report = %+v", rep)
	}
	if readFile(t, manifest) != before {
		t.Error("print mode changed the manifest")
	}
	if _, err := os.Stat(filepath.Join(dir, "lattice/.ledger/ledger.jsonl")); err == nil {
		t.Error("print mode wrote the ledger")
	}

	var text bytes.Buffer
	cmd := newFeatureCommand(&IO{Repo: dir, Out: &text, Err: &text})
	cmd.SetArgs([]string{"promote", "shop.checkout"})
	cmd.SetOut(&text)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"shop.orders_route", "Lattice cannot see runtime flags", "Nothing was written"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("text output lacks %q:\n%s", want, text.String())
		}
	}
}

// --by sets only status, in place, and records who did it and on what evidence.
// @verifies cli:INV-7
func TestFeaturePromoteWrites(t *testing.T) {
	dir := driftFixture(t)
	manifest := filepath.Join(dir, "lattice/features/shop/checkout.yaml")
	before := readFile(t, manifest)

	out, err := runPromote(t, dir, "test", "shop.checkout", "--by", "a@b.c")
	if err != nil {
		t.Fatalf("promote: %v\n%s", err, out)
	}
	want := strings.Replace(before, "status: proposal\n", "status: production\n", 1)
	if got := readFile(t, manifest); got != want {
		t.Errorf("manifest changed beyond the status line:\n%s\nwant:\n%s", got, want)
	}
	led := readFile(t, filepath.Join(dir, "lattice/.ledger/ledger.jsonl"))
	for _, s := range []string{`"unit":"shop.checkout"`, `"actor":"test"`, "proposal→production", "a@b.c", "shop.orders_route"} {
		if !strings.Contains(led, s) {
			t.Errorf("ledger lacks %q:\n%s", s, led)
		}
	}
	// the feature is production now, so a second promote is refused
	if out, err := runPromote(t, dir, "test", "shop.checkout", "--by", "a@b.c"); err == nil || !strings.Contains(out, "PROMOTE_STATUS") {
		t.Errorf("second promote: err=%v\n%s", err, out)
	}
	// and the drift finding is gone
	by, _, _ := driftValidate(t, dir)
	if len(by["STATUS_DRIFT"]) != 0 {
		t.Errorf("STATUS_DRIFT after promote: %+v", by["STATUS_DRIFT"])
	}
}

// A feature no production entry point names, and an agent identity, are refused
// and leave the manifest alone.
// @verifies cli:INV-7
func TestFeaturePromoteRefuses(t *testing.T) {
	dir := driftFixture(t)
	refunds := filepath.Join(dir, "lattice/features/shop/refunds.yaml")
	checkout := filepath.Join(dir, "lattice/features/shop/checkout.yaml")
	r0, c0 := readFile(t, refunds), readFile(t, checkout)

	if out, err := runPromote(t, dir, "test", "shop.refunds", "--by", "a@b.c"); err == nil || !strings.Contains(out, "PROMOTE_NO_EVIDENCE") {
		t.Errorf("refunds: err=%v\n%s", err, out)
	}
	if out, err := runPromote(t, dir, "test", "shop.checkout", "--by", "agent:bot"); err == nil || !strings.Contains(out, "PROMOTE_HUMAN_ONLY") {
		t.Errorf("agent --by: err=%v\n%s", err, out)
	}
	if out, err := runPromote(t, dir, "agent:bot", "shop.checkout", "--by", "a@b.c"); err == nil || !strings.Contains(out, "PROMOTE_HUMAN_ONLY") {
		t.Errorf("agent actor: err=%v\n%s", err, out)
	}
	if out, err := runPromote(t, dir, "test", "shop.nothing", "--by", "a@b.c"); err == nil || !strings.Contains(out, "FEATURE_NOT_FOUND") {
		t.Errorf("unknown feature: err=%v\n%s", err, out)
	}
	if readFile(t, refunds) != r0 || readFile(t, checkout) != c0 {
		t.Error("a refused promote changed a manifest")
	}
	if _, err := os.Stat(filepath.Join(dir, "lattice/.ledger/ledger.jsonl")); err == nil {
		t.Error("a refused promote wrote the ledger")
	}
}

// STATUS_DRIFT's next action names the command this one implements.
// @verifies cli:INV-7
func TestStatusDriftNamesPromote(t *testing.T) {
	dir := driftFixture(t)
	by, _, _ := driftValidate(t, dir)
	if len(by["STATUS_DRIFT"]) != 1 {
		t.Fatalf("STATUS_DRIFT = %+v", by["STATUS_DRIFT"])
	}
	d := by["STATUS_DRIFT"][0].NextAction.Detail
	if !strings.Contains(d, "lattice feature promote shop.checkout") || !strings.Contains(d, "runs it with --by <email>") {
		t.Errorf("next action = %q", d)
	}
}
