package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/slice"
	"github.com/salahmyn/lattice/pkg/lattice/ui"
	"github.com/salahmyn/lattice/pkg/lattice/workspace"
)

const sliceFeature = `id: orders
version: 1
status: production
purpose: |
  Lists and updates orders.
owners:
  business: nobody
  engineering: nobody
invariants:
  - id: INV-1
    statement: A customer reads only their own orders.
  - id: INV-2
    statement: A run is checked before it is applied.
  - id: INV-3
    statement: An archived order is never listed.
`

// sliceFixture is a workspace with three promises: INV-1 is kept by a SQL
// policy on a table a client module reads, INV-2 by a method only an
// ambiguous call can reach, INV-3 by a function nothing calls. The client
// module is run from the entry file index.html names, and web/** is the ui
// layer by a declared rule. The entry module also loads lib/steps, so a
// by-name member call may name its methods.
func sliceFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"lattice/config.yaml":          "{}\n",
		"lattice/adapters.yaml":        "adapters:\n  typescript:\n    enabled: true\n  postgres:\n    enabled: true\n",
		"lattice/workspace.yaml":       "mode: embedded\nlayers:\n  - glob: \"web/**\"\n    layer: ui\n",
		"lattice/features/orders.yaml": sliceFeature,
		"index.html":                   "<!doctype html>\n<script type=\"module\" src=\"/web/main.ts\"></script>\n",
		"web/main.ts":                  "/** @module-feature orders */\n\nimport { loadOrders } from './orders';\nimport '../lib/steps';\n\ndeclare const client: any;\nloadOrders(client, client);\n",
		"web/orders.ts": "/** @module-feature orders */\n\nexport async function loadOrders(supabase: any, step: any) {\n" +
			"  step.run();\n  return supabase.from('orders').select('*');\n}\n",
		"db/migrations/0001_orders.sql": "-- @module-feature orders\n\n-- @enforces orders:INV-1\ncreate policy owner_reads on public.orders for select using (user_id = auth.uid());\n",
		"lib/steps.ts": "/** @module-feature orders */\n\nexport class Check {\n  /**\n   * @enforces orders:INV-2\n   */\n  run(): void {}\n}\n\nexport class Apply {\n  run(): void {}\n}\n\n" +
			"/**\n * @enforces orders:INV-3\n */\nexport function dropArchived(rows: any[]): any[] {\n  return rows;\n}\n",
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func sliceJSON(t *testing.T, dir, id string) slice.Record {
	t.Helper()
	out, err := run(t, dir, newSliceCommand, id)
	if err != nil {
		t.Fatalf("slice %s: %v\n%s", id, err, out)
	}
	var rec slice.Record
	if err := json.Unmarshal([]byte(out), &rec); err != nil {
		t.Fatalf("slice output: %v\n%s", err, out)
	}
	return rec
}

// The slice of a promise a SQL policy keeps: the client module that reads the
// table is a root's path, its hops include the table hop, the path is carried
// by ui then data, and the slice reads complete. A promise reached only
// through an ambiguous call reads unknown_from_hop, and one whose enforcer
// nothing reaches reads unreached.
// @verifies reachability:INV-10
func TestSliceCommand(t *testing.T) {
	dir := sliceFixture(t)

	rec := sliceJSON(t, dir, "orders:INV-1")
	if rec.Contract != "lattice.slice/1" || rec.Reading != slice.ReadingComplete {
		t.Fatalf("INV-1: contract %q reading %q: %s", rec.Contract, rec.Reading, rec.Why)
	}
	var client *slice.Path
	for i, p := range rec.Paths {
		if p.Root.FQN == "web.main.<top>" || strings.HasPrefix(p.Root.Reason, "index.html") {
			client = &rec.Paths[i]
		}
	}
	if client == nil {
		t.Fatalf("no path from the entry file: %+v", rec.Paths)
	}
	if !reflect.DeepEqual(client.CarriedBy, []string{"ui", "data"}) {
		t.Errorf("carried by %v, want [ui data]", client.CarriedBy)
	}
	table := false
	for _, h := range client.Hops {
		if h.Kind == "table" && h.To == "sql.table.public.orders" && h.FromLayer == "ui" && h.ToLayer == "data" && h.Truth == slice.TruthResolved {
			table = true
		}
	}
	if !table || client.Enforcer != "sql.policy.public.orders.owner_reads" {
		t.Errorf("the path lacks the table hop to the policy: %+v", client)
	}

	rec = sliceJSON(t, dir, "orders:INV-2")
	if rec.Reading != slice.ReadingUnknownFromHop || rec.Hop != 2 || len(rec.Paths) == 0 || rec.Paths[0].Hops[1].Truth != slice.TruthUnknown {
		t.Errorf("INV-2: reading %q hop %d paths %+v", rec.Reading, rec.Hop, rec.Paths)
	}

	rec = sliceJSON(t, dir, "orders:INV-3")
	if rec.Reading != slice.ReadingUnreached || len(rec.Paths) != 0 || !reflect.DeepEqual(rec.Unreached, []string{"lib.steps.dropArchived"}) {
		t.Errorf("INV-3: reading %q unreached %v paths %+v", rec.Reading, rec.Unreached, rec.Paths)
	}
}

// The text output starts with the reading and prints one line per path with
// the layers; an unknown promise is a usage error.
func TestSliceCommandText(t *testing.T) {
	dir := sliceFixture(t)
	var text bytes.Buffer
	io := &IO{Repo: dir, Out: &text, Err: &text}
	cmd := newSliceCommand(io)
	cmd.SetArgs([]string{"orders:INV-1"})
	cmd.SetOut(&text)
	cmd.SetErr(&text)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("%v\n%s", err, text.String())
	}
	for _, want := range []string{"slice orders:INV-1\nreading: complete", "web.main.<top> [ui] -call-> web.orders.loadOrders [ui] -table-> sql.table.public.orders [data]", "carried by ui -> data;"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("text lacks %q:\n%s", want, text.String())
		}
	}
	if out, err := run(t, dir, newSliceCommand, "orders:INV-9"); err == nil || !strings.Contains(out, "SLICE_UNKNOWN_PROMISE") {
		t.Errorf("unknown promise: err %v\n%s", err, out)
	}
}

// GET /api/v2/promises/{id}/slice returns exactly what `lattice slice --json`
// prints, and the audit page draws the same slice: the reading, the layers a
// path is carried by and its chain.
// @verifies reachability:INV-10
func TestSliceAPIAndPage(t *testing.T) {
	dir := sliceFixture(t)
	ws, err := workspace.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	build := func(ctx context.Context) (schema.KnowledgeGraph, error) {
		return buildGraphWith(ctx, ws, buildOptions{readOnly: true})
	}
	srv := ui.New(ws, ui.Options{GraphBuilder: build, ReadOnlyGraphBuilder: build, SliceBuilder: sliceBuilder(ws)})

	for _, id := range []string{"orders:INV-1", "orders:INV-2", "orders:INV-3"} {
		cli, err := run(t, dir, newSliceCommand, id)
		if err != nil {
			t.Fatalf("%s: %v\n%s", id, err, cli)
		}
		if w := apiGet(srv, "/api/v2/promises/"+id+"/slice"); w.Code != 200 || w.Body.String() != cli {
			t.Errorf("%s: the API and the CLI differ (status %d)\n--- CLI\n%s\n--- API\n%s", id, w.Code, cli, w.Body.String())
		}
	}

	page := apiGet(srv, "/audit?promise=orders:INV-1").Body.String()
	for _, want := range []string{"Slice (which ways in reach", ">complete</span>", "web.main.&lt;top&gt; [ui] -call-&gt;", "-table-&gt; sql.table.public.orders [data]"} {
		if !strings.Contains(page, want) {
			t.Errorf("audit page lacks %q", want)
		}
	}
	if page := apiGet(srv, "/audit?promise=orders:INV-2").Body.String(); !strings.Contains(page, "unknown_from_hop") || !strings.Contains(page, "unknown hop") {
		t.Errorf("INV-2 on the page:\n%s", page)
	}
}
