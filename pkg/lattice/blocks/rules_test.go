package blocks

import (
	"strings"
	"testing"
)

func TestCompileID(t *testing.T) {
	cases := []struct {
		pattern, id string
		want        bool
	}{
		{"fe:payment.gateway.*", "fe:payment.gateway.tap", true},
		{"fe:payment.gateway.*", "fe:payment.gateway.tap.otp", false},
		{"fe:**", "fe:payment.gateway.tap.otp", true},
		{"fe:**", "be:orders", false},
		{"fe:cart.?tems", "fe:cart.items", true},
		{"a+b.*", "a+b.c", true}, // regexp metacharacters are literal
	}
	for _, c := range cases {
		if got := compileID(c.pattern).MatchString(c.id); got != c.want {
			t.Errorf("%s ~ %s = %v want %v", c.pattern, c.id, got, c.want)
		}
	}
	if compileID("") != nil {
		t.Error("an empty pattern should compile to nil")
	}
}

func TestEdgeJudgmentPatterns(t *testing.T) {
	in := shopInputs(t)
	in.Judgments = append(in.Judgments,
		Judgment{ID: "ext", Type: "external", Entries: []JudgmentEntry{{ID: "ext.x", Title: "X"}}},
		Judgment{ID: "fan", Type: "edge", Entries: []JudgmentEntry{
			{From: "fe:cart.*", To: "ext.x", Kind: "uses", Exclude: []string{"fe:cart.checkout"}},
			{From: "fe:nothing.*", To: "ext.x", Kind: "uses"},
		}},
	)
	r := Build(in)
	e := edgeSet(r)
	if _, ok := e["fe:cart.items -uses-> ext.x"]; !ok {
		t.Errorf("pattern edge missing: %v", e)
	}
	for k := range e {
		if strings.HasPrefix(k, "fe:cart.checkout") && strings.HasSuffix(k, "-uses-> ext.x") {
			t.Errorf("excluded or nested block got an edge: %s", k)
		}
	}
	if !strings.Contains(strings.Join(r.Warnings, "\n"), `"fe:nothing.*" matches no block`) {
		t.Errorf("warnings = %v", r.Warnings)
	}
}

func configInputs() Inputs {
	fe := &Catalogue{Area: "a", Path: "a.json",
		Configs: []CatConfig{{ID: "store.currency", Kind: "storeSetting"}, {ID: "device.ios", Kind: "device"}},
		Nodes: []CatNode{
			{ID: "a", Kind: "screen", Title: "A"},
			{ID: "a.x", Parent: "a", Kind: "decision", Title: "X", When: []string{"store.currency = SAR and device.ios", "unknown.flag"}},
		}}
	be := &Catalogue{Area: "b", Path: "b.json", Nodes: []CatNode{
		{ID: "b", Kind: "service", Title: "B"},
		{ID: "b.y", Parent: "b", Kind: "config", Title: "Y", ConfigSource: &CatConfigSource{Type: "db", Scope: "store"}},
		{ID: "b.z", Parent: "b", Kind: "config", Title: "Z", ConfigSource: &CatConfigSource{Type: "db", Scope: "global"}},
		{ID: "b.w", Parent: "b", Kind: "config", Title: "W", ConfigSource: &CatConfigSource{Type: "db"}},
	}}
	return Inputs{
		Judgments: []Judgment{
			{ID: "land", Type: "system", Entries: []JudgmentEntry{{ID: "dashboard", Title: "Dashboard"}, {ID: "shopper", Title: "Shopper", Kind: "actor"}}},
			{ID: "cfg", Type: "edge", Entries: []JudgmentEntry{
				{Kind: "config", From: "dashboard", ConfigKind: "storeSetting", Blocks: "fe:**"},
				{Kind: "config", From: "shopper", ConfigKind: "device", Blocks: "fe:**"},
				{Kind: "config", From: "shopper", ConfigKind: "device", Blocks: "be:**"},
				{Kind: "config", From: "dashboard", ConfigSource: "db", Scopes: []string{"", "store"}, Label: "store setting", Blocks: "be:**"},
			}},
		},
		Sources: []SourceInput{
			{Info: SourceInfo{ID: "web", Prefix: "fe"}, Catalogues: []*Catalogue{fe}},
			{Info: SourceInfo{ID: "api", Prefix: "be"}, Catalogues: []*Catalogue{be}},
		},
	}
}

func TestConfigEdges(t *testing.T) {
	e := edgeSet(Build(configInputs()))
	want := map[string][]string{
		"dashboard -config-> fe:a.x": {"store.currency"},
		"shopper -config-> fe:a.x":   {"device.ios"},
		"dashboard -config-> be:b.y": {"store setting"},
		"dashboard -config-> be:b.w": {"store setting"},
	}
	for k, labels := range want {
		got, ok := e[k]
		if !ok || len(got) != 1 || got[0] != labels[0] {
			t.Errorf("%s = %v want %v", k, got, labels)
		}
	}
	if _, ok := e["dashboard -config-> be:b.z"]; ok {
		t.Error("a global-scope config must not get a store-setting edge")
	}
	if len(e) != len(want) {
		t.Errorf("edges = %v", e)
	}
}

func TestConfigRefs(t *testing.T) {
	got := configRefs([]string{"a.b = 1 and c.d", "e.f != x or g.h>=2", "  i.j  "})
	want := []string{"a.b", "c.d", "e.f", "g.h", "i.j"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("configRefs = %v want %v", got, want)
	}
}

func TestEventEdges(t *testing.T) {
	fe := &Catalogue{Area: "a", Path: "a.json", Nodes: []CatNode{
		{ID: "a", Kind: "screen", Title: "A"},
		{ID: "a.n", Parent: "a", Kind: "action", Title: "N",
			Events: []string{"mobile::loc.resolved", "mobile::reload", "checkout::address.changed", "internal"}},
	}}
	r := Build(Inputs{
		Judgments: []Judgment{
			{ID: "land", Type: "system", Entries: []JudgmentEntry{{ID: "mobile_app", Title: "App"}, {ID: "storefront", Title: "Store"}}},
			{ID: "ev", Type: "edge", Entries: []JudgmentEntry{
				{Kind: "native_bridge", From: "mobile_app", Events: []string{"mobile::loc.resolved"}},
				{Kind: "native_bridge", To: "mobile_app", Events: []string{"mobile::*"}},
				{Kind: "storefront_event", To: "storefront", Events: []string{"*::*"}, Blocks: "fe:**"},
			}},
		},
		Sources: []SourceInput{{Info: SourceInfo{ID: "web", Prefix: "fe"}, Catalogues: []*Catalogue{fe}}},
	})
	e := edgeSet(r)
	want := map[string][]string{
		"mobile_app -native_bridge-> fe:a.n":    {"mobile::loc.resolved"},
		"fe:a.n -native_bridge-> mobile_app":    {"mobile::reload"},
		"fe:a.n -storefront_event-> storefront": {"checkout::address.changed"},
	}
	if len(e) != len(want) {
		t.Fatalf("edges = %v want %v", e, want)
	}
	for k, l := range want {
		if got := e[k]; len(got) != 1 || got[0] != l[0] {
			t.Errorf("%s = %v want %v (first matching rule wins)", k, got, l)
		}
	}
}
