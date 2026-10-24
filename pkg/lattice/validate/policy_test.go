package validate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

func unattributedKG() schema.KnowledgeGraph {
	return schema.KnowledgeGraph{
		Modules: []schema.GraphModule{
			{File: "src/cart.ts", Feature: "cart", SymbolFQNs: []string{"src.cart.total"}},
			{File: "src/util.ts", SymbolFQNs: []string{"src.util.pad"}},
			{File: "src/attributed.ts", SymbolFQNs: []string{"src.attributed.f"}},
			{File: "src/util.test.ts", SymbolFQNs: []string{"src.util.test.test.pads"}},
			{File: "src/types.ts"},
		},
		Symbols: []schema.GraphSymbol{
			{FQN: "src.cart.total", Kind: "function", File: "src/cart.ts", Feature: "cart"},
			{FQN: "src.util.pad", Kind: "function", File: "src/util.ts"},
			{FQN: "src.attributed.f", Kind: "function", File: "src/attributed.ts", Feature: "cart"},
		},
		Tests: []schema.GraphSymbol{{FQN: "src.util.test.test.pads", File: "src/util.test.ts", IsTest: true}},
	}
}

func byCode(v []schema.Violation, code string) []schema.Violation {
	var out []schema.Violation
	for _, f := range v {
		if f.Code == code {
			out = append(out, f)
		}
	}
	return out
}

func TestUnattributedModuleIsOptIn(t *testing.T) {
	kg := unattributedKG()
	if got := byCode(Validate(kg, config.Default(), Options{}), schema.CodeUnattributedModule); len(got) != 0 {
		t.Fatalf("UNATTRIBUTED_MODULE fired by default: %+v", got)
	}
	cfg := config.Default()
	cfg.Validate.Severity = map[string]string{schema.CodeUnattributedModule: "error"}
	got := byCode(Validate(kg, cfg, Options{}), schema.CodeUnattributedModule)
	if len(got) != 1 || got[0].Subject != "src/util.ts" || got[0].Severity != schema.SeverityError {
		t.Errorf("want one error for src/util.ts only (test-only and symbol-less modules are not production): %+v", got)
	}
}

func TestSeverityOverrides(t *testing.T) {
	in := []schema.Violation{
		{Code: "A", Severity: schema.SeverityInfo},
		{Code: "B", Severity: schema.SeverityWarning},
		{Code: "C", Severity: schema.SeverityError},
	}
	out := applySeverity(in, map[string]string{"A": "warning", "B": "off", "D": "loud"})
	codes := map[string]schema.Severity{}
	for _, f := range out {
		codes[f.Code] = f.Severity
	}
	if codes["A"] != schema.SeverityWarning || codes["C"] != schema.SeverityError {
		t.Errorf("overrides not applied: %v", codes)
	}
	if _, ok := codes["B"]; ok {
		t.Error("B was set off and should be dropped")
	}
	if codes[schema.CodeValidatePolicy] != schema.SeverityError {
		t.Error("an unknown severity name must be reported")
	}
}

func TestFailOn(t *testing.T) {
	warn := schema.Violation{Severity: schema.SeverityWarning}
	info := schema.Violation{Severity: schema.SeverityInfo}
	for _, c := range []struct {
		name       string
		warn, info bool
	}{{"", false, false}, {"error", false, false}, {"warning", true, false}, {"info", true, true}} {
		th, err := FailOn(c.name)
		if err != nil {
			t.Fatal(err)
		}
		if Fails(warn, th) != c.warn || Fails(info, th) != c.info {
			t.Errorf("fail-on %q: warn=%v info=%v, want %v %v", c.name, Fails(warn, th), Fails(info, th), c.warn, c.info)
		}
	}
	if _, err := FailOn("off"); err == nil {
		t.Error("fail-on off is not a threshold")
	}
}

func TestAllowlist(t *testing.T) {
	dir := t.TempDir()
	body := `entries:
  - code: BRD_CRITERION_UNMAPPED
    subject: brd.tv.devices:SC-5
    reason: enforced outside the analysed languages
  - code: UNATTRIBUTED_MODULE
    file: src/generated/**
    reason: generated
  - code: METHOD_LINE_CAP
    subject: src.gone.longFn
    reason: todo
  - code: UNATTRIBUTED_MODULE
    reason: forgot the selector
  - code: FILE_LINE_CAP
    subject: src/x.ts
`
	if err := os.WriteFile(filepath.Join(dir, "allowlist.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	al, bad := LoadAllowlist(dir, "allowlist.yaml")
	if len(bad) != 2 || len(al.Entries) != 3 {
		t.Fatalf("want 2 schema errors and 3 entries, got %d / %d: %+v", len(bad), len(al.Entries), bad)
	}
	findings := []schema.Violation{
		{Code: "BRD_CRITERION_UNMAPPED", Severity: schema.SeverityInfo, Subject: "brd.tv.devices:SC-5"},
		{Code: "BRD_CRITERION_UNMAPPED", Severity: schema.SeverityInfo, Subject: "brd.tv.devices:SC-6"},
		{Code: "UNATTRIBUTED_MODULE", Severity: schema.SeverityWarning, Subject: "src/generated/api.ts",
			Location: &schema.Location{File: "src/generated/api.ts"}},
	}
	open, accepted := al.Apply(append(findings, bad...))
	if len(accepted) != 2 || accepted[0].Reason == "" {
		t.Errorf("accepted = %+v", accepted)
	}
	var stale, schemaErrs, sc6 int
	for _, f := range open {
		switch {
		case f.Code == schema.CodeAllowlistStale:
			stale++
		case f.Code == schema.CodeAllowlistSchema:
			schemaErrs++
		case f.Subject == "brd.tv.devices:SC-6":
			sc6++
		}
	}
	if stale != 1 || schemaErrs != 2 || sc6 != 1 {
		t.Errorf("open: stale=%d schema=%d sc6=%d, want 1 2 1 (%+v)", stale, schemaErrs, sc6, open)
	}

	empty, none := LoadAllowlist(t.TempDir(), "allowlist.yaml")
	if len(empty.Entries) != 0 || none != nil {
		t.Error("a missing allowlist is empty, not an error")
	}
}
