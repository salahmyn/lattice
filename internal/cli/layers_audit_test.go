package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Audit rows carry the layers of their enforcers: a SQL policy is data from
// the adapter, a TypeScript function is ui from a declared rule, a function no
// rule matches reads unknown, and a promise enforced in two layers lists both.
// @verifies graph:INV-6
func TestAuditRowsCarryLayers(t *testing.T) {
	dir := layersFixture(t, "mode: embedded\nlayers:\n  - glob: \"web/**\"\n    layer: ui\n  - glob: \"db/**\"\n    layer: service\n")
	rep := auditJSON(t, dir)
	for promise, want := range map[string][]string{
		"cart:INV-1": {"ui"},
		"cart:INV-2": {"data"}, // the adapter fact beats the db/** rule
		"cart:INV-3": {"unknown"},
		"cart:INV-4": {"data", "ui"},
	} {
		if got := auditRow(t, rep, promise).Layers; !reflect.DeepEqual(got, want) {
			t.Errorf("%s layers = %v, want %v", promise, got, want)
		}
	}
	ev := auditRow(t, rep, "cart:INV-1").Evidence.Enforcers
	if len(ev) != 1 || ev[0].Layer != "ui" {
		t.Errorf("enforcer evidence = %+v", ev)
	}
}

// `feature show` lists the feature's layers, and the JSON adds a `layers`
// field beside the manifest's own.
// @verifies graph:INV-6
func TestFeatureShowLayers(t *testing.T) {
	dir := layersFixture(t, "mode: embedded\nlayers:\n  - glob: \"web/**\"\n    layer: ui\n")
	out, err := run(t, dir, newFeatureCommand, "show", "cart")
	if err != nil {
		t.Fatalf("feature show: %v\n%s", err, out)
	}
	var m struct {
		ID     string
		Layers []string `json:"layers"`
	}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if m.ID != "cart" || !reflect.DeepEqual(m.Layers, []string{"data", "ui", "unknown"}) {
		t.Errorf("feature = %+v", m)
	}
	var text bytes.Buffer
	io := &IO{Repo: dir, Out: &text, Err: &text}
	cmd := newFeatureCommand(io)
	cmd.SetArgs([]string{"show", "cart"})
	cmd.SetOut(&text)
	cmd.SetErr(&text)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text.String(), "layers      data, ui, unknown") || !strings.Contains(text.String(), "(typescript) [ui]") {
		t.Errorf("text output:\n%s", text.String())
	}
}
