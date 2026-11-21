package ui

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/audit"
)

// The promise object of /api/v2 carries the layers of its enforcers, and each
// enforcer its own layer; a promise whose enforcers have no layer assigned
// omits both.
// @verifies graph:INV-6
func TestPromiseCarriesLayers(t *testing.T) {
	row := audit.Row{Promise: "f:INV-1", Feature: "f", Statement: "s", Tier: 1, Class: audit.ClassBreakTested,
		Layers:   []string{"data", "ui"},
		Evidence: audit.Evidence{Enforcers: []audit.EnforcerEvidence{{FQN: "a", Layer: "ui"}, {FQN: "b", Layer: "data"}}}}
	p := (&v2world{}).promiseOf(row, nil)
	if !reflect.DeepEqual(p.Layers, []string{"data", "ui"}) || p.Enforcers[0].Layer != "ui" || p.Enforcers[1].Layer != "data" {
		t.Errorf("layers = %v, enforcers = %+v", p.Layers, p.Enforcers)
	}
	body, _ := json.Marshal(p)
	if !strings.Contains(string(body), `"layers":["data","ui"]`) {
		t.Errorf("payload = %s", body)
	}
	row.Layers, row.Evidence.Enforcers = nil, []audit.EnforcerEvidence{{FQN: "a"}}
	body, _ = json.Marshal((&v2world{}).promiseOf(row, nil))
	if strings.Contains(string(body), `"layer`) {
		t.Errorf("no layer assigned, yet payload = %s", body)
	}
}
