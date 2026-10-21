package blocks

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func proposalInputs(t *testing.T) Inputs {
	t.Helper()
	ev, err := ParseEvidence([]byte(`{"format": "evidence/v1", "id": "new-cart", "source": "web",
	  "classes": [{"id": "GAP", "title": "gap"}, {"id": "KEEP", "title": "stays"}, {"id": "NEW", "title": "replaced", "replaces": true}],
	  "package": {"name": "cart-kit", "version": "1.0.0", "commit": "0123456789", "dir": "src",
	    "components": [{"tag": "cart-box", "file": "box.tsx", "props": ["items", "total"]}],
	    "modules": [{"concern": "pricing", "modules": ["tax", "round"]}],
	    "paths": ["/cart/items?expand=1"]},
	  "verdicts": [{"block": "box", "class": "NEW"}, {"block": "box", "case": "empty", "class": "KEEP"},
	    {"block": "total", "class": "NEW"}, {"block": "total", "case": "tax", "class": "GAP"},
	    {"block": "gone", "class": "KEEP"}],
	  "gaps": [{"code": "G-TAX", "cites": ["total", "nowhere"], "owners": ["H", "K"], "summary": "tax is missing"},
	    {"code": "G-ROUND", "cites": ["box"], "low": true}],
	  "payload": [{"endpoint": "POST cart/items", "field": "qty", "severity": "breaking", "paths": ["cart/items"]},
	    {"endpoint": "POST cart/items", "field": "note", "severity": "cosmetic", "paths": ["cart/items"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return Inputs{
		Judgments: []Judgment{{ID: "short", Type: "alias", Entries: []JudgmentEntry{{Prefix: "fe:cart."}}}},
		Sources: []SourceInput{
			inlineSrc("web", "fe", "frontend", CatNode{ID: "box", Parent: "root", Kind: "screen"},
				CatNode{ID: "cart.total", Parent: "root", Kind: "screen"}),
			{Info: SourceInfo{ID: "api", Prefix: "be", Tier: "backend", RoutePrefixes: []string{"shop/{}/"}},
				Catalogues: []*Catalogue{{Area: "y", Nodes: []CatNode{{ID: "root"},
					{ID: "items", Parent: "root", Kind: "route", HTTP: &CatHTTP{Method: "POST", Path: "/shop/{shop}/cart/items"}},
					{ID: "item", Parent: "root", Kind: "route", HTTP: &CatHTTP{Method: "PUT", Path: "/shop/{shop}/cart/items/{item}"}},
					{ID: "other", Parent: "root", Kind: "route", HTTP: &CatHTTP{Method: "GET", Path: "/shop/{shop}/cart/itemsx"}}}}},
				Stamp: schema.SourceStamp{ID: "api"}},
		},
		Proposals: []Proposal{{Initiative: schema.Initiative{ID: "new-cart", Status: schema.InitiativePaused,
			Map: &schema.InitiativeMap{System: schema.MapSystem{Title: "Cart kit"}, HostBlock: "fe:box", Flag: "use_kit",
				Blocking: []string{"G-TAX"}, Externals: []schema.MapExternal{{To: "fe:x", Kind: "sdk", Label: "maps"}}}},
			Evidence: &ev}},
	}
}

func TestBuildDrawsProposal(t *testing.T) {
	r := Build(proposalInputs(t))
	bm := blockMap(r)
	kinds := map[string]string{}
	for id, b := range bm {
		if b.Initiative == "new-cart" {
			kinds[id] = b.Kind
		}
	}
	want := map[string]string{"init:new-cart": "system", "init:new-cart:components": "group",
		"init:new-cart:components:cart-box": "component", "init:new-cart:modules": "group", "init:new-cart:modules:pricing": "module",
		"init:new-cart:gaps": "group", "init:new-cart:gaps:G-TAX": "gap", "init:new-cart:gaps:G-ROUND": "gap",
		"init:new-cart:payload": "group", "init:new-cart:payload:0": "difference"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("proposal blocks = %v", kinds)
	}

	sys := bm["init:new-cart"].Attrs
	for k, v := range map[string]string{"technology": "proposed · cart-kit 1.0.0", "status": "paused", "flag": "use_kit",
		"verdicts": "GAP 1 · KEEP 2 · NEW 2", "gaps": "2", "blocking": "G-TAX", "payload": "1 breaking · 1 cosmetic", "unmapped": "gone"} {
		if sys[k] != v {
			t.Errorf("system attr %s = %q, want %q", k, sys[k], v)
		}
	}
	if a := bm["init:new-cart:gaps:G-TAX"].Attrs; a["severity"] != "blocking" || a["technology"] != "blocking · owner H/K" || a["unmapped_cites"] != "nowhere" {
		t.Errorf("G-TAX attrs = %v", a)
	}
	if bm["init:new-cart:gaps:G-ROUND"].Attrs["severity"] != "low" {
		t.Errorf("G-ROUND attrs = %v", bm["init:new-cart:gaps:G-ROUND"].Attrs)
	}

	box := bm["fe:box"].Overlays["new-cart"]
	if box.Verdict != "KEEP" || box.Title != "stays" || len(box.Cases) != 2 || !reflect.DeepEqual(box.Cases[0].Gaps, []string{"G-ROUND"}) {
		t.Errorf("box overlay = %+v", box)
	}
	if total := bm["fe:cart.total"].Overlays["new-cart"]; total.Verdict != "GAP" || total.Cases[1].Gaps[0] != "G-TAX" {
		t.Errorf("total overlay (resolved through the alias) = %+v", total)
	}

	es := edgeSet(r)
	for e, labels := range map[string][]string{
		"init:new-cart:gaps:G-TAX -gap-> fe:cart.total": {"blocking"},
		"init:new-cart:gaps:G-ROUND -gap-> fe:box":      {"low"},
		"init:new-cart:payload:0 -api-> be:items":       {"differs"},
		"init:new-cart:payload:0 -api-> be:item":        {"differs"},
		"init:new-cart -api-> be:items":                 {"calls cart/items"},
		"init:new-cart -sdk-> fe:x":                     {"maps"},
		"fe:box -uses-> init:new-cart":                  {"mounts the proposal (use_kit)"},
	} {
		if got, ok := es[e]; !ok || !reflect.DeepEqual(got, labels) {
			t.Errorf("edge %s = %v (present %v)", e, got, ok)
		}
	}
	for e := range es {
		if strings.Contains(e, "-replaces->") || strings.Contains(e, "be:other") {
			t.Errorf("unexpected edge %s (no block's top verdict replaces; itemsx is another route)", e)
		}
	}
	if len(r.Warnings) != 0 {
		t.Errorf("warnings = %v", r.Warnings)
	}
}

func TestBuildProposalReplacesAndIsDeterministic(t *testing.T) {
	in := proposalInputs(t)
	in.Proposals[0].Evidence.Verdicts = []EvidenceVerdict{{Block: "box", Class: "NEW"}}
	a, _ := json.Marshal(Build(in))
	b, _ := json.Marshal(Build(in))
	if string(a) != string(b) {
		t.Fatal("two builds differ")
	}
	r := Build(in)
	if got := edgeSet(r)["init:new-cart -replaces-> fe:box"]; !reflect.DeepEqual(got, []string{"replaced"}) {
		t.Errorf("replaces edge = %v", got)
	}
}

func TestBuildWarnsOnEvidenceWithoutInitiative(t *testing.T) {
	in := proposalInputs(t)
	in.Proposals = []Proposal{{Evidence: in.Proposals[0].Evidence}}
	r := Build(in)
	if !strings.Contains(strings.Join(r.Warnings, "\n"), "evidence new-cart has no initiative") {
		t.Errorf("warnings = %v", r.Warnings)
	}
	for _, b := range r.Blocks {
		if b.Initiative != "" || len(b.Overlays) > 0 {
			t.Errorf("drew %s without an initiative", b.ID)
		}
	}
}
