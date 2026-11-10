package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/check"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/ui"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

func apiGet(srv *ui.Server, url string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Host, req.RemoteAddr = "127.0.0.1:7070", "127.0.0.1:41000"
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

// GET /api/v2/check returns exactly what `lattice check --json` prints: same
// function, same encoder, byte for byte, for a BROKEN change, with and without
// --ci.
// @verifies api:INV-5
func TestAPIV2CheckIsTheCLIReport(t *testing.T) {
	clearCIEnv(t)
	f := newCheckFixture(t)
	f.git("checkout", "-q", "-b", "drop-tag")
	f.write("shop/shop.go", shopGo(false))
	f.commit("drop the tag")

	ws, err := workspace.Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	build := func(ctx context.Context) (schema.KnowledgeGraph, error) {
		return buildGraphWith(ctx, ws, buildOptions{readOnly: true})
	}
	srv := ui.New(ws, ui.Options{GraphBuilder: build, ReadOnlyGraphBuilder: build})

	for _, c := range []struct {
		args []string
		url  string
	}{
		{[]string{"--base", "main"}, "/api/v2/check?base=main"},
		{[]string{"--base", "main", "--ci"}, "/api/v2/check?base=main&ci=1"},
	} {
		cli, err := run(t, f.dir, newCheckCommand, c.args...)
		if err == nil {
			t.Fatalf("a dropped enforcer exits 1: %v", c.args)
		}
		w := apiGet(srv, c.url)
		if w.Code != 200 || w.Body.String() != cli {
			t.Errorf("%v: the API and the CLI differ (status %d)\n--- CLI\n%s\n--- API\n%s", c.args, w.Code, cli, w.Body.String())
		}
	}

	if w := apiGet(srv, "/api/v2/check"); w.Code != 400 {
		t.Errorf("no base: %d, want 400", w.Code)
	}
	if w := apiGet(srv, "/api/v2/check?base=no-such-rev"); w.Code != 400 {
		t.Errorf("a bad base: %d, want 400", w.Code)
	}

	// The API reads only: unlike the CLI it saved no audit snapshot of its own.
	head := f.git("rev-parse", "HEAD")
	if err := os.Remove(check.SnapshotPath(filepath.Join(f.dir, "lattice"), head)); err != nil {
		t.Fatalf("the CLI saves the head snapshot: %v", err)
	}
	apiGet(srv, "/api/v2/check?base=main")
	if _, err := os.Stat(check.SnapshotPath(filepath.Join(f.dir, "lattice"), head)); err == nil {
		t.Error("GET /api/v2/check wrote a snapshot")
	}
}
