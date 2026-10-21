package blocks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeJudgment(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadJudgments(t *testing.T) {
	dir := t.TempDir()
	writeJudgment(t, dir, "b-groups.yaml", `id: lanes
type: group
rationale: how providers are reached
author: a@example.com
entries:
  - {id: pay.lanes.card, title: Card gateways, parent: pay-providers, members: [ext.visa]}
`)
	writeJudgment(t, dir, "a-landscape.yaml", `id: landscape
type: system
entries:
  - {id: shop-web, title: Shop web app, technology: React}
`)
	js, err := LoadJudgments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(js) != 2 || js[0].ID != "landscape" || js[1].Entries[0].Members[0] != "ext.visa" || js[1].Rationale == "" {
		t.Fatalf("judgments = %+v", js)
	}
	if js, err := LoadJudgments(filepath.Join(dir, "absent")); err != nil || len(js) != 0 {
		t.Errorf("missing dir: %v %v", js, err)
	}
}

func TestLoadJudgmentsRejects(t *testing.T) {
	cases := map[string]string{
		"exactly one of from and to": "id: x\ntype: edge\nentries: [{kind: k, from: a, to: b, events: [e]}]\n",
		"bad event pattern":          "id: x\ntype: edge\nentries: [{kind: k, to: b, events: [\"[\"]}]\n",
		"no id":                      "type: system\nentries: [{id: a}]\n",
		"unknown type":               "id: x\ntype: colour\nentries: [{id: a}]\n",
		"no entries":                 "id: x\ntype: system\n",
		"memebers":                   "id: x\ntype: group\nentries: [{id: a, memebers: [b]}]\n",
		"entry 0 needs id":           "id: x\ntype: group\nentries: [{title: a}]\n",
		"entry 1 needs from":         "id: x\ntype: edge\nentries: [{from: a, to: b, kind: k}, {to: b, kind: k}]\n",
		"entry 0 needs to":           "id: x\ntype: edge\nentries: [{from: a, kind: k}]\n",
		"entry 0 needs kind":         "id: x\ntype: edge\nentries: [{from: a, to: b}]\n",
		"entry 0 needs title":        "id: x\ntype: label\nentries: [{id: a}]\n",
		"entry 0 needs prefix":       "id: x\ntype: alias\nentries: [{id: a}]\n",
	}
	for want, body := range cases {
		dir := t.TempDir()
		writeJudgment(t, dir, "j.yaml", body)
		if _, err := LoadJudgments(dir); err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "j.yaml") {
			t.Errorf("%s: err = %v", want, err)
		}
	}
	dir := t.TempDir()
	writeJudgment(t, dir, "a.yaml", "id: x\ntype: system\nentries: [{id: a}]\n")
	writeJudgment(t, dir, "b.yaml", "id: x\ntype: label\nentries: [{id: a, title: A}]\n")
	if _, err := LoadJudgments(dir); err == nil || !strings.Contains(err.Error(), "also used") {
		t.Errorf("duplicate id: err = %v", err)
	}
}

func TestJudgmentsApplyInBuild(t *testing.T) {
	in := shopInputs(t)
	in.Judgments = append(in.Judgments,
		Judgment{ID: "providers", Type: "external", Entries: []JudgmentEntry{
			{ID: "pay-providers", Title: "Payment providers", Kind: "external_group"},
			{ID: "ext.visa", Title: "Visa", Parent: "pay-providers"}}},
		Judgment{ID: "lanes", Type: "group", Entries: []JudgmentEntry{
			{ID: "pay.lanes.card", Title: "Card gateways", Parent: "pay-providers", Members: []string{"ext.visa", "ext.nope"}}}},
		Judgment{ID: "calls", Type: "edge", Entries: []JudgmentEntry{
			{From: "be:orders.create", To: "ext.visa", Kind: "server_api", Label: "charge"},
			{From: "be:orders.create", To: "ext.gone", Kind: "server_api"}}},
		Judgment{ID: "names", Type: "label", Entries: []JudgmentEntry{{ID: "fe:cart", Title: "Basket"}}},
	)
	r := Build(in)
	b := blockMap(r)
	if b["ext.visa"].Parent != "pay.lanes.card" || b["pay.lanes.card"].Kind != "group" || b["pay-providers"].Kind != "external_group" {
		t.Errorf("group/external: visa=%+v lane=%+v", b["ext.visa"], b["pay.lanes.card"])
	}
	if b["ext.visa"].Provenance.Source != "judgment:providers" || b["fe:cart"].Title != "Basket" {
		t.Errorf("provenance/label: %+v %+v", b["ext.visa"].Provenance, b["fe:cart"])
	}
	if labels := edgeSet(r)["be:orders.create -server_api-> ext.visa"]; len(labels) != 1 || labels[0] != "charge" {
		t.Errorf("judgment edge labels = %v", labels)
	}
	w := strings.Join(r.Warnings, "\n")
	for _, want := range []string{`member "ext.nope"`, `"ext.gone"`} {
		if !strings.Contains(w, want) {
			t.Errorf("warnings missing %s: %v", want, r.Warnings)
		}
	}
}

func TestResolveID(t *testing.T) {
	js := []Judgment{{ID: "ids", Type: "alias", Entries: []JudgmentEntry{{Prefix: "shop.cart."}}}}
	known := func(id string) bool { return id == "shop.cart.items" || id == "items2" }
	if got, ok := ResolveID("items", js, known); !ok || got != "shop.cart.items" {
		t.Errorf("short id = %q %v", got, ok)
	}
	if got, ok := ResolveID("items2", js, known); !ok || got != "items2" {
		t.Errorf("exact id = %q %v", got, ok)
	}
	if _, ok := ResolveID("nothing", js, known); ok {
		t.Error("unknown id resolved")
	}
}

func TestGroupReparentsCatalogueBlockAndWarnsOnCollision(t *testing.T) {
	in := shopInputs(t)
	in.Judgments = append(in.Judgments, Judgment{ID: "g", Type: "group", Entries: []JudgmentEntry{
		{ID: "lane", Title: "Lane", Members: []string{"fe:cart.items", "lane"}},
		{ID: "fe:pay", Title: "Pay again"}}})
	r := Build(in)
	if p := blockMap(r)["fe:cart.items"].Parent; p != "lane" {
		t.Errorf("parent = %q", p)
	}
	w := strings.Join(r.Warnings, "\n")
	for _, want := range []string{"fe:pay replaces an existing block", "group lane lists itself as a member"} {
		if !strings.Contains(w, want) {
			t.Errorf("warnings missing %q: %v", want, r.Warnings)
		}
	}
}

func TestAliasResolvesShortIDsInLabelAndEdge(t *testing.T) {
	in := shopInputs(t)
	in.Judgments = append(in.Judgments,
		Judgment{ID: "ids", Type: "alias", Entries: []JudgmentEntry{{Prefix: "fe:"}}},
		Judgment{ID: "names", Type: "label", Entries: []JudgmentEntry{{ID: "cart.items", Title: "Line items"}}},
		Judgment{ID: "calls", Type: "edge", Entries: []JudgmentEntry{{From: "be:orders.create", To: "cart", Kind: "server_api"}}},
	)
	r := Build(in)
	if got := blockMap(r)["fe:cart.items"].Title; got != "Line items" {
		t.Errorf("label via alias: title = %q", got)
	}
	if _, ok := edgeSet(r)["be:orders.create -server_api-> fe:cart"]; !ok {
		t.Errorf("edge via alias missing: %v", edgeSet(r))
	}
}
