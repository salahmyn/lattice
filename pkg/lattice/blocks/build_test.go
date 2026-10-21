package blocks

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func shopInputs(t *testing.T) Inputs {
	t.Helper()
	fe, _, err := ParseAll([]File{readFixture(t, "frontend/cart.json"), readFixture(t, "frontend/pay.json")})
	if err != nil {
		t.Fatal(err)
	}
	be, _, err := ParseAll([]File{readFixture(t, "backend/orders.json")})
	if err != nil {
		t.Fatal(err)
	}
	return Inputs{
		Judgments: []Judgment{{ID: "landscape", Type: "system", Entries: []JudgmentEntry{
			{ID: "shop-web", Title: "Shop web app"}, {ID: "shop-api", Title: "Shop API"}}}},
		Sources: []SourceInput{
			{Info: SourceInfo{ID: "web", Prefix: "fe", Tier: "frontend", System: "shop-web", Commit: "abcdef1234"},
				Catalogues: fe, Stamp: schema.SourceStamp{ID: "web", Commit: "abcdef1234"}},
			{Info: SourceInfo{ID: "api", Prefix: "be", Tier: "backend", System: "shop-api"}, Catalogues: be,
				Stamp: schema.SourceStamp{ID: "api"}},
		},
	}
}

func blockMap(r Result) map[string]schema.Block {
	m := map[string]schema.Block{}
	for _, b := range r.Blocks {
		m[b.ID] = b
	}
	return m
}

func edgeSet(r Result) map[string][]string {
	m := map[string][]string{}
	for _, e := range r.Edges {
		m[e.From+" -"+e.Kind+"-> "+e.To] = e.Labels
	}
	return m
}

func TestBuildHierarchy(t *testing.T) {
	r := Build(shopInputs(t))
	b := blockMap(r)
	want := map[string]string{ // block -> parent
		"shop-web": "", "shop-api": "",
		"fe:cart": "shop-web", "fe:pay": "shop-web", "be:orders": "shop-api",
		"fe:cart.items": "fe:cart", "fe:cart.checkout": "fe:cart", "fe:cart.checkout.retry": "fe:cart.checkout",
		"fe:pay.card": "fe:pay", "fe:pay.lost": "fe:pay",
		"be:orders.create": "be:orders", "be:orders.create.save": "be:orders.create",
	}
	if len(b) != len(want) {
		t.Errorf("blocks = %d want %d: %v", len(b), len(want), b)
	}
	for id, parent := range want {
		got, ok := b[id]
		if !ok {
			t.Errorf("missing block %s", id)
			continue
		}
		if got.Parent != parent {
			t.Errorf("%s parent = %q want %q", id, got.Parent, parent)
		}
	}
	if b["fe:cart"].Description != "Cart root." || b["fe:cart"].Kind != "area" {
		t.Errorf("area block = %+v", b["fe:cart"])
	}
	cb := b["fe:cart.checkout"]
	if cb.Attrs["api"] != "POST /checkout" || cb.Attrs["tests_react"] != "web/cart.test.ts > checkout" ||
		cb.Truth != schema.TruthDeclared || cb.Provenance.Source != "web@abcdef1:frontend/cart.json" || cb.System != "shop-web" {
		t.Errorf("checkout block = %+v", cb)
	}
	if got := b["be:orders.create.save"].Attrs["config_source"]; got != "db · settings.orders" {
		t.Errorf("config_source = %q", got)
	}
}

func TestBuildEdges(t *testing.T) {
	r := Build(shopInputs(t))
	got := edgeSet(r)
	want := map[string][]string{
		"fe:cart.items -flow-> fe:cart.checkout":   {"continue"},
		"fe:cart.items -call-> fe:pay.card":        {"calls"},
		"fe:cart.checkout -api-> be:orders.create": {"POST /checkout"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("edges =\n%v\nwant\n%v", got, want) // the retry loop targets its own ancestor and is dropped
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	a, _ := json.Marshal(Build(shopInputs(t)))
	for i := 0; i < 5; i++ {
		b, _ := json.Marshal(Build(shopInputs(t)))
		if string(a) != string(b) {
			t.Fatal("Build output differs between runs")
		}
	}
}

func TestBuildWarnsOnUndeclaredSystem(t *testing.T) {
	in := shopInputs(t)
	in.Judgments = nil
	r := Build(in)
	if len(r.Warnings) == 0 || !strings.Contains(strings.Join(r.Warnings, "\n"), `system "shop-web"`) {
		t.Errorf("warnings = %v", r.Warnings)
	}
	if blockMap(r)["fe:cart"].Parent != "" {
		t.Error("an area under an undeclared system should be top level")
	}
}
