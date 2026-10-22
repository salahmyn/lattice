package blocks

import (
	"reflect"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func TestBusiness(t *testing.T) {
	bs := []schema.Block{
		{ID: "shopper", Kind: "actor", Title: "Shopper"},
		{ID: "web", Kind: "system", Title: "Web"},
		{ID: "api", Kind: "system", Title: "API"},
		{ID: "fe:cart", Kind: "area", Parent: "web", Title: "Cart", Tier: "frontend"},
		{ID: "fe:cart.total", Kind: "screen", Parent: "fe:cart", Tier: "frontend", Attrs: map[string]string{"status": "built", "catalogue_id": "cart.total", "tests_react": "a | b"}},
		{ID: "fe:cart.coupon", Kind: "action", Parent: "fe:cart", Tier: "frontend", Attrs: map[string]string{"status": "planned", "catalogue_id": "cart.coupon", "tests_vue": "c"}},
		{ID: "be:price", Kind: "route", Parent: "api", Tier: "backend", Attrs: map[string]string{"status": "built"}},
	}
	es := []schema.BlockEdge{{From: "shopper", To: "fe:cart.coupon", Kind: "config"}, {From: "fe:cart.total", To: "be:price", Kind: "api"},
		{From: "fe:cart.coupon", To: "be:price", Kind: "api"}}
	brds := []schema.BRD{
		{ID: "brd.coupon", Title: "Coupons", Blocks: []string{"fe:cart.total", "fe:cart.coupon", "fe:gone"}},
		{ID: "brd.sale", Title: "Sale", Blocks: []string{"fe:cart.total", "be:price"}},
	}
	v := Business(bs, es, brds)
	c, s := v.Features[0], v.Features[1]
	if c.Built != "mixed" || !reflect.DeepEqual(c.Tests, Coverage{"react": 2, "vue": 1}) || !reflect.DeepEqual(c.Areas, []string{"fe:cart"}) || !reflect.DeepEqual(c.Missing, []string{"fe:gone"}) {
		t.Errorf("coupon view = %+v", c)
	}
	if s.Built != "built" || !reflect.DeepEqual(s.Areas, []string{"be:price", "fe:cart"}) {
		t.Errorf("sale view = %+v", s)
	}
	if !reflect.DeepEqual(v.Shared, []SharedBlock{{Block: "fe:cart.total", Features: []string{"brd.coupon", "brd.sale"}}}) ||
		!reflect.DeepEqual(v.Conflicts, []BRDConflict{{A: "brd.coupon", B: "brd.sale", Blocks: []string{"fe:cart.total"}}}) {
		t.Errorf("shared %+v conflicts %+v", v.Shared, v.Conflicts)
	}
	if len(v.Actors) != 1 || v.Actors[0].Edges["config"] != 1 || v.Actors[0].Reaches != 1 {
		t.Errorf("actors = %+v", v.Actors)
	}
	if len(v.Systems) != 2 || v.Systems[0].ID != "web" || v.Systems[0].Blocks != 3 || v.Systems[0].Tiers["frontend"] != 3 {
		t.Errorf("systems = %+v", v.Systems)
	}
	if !reflect.DeepEqual(v.Seams, []Seam{{From: "web", To: "api", Kind: "api", N: 2}, {From: "shopper", To: "web", Kind: "config", N: 1}}) {
		t.Errorf("seams = %+v", v.Seams)
	}
}

func TestParseBRDAndGherkin(t *testing.T) {
	src := "id: brd.coupon\nversion: 1\nstatus: approved\ntitle: Coupons\nbusiness_problem: |-\n  In order to sell more,\n  as a shop\n" +
		"flags:\n  - {id: cart.total, meaning: the cart total}\nuser_scenarios:\n" +
		"  - {id: S1, narrative: A coupon lowers the total, given: [a valid coupon, a full cart], when: [the shopper applies it], then: [the total drops], blocks: [\"fe:cart.total\", \"fe:x\"]}\n" +
		"open_questions: [\"OPEN QUESTION: who pays?\"]\n"
	b, err := ParseBRD([]byte(src), "t")
	if err != nil {
		t.Fatal(err)
	}
	got := Gherkin(b, []schema.Block{{ID: "fe:cart.total", Attrs: map[string]string{"catalogue_id": "cart.total"}}})
	want := "Feature: Coupons\n\n  In order to sell more,\n  as a shop\n\n  # flag: cart.total  —  the cart total\n\n" +
		"  Scenario: A coupon lowers the total\n    Given a valid coupon\n    And a full cart\n    When the shopper applies it\n    Then the total drops\n" +
		"    # realised by: cart.total, fe:x\n\n  # OPEN QUESTION: who pays?\n"
	if got != want {
		t.Errorf("gherkin:\n%s\nwant:\n%s", got, want)
	}
	for name, bad := range map[string]string{
		"status":   strings.Replace(src, "approved", "done", 1),
		"no then":  strings.Replace(src, ", then: [the total drops]", "", 1),
		"unknown":  src + "colour: red\n",
		"no title": strings.Replace(src, "title: Coupons\n", "", 1),
	} {
		if _, err := ParseBRD([]byte(bad), "t"); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestBusinessReviewCases(t *testing.T) {
	bs := []schema.Block{{ID: "a", Kind: "screen", Attrs: map[string]string{"status": "built"}}, {ID: "b", Kind: "screen"}}
	v := Business(bs, nil, []schema.BRD{{ID: "x", Title: "X", Blocks: []string{"a", "a", "b"}}})
	if len(v.Shared) != 0 || len(v.Conflicts) != 0 || v.Features[0].Built != "mixed" {
		t.Errorf("repeated block or a block without status: %+v", v)
	}
	g := Gherkin(schema.BRD{Title: "Two\nlines", UserScenarios: []schema.UserScenario{{Narrative: "a\nb", Then: []string{"c\n d"}}}}, nil)
	if strings.Contains(g, "Two\nlines") || !strings.Contains(g, "Scenario: a b") || !strings.Contains(g, "Then c d") {
		t.Errorf("newlines leak into Gherkin:\n%s", g)
	}
}
