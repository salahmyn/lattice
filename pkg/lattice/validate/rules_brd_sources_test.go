package validate

import (
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// The wire codes, spelled out so the test pins them.
const (
	sourceInvalid      = "BRD_SOURCE_INVALID"
	sourceCheckInvalid = "BRD_SOURCE_CHECK_INVALID"
)

// sourcesBRD decodes a BRD with the given sources block, as extraction reads it.
func sourcesBRD(t *testing.T, sources string) schema.BRD {
	t.Helper()
	var b schema.BRD
	body := "id: brd.fees\nversion: 1\nstatus: draft\ntitle: Fees\nbusiness_problem: p\nimplements_via: [fees]\n" + sources
	if err := yaml.Unmarshal([]byte(body), &b); err != nil {
		t.Fatal(err)
	}
	return b
}

// sourceFindings returns code -> NextAction.Field for each source finding.
func sourceFindings(t *testing.T, sources string) map[string][]string {
	t.Helper()
	kg := schema.KnowledgeGraph{BRDs: []schema.BRD{sourcesBRD(t, sources)}, Features: []schema.Manifest{brdManifest("fees")}}
	kg.Features[0].ImplementsBRD = "brd.fees"
	out := map[string][]string{}
	for _, v := range Validate(kg, config.Default(), Options{}) {
		if v.Code == sourceInvalid || v.Code == sourceCheckInvalid {
			field := ""
			if v.NextAction != nil {
				field = v.NextAction.Field
			}
			out[v.Code] = append(out[v.Code], field)
		}
	}
	return out
}

// Every malformed source shape gives its code and names the field; a
// well-formed list gives none.
// @verifies brds:INV-11
func TestBRDSourceShapes(t *testing.T) {
	valid := `sources:
  - kind: ticket
    ref: WAL-12
    title: Withdrawal fees
    url: https://tracker.example/WAL-12
    checked_by: ana@example.com
    checked_at: 2026-11-10
  - kind: adr
    ref: ADR-7
  - {kind: roadmap, ref: R-1, checked_by: ana@example.com, checked_at: 2024-02-29}
`
	if got := sourceFindings(t, valid); len(got) != 0 {
		t.Fatalf("valid sources raised %v", got)
	}

	cases := []struct {
		name, sources, code, field string
	}{
		{"unknown kind", "sources: [{kind: wiki, ref: W-1}]", sourceInvalid, "sources[0].kind"},
		{"missing kind", "sources: [{ref: W-1}]", sourceInvalid, "sources[0].kind"},
		{"empty ref", "sources: [{kind: ticket, ref: ' '}]", sourceInvalid, "sources[0].ref"},
		{"relative url", "sources: [{kind: ticket, ref: T-1, url: /tickets/T-1}]", sourceInvalid, "sources[0].url"},
		{"non-http url", "sources: [{kind: ticket, ref: T-1, url: 'ftp://host/T-1'}]", sourceInvalid, "sources[0].url"},
		{"url without host", "sources: [{kind: ticket, ref: T-1, url: 'https:///T-1'}]", sourceInvalid, "sources[0].url"},
		{"checked_by alone", "sources: [{kind: ticket, ref: T-1, checked_by: ana@example.com}]", sourceCheckInvalid, "sources[0].checked_at"},
		{"checked_at alone", "sources: [{kind: ticket, ref: T-1, checked_at: 2026-11-10}]", sourceCheckInvalid, "sources[0].checked_by"},
		{"checked_at not a date", "sources: [{kind: ticket, ref: T-1, checked_by: ana, checked_at: last week}]", sourceCheckInvalid, "sources[0].checked_at"},
		{"checked_at in the future", "sources: [{kind: adr, ref: A-1}, {kind: ticket, ref: T-1, checked_by: ana, checked_at: 2999-01-01}]", sourceCheckInvalid, "sources[1].checked_at"},
	}
	for _, c := range cases {
		got := sourceFindings(t, c.sources+"\n")
		if len(got) != 1 || len(got[c.code]) != 1 || got[c.code][0] != c.field {
			t.Errorf("%s: got %v, want %s on %s", c.name, got, c.code, c.field)
		}
	}
}
