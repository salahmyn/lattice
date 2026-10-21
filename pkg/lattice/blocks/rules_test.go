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
		{"fe:payment.gateway.*", "fe:payment.gateway.acme", true},
		{"fe:payment.gateway.*", "fe:payment.gateway.acme.otp", false},
		{"fe:**", "fe:payment.gateway.acme.otp", true},
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
			{ID: "a.x", Parent: "a", Kind: "decision", Title: "X", When: []string{"store.currency = XYZ and device.ios", "unknown.flag"}},
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
			Events: []string{"app::loc.resolved", "app::reload", "shop::address.changed", "internal"}},
	}}
	r := Build(Inputs{
		Judgments: []Judgment{
			{ID: "land", Type: "system", Entries: []JudgmentEntry{{ID: "app_shell", Title: "App"}, {ID: "store", Title: "Store"}}},
			{ID: "ev", Type: "edge", Entries: []JudgmentEntry{
				{Kind: "native_bridge", From: "app_shell", Events: []string{"app::loc.resolved"}},
				{Kind: "native_bridge", To: "app_shell", Events: []string{"app::*"}},
				{Kind: "public_event", To: "store", Events: []string{"*::*"}, Blocks: "fe:**"},
			}},
		},
		Sources: []SourceInput{{Info: SourceInfo{ID: "web", Prefix: "fe"}, Catalogues: []*Catalogue{fe}}},
	})
	e := edgeSet(r)
	want := map[string][]string{
		"app_shell -native_bridge-> fe:a.n": {"app::loc.resolved"},
		"fe:a.n -native_bridge-> app_shell": {"app::reload"},
		"fe:a.n -public_event-> store":      {"shop::address.changed"},
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

func TestRuleEntryChecks(t *testing.T) {
	for name, e := range map[string]JudgmentEntry{
		"events and config": {Kind: "k", To: "b", Events: []string{"e"}, ConfigKind: "c"},
		"two config forms":  {Kind: "k", From: "a", ConfigKind: "c", ConfigSource: "db"},
		"scopes alone":      {Kind: "k", From: "a", To: "b", Scopes: []string{"x"}},
		"pattern rule end":  {Kind: "k", From: "fe:*", ConfigKind: "c"},
	} {
		if err := (Judgment{ID: "x", Type: "edge", Entries: []JudgmentEntry{e}}).check(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestPlainExcludeResolvesAliases(t *testing.T) {
	in := shopInputs(t)
	in.Judgments = append(in.Judgments,
		Judgment{ID: "al", Type: "alias", Entries: []JudgmentEntry{{Prefix: "fe:"}}},
		Judgment{ID: "ext", Type: "external", Entries: []JudgmentEntry{{ID: "ext.x", Title: "X"}}},
		Judgment{ID: "fan", Type: "edge", Entries: []JudgmentEntry{{From: "fe:cart.*", To: "ext.x", Kind: "uses", Exclude: []string{"cart.items"}}}},
	)
	if _, ok := edgeSet(Build(in))["fe:cart.items -uses-> ext.x"]; ok {
		t.Error("a plain exclude should resolve through aliases")
	}
}

func TestTopicLongestPrefix(t *testing.T) {
	opt := &EndpointGrouping{PathPrefixes: map[string]string{"/api/": "api-", "/api/v2/": "v2-"}}
	if got := topicOf("/api/v2/orders", opt); got != "v2-api" {
		t.Errorf("topicOf = %q", got)
	}
}
