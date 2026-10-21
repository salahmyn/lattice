package blocks

import (
	"strings"
	"testing"
)

const goodEvidence = `{"format": "evidence/v1", "id": "new-cart", "source": "web",
  "classes": [{"id": "GAP", "title": "gap"}, {"id": "NEW", "title": "replaced", "replaces": true}],
  "package": {"name": "cart-kit", "components": [{"tag": "cart-box"}], "modules": [{"concern": "pricing", "modules": ["tax"]}]},
  "verdicts": [{"block": "cart.box", "class": "NEW"}],
  "gaps": [{"code": "G-TAX", "cites": ["cart.total"]}],
  "payload": [{"endpoint": "POST cart/items", "field": "qty", "severity": "breaking", "paths": ["cart/items"]}]}`

func TestParseEvidence(t *testing.T) {
	ev, err := ParseEvidence([]byte(goodEvidence))
	if err != nil {
		t.Fatal(err)
	}
	if ev.ID != "new-cart" || len(ev.Classes) != 2 || ev.Package.Components[0].Tag != "cart-box" || ev.Payload[0].Paths[0] != "cart/items" {
		t.Fatalf("parsed %+v", ev)
	}
	bad := map[string]string{
		"unknown key":      strings.Replace(goodEvidence, `"source"`, `"sauce": "x", "source"`, 1),
		"null":             `null`,
		"trailing data":    goodEvidence + ` {}`,
		"wrong format":     strings.Replace(goodEvidence, `evidence/v1`, `evidence/v2`, 1),
		"undeclared class": strings.Replace(goodEvidence, `"class": "NEW"`, `"class": "KEEP"`, 1),
		"repeated class":   strings.Replace(goodEvidence, `{"id": "NEW"`, `{"id": "GAP"`, 1),
		"no source":        strings.Replace(goodEvidence, `"source": "web",`, ``, 1),
		"gap without code": strings.Replace(goodEvidence, `"code": "G-TAX", `, ``, 1),
	}
	for name, src := range bad {
		if _, err := ParseEvidence([]byte(src)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestParseInitiative(t *testing.T) {
	good := "id: new-cart\ntype: initiative\nstatus: paused\ntitle: New cart\npins:\n  - {role: kit, repo: acme/cart-kit, ref: main, commit: abc}\n" +
		"map:\n  system: {title: Cart kit}\n  host_block: \"web:cart\"\n  blocking: [G-TAX]\n  externals: [{to: \"ext:tax\", kind: sdk}]\n"
	in, err := ParseInitiative([]byte(good), "t")
	if err != nil {
		t.Fatal(err)
	}
	if in.Status != "paused" || in.Map.HostBlock != "web:cart" || in.Pins[0].Repo != "acme/cart-kit" {
		t.Fatalf("parsed %+v", in)
	}
	bad := map[string]string{
		"bad status":    strings.Replace(good, "paused", "parked", 1),
		"no id":         strings.Replace(good, "id: new-cart\n", "", 1),
		"unknown key":   good + "colour: red\n",
		"system title":  strings.Replace(good, "{title: Cart kit}", "{description: x}", 1),
		"external kind": strings.Replace(good, ", kind: sdk", "", 1),
	}
	for name, src := range bad {
		if _, err := ParseInitiative([]byte(src), "t"); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
