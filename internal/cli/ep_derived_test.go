package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/entrypoints"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

// runtimeRepo is a workspace holding the runtime-roots fixture: a serve
// handler (orders), a scheduled job (reports) and two roots that cannot be
// proposed.
func runtimeRepo(t *testing.T) (string, *workspace.Workspace) {
	t.Helper()
	repo := t.TempDir()
	fixture := filepath.Join("..", "..", "pkg", "lattice", "entrypoints", "testdata", "runtime")
	for _, c := range [][2]string{{"api", "api"}, {"db", filepath.Join("supabase", "migrations")}} {
		if err := os.CopyFS(filepath.Join(repo, c[1]), os.DirFS(filepath.Join(fixture, c[0]))); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, s string) {
		p = filepath.Join(repo, p)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("lattice/config.yaml", "{}\n")
	write("lattice/adapters.yaml", "adapters:\n  typescript:\n    enabled: true\n  postgres:\n    enabled: true\n    migrations: [\"supabase/migrations/*.sql\"]\n")
	for _, id := range []string{"audit", "orders", "reports"} {
		write("lattice/features/"+id+".yaml", "id: "+id+"\nversion: 1\nstatus: production\npurpose: "+id+"\nowners:\n  business: x\n  engineering: y\n")
	}
	ws, err := workspace.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	return repo, ws
}

func filesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func derivedIDs(t *testing.T, ws *workspace.Workspace) []string {
	t.Helper()
	kg, err := buildGraphWith(context.Background(), ws, buildOptions{readOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, ep := range kg.EntryPoints {
		if ep.DetectedBy != "" {
			ids = append(ids, ep.ID)
		}
	}
	return ids
}

// extract derives proposals into the graph and writes no entry-point
// manifest: a root that later moves leaves nothing behind.
// @verifies entrypoints:INV-5
func TestExtractWritesNoProposalManifests(t *testing.T) {
	repo, ws := runtimeRepo(t)
	if out, err := run(t, repo, newExtractCommand); err != nil {
		t.Fatalf("extract: %v\n%s", err, out)
	}
	if files := filesUnder(t, ws.EntryPointsDir()); len(files) != 0 {
		t.Errorf("extract wrote entry-point files: %v", files)
	}
	if ids := derivedIDs(t, ws); len(ids) != 2 {
		t.Errorf("the graph should hold the 2 derived proposals, got %v", ids)
	}
}

// Accepting a derived proposal writes a production manifest in canonical
// YAML without its flow; rejecting one archives it so it is not proposed
// again; an unknown id is still an error.
// @verifies entrypoints:INV-3, entrypoints:INV-5
func TestAcceptAndRejectDerivedProposals(t *testing.T) {
	repo, ws := runtimeRepo(t)
	if out, err := run(t, repo, newEPCommand, "list"); err != nil || !json.Valid([]byte(out)) {
		t.Fatalf("list: %v\n%s", err, out)
	} else {
		var eps []schema.EntryPoint
		if json.Unmarshal([]byte(out), &eps) != nil || len(eps) != 2 {
			t.Fatalf("ep list should show the 2 derived proposals: %s", out)
		}
	}
	if _, err := run(t, repo, newEPCommand, "accept", "ep.nope"); err == nil {
		t.Error("an unknown id should still fail")
	}

	if out, err := run(t, repo, newEPCommand, "accept", "ep.http.api.index.serve"); err != nil {
		t.Fatalf("accept: %v\n%s", err, out)
	}
	eps, err := entrypoints.LoadEntryPoints(ws.EntryPointsDir())
	if err != nil || len(eps) != 1 || eps[0].Status != schema.StatusProduction || len(eps[0].Flow) != 0 ||
		eps[0].DetectedBy != "runtime_root:serve" {
		t.Fatalf("accepted manifest = %+v, %v", eps, err)
	}
	if out, err := run(t, repo, newFmtCommand, "--check", filepath.Join(ws.EntryPointsDir(), "ep", "http", "api", "index", "serve.yaml")); err != nil {
		t.Errorf("the accepted manifest is not canonical: %v\n%s", err, out)
	}

	if out, err := run(t, repo, newEPCommand, "reject", "ep.cron.nightly_x"); err != nil {
		t.Fatalf("reject: %v\n%s", err, out)
	}
	if !entrypoints.IsRejected(ws.EntryPointsDir(), "ep.cron.nightly_x") {
		t.Error("the rejected proposal is not archived under .rejected/")
	}
	if ids := derivedIDs(t, ws); len(ids) != 1 || ids[0] != "ep.http.api.index.serve" {
		t.Errorf("after reject the next run should propose only the accepted one, got %v", ids)
	}
	if got := filesUnder(t, ws.EntryPointsDir()); len(got) != 2 {
		t.Errorf("want the accepted manifest and the archived one, got %v", got)
	}
}
