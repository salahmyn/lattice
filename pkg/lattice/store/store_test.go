package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

func filesStore(t *testing.T) Store {
	root := t.TempDir()
	dir := filepath.Join(root, "lattice")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"config.yaml": "version: 1\n", "workspace.yaml": "mode: standalone\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return OpenFiles(ws)
}

// backends are the stores every contract test runs against. Postgres runs when
// LATTICE_TEST_PG_DSN is set; each test gets its own schema, dropped after.
func backends(t *testing.T) map[string]func(*testing.T) Store {
	b := map[string]func(*testing.T) Store{"files": filesStore}
	dsn := os.Getenv("LATTICE_TEST_PG_DSN")
	if dsn == "" {
		t.Log("LATTICE_TEST_PG_DSN not set: postgres contract skipped")
		return b
	}
	b["postgres"] = func(t *testing.T) Store {
		ctx := context.Background()
		sch := fmt.Sprintf("lattice_test_%d", time.Now().UnixNano())
		p, err := OpenPostgres(ctx, dsn, sch)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := p.c.Exec(ctx, "DROP SCHEMA "+sch+" CASCADE"); err != nil {
				t.Errorf("drop %s: %v", sch, err)
			}
			_ = p.Close()
		})
		return p
	}
	return b
}

func TestContract(t *testing.T) {
	for name, open := range backends(t) {
		t.Run(name+"/records", func(t *testing.T) { recordsContract(t, open(t)) })
		t.Run(name+"/graph", func(t *testing.T) { graphContract(t, open(t)) })
	}
}

func judgmentBody(id, title string) []byte {
	return []byte("id: " + id + "\ntype: label\nauthor: dev@acme.test\nentries: [{id: x, title: " + title + "}]\n")
}

func recordsContract(t *testing.T, st Store) {
	ctx := context.Background()
	a1, err := st.Put(ctx, Record{Kind: "judgment", ID: "a", Body: judgmentBody("a", "One")})
	if err != nil || a1.Version == "" || a1.Author != "dev@acme.test" {
		t.Fatalf("put new = %+v, %v", a1, err)
	}
	var stale *StaleError
	if _, err := st.Put(ctx, Record{Kind: "judgment", ID: "a", Body: judgmentBody("a", "Two")}); !errors.As(err, &stale) || stale.Current != a1.Version {
		t.Errorf("create over an existing record = %v; want stale at %s", err, a1.Version)
	}
	a2, err := st.Put(ctx, Record{Kind: "judgment", ID: "a", Version: a1.Version, Body: judgmentBody("a", "Two")})
	if err != nil || a2.Version == a1.Version {
		t.Fatalf("update = %+v, %v", a2, err)
	}
	if _, err := st.Put(ctx, Record{Kind: "judgment", ID: "a", Version: a1.Version, Body: judgmentBody("a", "Three")}); !errors.As(err, &stale) || stale.Current != a2.Version {
		t.Errorf("stale update = %v; want stale at %s", err, a2.Version)
	}
	if _, err := st.Put(ctx, Record{Kind: "judgment", ID: "new", Version: "7", Body: judgmentBody("new", "N")}); !errors.As(err, &stale) || stale.Current != "" {
		t.Errorf("update of a missing record = %v; want stale with no current version", err)
	}
	got, err := st.Get(ctx, "judgment", "a")
	if err != nil || !strings.Contains(string(got.Body), "Two") || got.Version != a2.Version {
		t.Errorf("get = %+v, %v", got, err)
	}
	if _, err := st.Put(ctx, Record{Kind: "judgment", ID: "b", Body: judgmentBody("b", "B")}); err != nil {
		t.Fatal(err)
	}
	list, err := st.List(ctx, "judgment")
	if err != nil || len(list) != 2 || list[0].ID != "a" || list[1].ID != "b" {
		t.Errorf("list = %+v, %v", list, err)
	}
	if _, err := st.Get(ctx, "judgment", "zzz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("get missing = %v", err)
	}
	for _, bad := range []Record{
		{Kind: "judgment", ID: "c", Body: judgmentBody("other", "X")}, // body id differs
		{Kind: "judgment", ID: "../c", Body: judgmentBody("../c", "X")},
		{Kind: "../x", ID: "c", Body: judgmentBody("c", "X")},
	} {
		if _, err := st.Put(ctx, bad); err == nil {
			t.Errorf("put %s/%s: want an error", bad.Kind, bad.ID)
		}
	}
}

func sampleGraph() Graph {
	return Graph{
		Blocks: []schema.Block{
			{ID: "be:orders", Kind: "service", Title: "Orders", System: "shop-api", Tier: "backend"},
			{ID: "fe:cart", Kind: "area", Title: "Cart", Description: "The shopping cart", System: "shop-web"},
			{ID: "fe:cart.items", Kind: "screen", Title: "Cart items", System: "shop-web"},
			{ID: "n:a", Kind: "action", Title: "A"}, {ID: "n:b", Kind: "action", Title: "B"},
			{ID: "n:c", Kind: "action", Title: "C"}, {ID: "n:d", Kind: "action", Title: "D"},
			{ID: "n:x", Kind: "action", Title: "X"},
		},
		Edges: []schema.BlockEdge{
			{From: "n:a", To: "n:b", Kind: "flow"}, {From: "n:b", To: "n:c", Kind: "flow"},
			{From: "n:c", To: "n:d", Kind: "flow"}, {From: "n:x", To: "n:a", Kind: "api", Labels: []string{"POST /x"}},
		},
		Sources: []schema.SourceStamp{{ID: "web", Commit: "abc", State: schema.FreshCurrent}},
		Flows:   []schema.Flow{{ID: "f", End: "ends", Steps: []schema.FlowMove{{To: "n:a", Kind: "start"}}}},
	}
}

func asJSON(t *testing.T, v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func ids(bs []schema.Block) string {
	var out []string
	for _, b := range bs {
		out = append(out, b.ID)
	}
	return strings.Join(out, ",")
}

func edges(es []schema.BlockEdge) string {
	var out []string
	for _, e := range es {
		out = append(out, e.From+">"+e.To)
	}
	return strings.Join(out, ",")
}

func graphContract(t *testing.T, st Store) {
	ctx := context.Background()
	if g, err := st.Graph(ctx); err != nil || len(g.Blocks) != 0 {
		t.Fatalf("empty graph = %+v, %v", g, err)
	}
	g := sampleGraph()
	if err := st.ReplaceGraph(ctx, g); err != nil {
		t.Fatal(err)
	}
	got, err := st.Graph(ctx)
	if err != nil || asJSON(t, got) != asJSON(t, g) {
		t.Fatalf("graph = %s, %v\nwant %s", asJSON(t, got), err, asJSON(t, g))
	}
	for q, want := range map[string]string{"cart": "fe:cart,fe:cart.items", "CART items": "fe:cart.items",
		"shop": "fe:cart", "": "", "nothing": "", "fe:cart.it": "fe:cart.items"} {
		bs, err := st.Search(ctx, q, Filter{})
		if err != nil || ids(bs) != want {
			t.Errorf("search %q = %s, %v; want %s", q, ids(bs), err, want)
		}
	}
	if bs, _ := st.Search(ctx, "orders cart", Filter{}); len(bs) != 0 {
		t.Errorf("every term must match: %s", ids(bs))
	}
	if bs, _ := st.Search(ctx, "c", Filter{System: "shop-web", Kind: "screen"}); ids(bs) != "fe:cart.items" {
		t.Errorf("filtered search = %s", ids(bs))
	}
	if bs, _ := st.Search(ctx, "a", Filter{Limit: 1}); len(bs) != 1 {
		t.Errorf("limit = %s", ids(bs))
	}
	for _, c := range []struct {
		dir   Direction
		kinds []string
		depth int
		want  string
	}{
		{Out, nil, 1, "n:a>n:b"},
		{Out, nil, 2, "n:a>n:b,n:b>n:c"},
		{In, nil, 1, "n:x>n:a"},
		{Both, nil, 1, "n:a>n:b,n:x>n:a"},
		{Both, []string{"api"}, 3, "n:x>n:a"},
		{Out, nil, 0, "n:a>n:b"}, // depth below 1 is 1
	} {
		es, err := st.Relations(ctx, "n:a", c.dir, c.kinds, c.depth)
		if err != nil || edges(es) != c.want {
			t.Errorf("relations %s %v %d = %s, %v; want %s", c.dir, c.kinds, c.depth, edges(es), err, c.want)
		}
	}
	cyc := Graph{Blocks: []schema.Block{{ID: "n:a"}, {ID: "n:b"}}, Edges: []schema.BlockEdge{
		{From: "n:a", To: "n:a", Kind: "flow"}, {From: "n:a", To: "n:b", Kind: "flow"}, {From: "n:b", To: "n:a", Kind: "flow"}}}
	if err := st.ReplaceGraph(ctx, cyc); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []Direction{Out, In, Both} {
		if es, err := st.Relations(ctx, "n:a", dir, nil, 12); err != nil || edges(es) != "n:a>n:a,n:a>n:b,n:b>n:a" {
			t.Errorf("cycle %s = %s, %v", dir, edges(es), err)
		}
	}
	small := Graph{Blocks: []schema.Block{{ID: "n:z", Kind: "action", Title: "Z"}}}
	if err := st.ReplaceGraph(ctx, small); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Graph(ctx); asJSON(t, got) != asJSON(t, small) {
		t.Errorf("replace must not merge: %s", asJSON(t, got))
	}
}

// @verifies store:INV-1
func TestFilesPutKeepsAnotherRecordsFile(t *testing.T) {
	st := filesStore(t).(*Files)
	dir := st.dir("judgment")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "010-sys.yaml"), judgmentBody("sys", "S"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Put(context.Background(), Record{Kind: "judgment", ID: "010-sys", Body: judgmentBody("010-sys", "X")}); err == nil {
		t.Error("a new record must not overwrite the file of another")
	}
	if r, err := st.Get(context.Background(), "judgment", "sys"); err != nil || !strings.Contains(string(r.Body), "title: S") {
		t.Errorf("sys = %+v, %v", r, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte("id: [oops\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.List(context.Background(), "judgment"); err == nil || !strings.Contains(err.Error(), "bad.yaml") || strings.Contains(err.Error(), "no top-level id") {
		t.Errorf("a YAML error should be reported as such: %v", err)
	}
}

// @verifies store:INV-2
func TestPostgresParseErrorHidesTheDSN(t *testing.T) {
	_, err := OpenPostgres(context.Background(), "postgres://u@h/db?password=s3cret&sslmode=bogus", "lattice")
	if err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("err = %v", err)
	}
}
