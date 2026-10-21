package blocks

import (
	"strings"
	"testing"
)

func TestBuildMarksVerifiedRoutesWired(t *testing.T) {
	src := inlineSrc("api", "be", "backend",
		CatNode{ID: "items", Parent: "root", Kind: "route", HTTP: &CatHTTP{Method: "POST", Path: "/cart/{cart}/items"}},
		CatNode{ID: "pay", Parent: "root", Kind: "route", HTTP: &CatHTTP{Method: "ANY", Path: "/pay/{cart}"}},
		CatNode{ID: "hook", Parent: "root", Kind: "route", HTTP: &CatHTTP{Method: "POST", Path: "/hooks/tax"}},
		CatNode{ID: "both", Parent: "root", Kind: "route", HTTP: &CatHTTP{Method: "GET|HEAD", Path: "/ping"}})
	r := Build(Inputs{Sources: []SourceInput{src}, Verified: []Verification{
		{Source: "code", Target: "api", Routes: []CodeRoute{
			{Method: "POST", Path: "/cart/{id}/items/", File: "routes/api.php", Line: 7},
			{Method: "GET", Path: "/pay/{cart}", File: "routes/web.php", Line: 3},
			{Method: "POST", Path: "/api/hooks/tax", File: "routes/api.php", Line: 9},
			{Method: "ANY", Path: "/ping", File: "routes/web.php", Line: 1}}},
		{Source: "code", Target: "nope"}}})
	bm := blockMap(r)
	for id, want := range map[string]string{"be:items": "code:routes/api.php:7", "be:pay": "code:routes/web.php:3",
		"be:both": "code:routes/web.php:1", "be:hook": ""} {
		b := bm[id]
		if got := b.Attrs["wired_by"]; got != want || (want != "") != (b.Truth == "wired") {
			t.Errorf("%s: truth %s wired_by %q, want %q", id, b.Truth, got, want)
		}
	}
	if !strings.Contains(strings.Join(r.Warnings, "\n"), `verifies "nope"`) {
		t.Errorf("warnings = %v", r.Warnings)
	}
}

func TestVerifyOptionalParamsAndHead(t *testing.T) {
	src := inlineSrc("api", "be", "backend",
		CatNode{ID: "opt", Parent: "root", Kind: "route", HTTP: &CatHTTP{Method: "POST", Path: "/hooks/pay/{store}"}},
		CatNode{ID: "bare", Parent: "root", Kind: "route", HTTP: &CatHTTP{Method: "POST", Path: "/hooks/pay"}},
		CatNode{ID: "head", Parent: "root", Kind: "route", HTTP: &CatHTTP{Method: "HEAD", Path: "/ping"}})
	r := Build(Inputs{Sources: []SourceInput{src}, Verified: []Verification{{Source: "code", Target: "api", Routes: []CodeRoute{
		{Method: "POST", Path: "/hooks/pay/{store?}", File: "r.php", Line: 1},
		{Method: "GET", Path: "/ping", File: "r.php", Line: 2}}}}})
	for _, id := range []string{"be:opt", "be:bare", "be:head"} {
		if b := blockMap(r)[id]; b.Truth != "wired" {
			t.Errorf("%s not wired: %+v", id, b.Attrs)
		}
	}
}
