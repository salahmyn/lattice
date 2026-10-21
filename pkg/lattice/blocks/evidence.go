package blocks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Evidence is one initiative's analysis (evidence/v1): verdicts on existing
// blocks, gaps, payload differences and the proposed package's model. Block
// ids are catalogue ids of Source.
type Evidence struct {
	Format   string            `json:"format"`
	ID       string            `json:"id"` // the initiative id
	Source   string            `json:"source"`
	Classes  []VerdictClass    `json:"classes"` // in rank order, most severe first
	Package  *EvidencePackage  `json:"package,omitempty"`
	Verdicts []EvidenceVerdict `json:"verdicts,omitempty"`
	Gaps     []EvidenceGap     `json:"gaps,omitempty"`
	Payload  []PayloadDiff     `json:"payload,omitempty"`
}

// VerdictClass is one verdict a block can get; Replaces marks the class whose
// blocks the proposal replaces.
type VerdictClass struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Replaces bool   `json:"replaces,omitempty"`
}

// EvidencePackage is the proposed package, extracted from its source.
type EvidencePackage struct {
	Name       string             `json:"name"`
	Version    string             `json:"version,omitempty"`
	Commit     string             `json:"commit,omitempty"`
	CommitDate string             `json:"commit_date,omitempty"`
	Dir        string             `json:"dir,omitempty"`
	Components []PackageComponent `json:"components,omitempty"`
	Modules    []PackageModules   `json:"modules,omitempty"`
	Paths      []string           `json:"paths,omitempty"` // request paths the package calls
}

// PackageComponent is one component the package exports.
type PackageComponent struct {
	Tag     string   `json:"tag"`
	File    string   `json:"file,omitempty"`
	Props   []string `json:"props,omitempty"`
	Events  []string `json:"events,omitempty"`
	Methods []string `json:"methods,omitempty"`
}

// PackageModules are the package's modules for one concern.
type PackageModules struct {
	Concern string   `json:"concern"`
	Modules []string `json:"modules"`
}

// EvidenceVerdict is one row of the verdict matrix.
type EvidenceVerdict struct {
	Block       string `json:"block"`
	Case        string `json:"case,omitempty"`
	Class       string `json:"class"`
	Requirement string `json:"requirement,omitempty"`
	Evidence    string `json:"evidence,omitempty"`
	Notes       string `json:"notes,omitempty"`
}

// EvidenceGap is one gap; whether it blocks is the initiative's call.
type EvidenceGap struct {
	Code    string   `json:"code"`
	Cites   []string `json:"cites,omitempty"`
	Low     bool     `json:"low,omitempty"`
	Owners  []string `json:"owners,omitempty"`
	Summary string   `json:"summary,omitempty"`
	Case    string   `json:"case,omitempty"`
}

// PayloadDiff is one request field the package sends differently.
type PayloadDiff struct {
	Endpoint   string   `json:"endpoint"`
	Field      string   `json:"field"`
	Old        string   `json:"old,omitempty"`
	New        string   `json:"new,omitempty"`
	Difference string   `json:"difference,omitempty"`
	Impact     string   `json:"impact,omitempty"`
	Severity   string   `json:"severity"`
	Paths      []string `json:"paths,omitempty"` // request paths the endpoint covers
}

// ParseEvidence reads one evidence/v1 file strictly.
func ParseEvidence(data []byte) (Evidence, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var ev Evidence
	if err := dec.Decode(&ev); err != nil {
		return Evidence{}, fmt.Errorf("evidence/v1: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return Evidence{}, fmt.Errorf("evidence/v1: unexpected data after the evidence")
	}
	switch {
	case ev.Format != "evidence/v1":
		return Evidence{}, fmt.Errorf("evidence/v1: format is %q, want evidence/v1", ev.Format)
	case ev.ID == "":
		return Evidence{}, fmt.Errorf("evidence/v1: no id (the initiative id)")
	case ev.Source == "":
		return Evidence{}, fmt.Errorf("evidence/v1: no source (the workspace source its block ids belong to)")
	case len(ev.Classes) == 0:
		return Evidence{}, fmt.Errorf("evidence/v1: no verdict classes")
	}
	classes := map[string]bool{}
	for _, c := range ev.Classes {
		if c.ID == "" || classes[c.ID] {
			return Evidence{}, fmt.Errorf("evidence/v1: class id %q is empty or repeated", c.ID)
		}
		classes[c.ID] = true
	}
	for i, v := range ev.Verdicts {
		if v.Block == "" || !classes[v.Class] {
			return Evidence{}, fmt.Errorf("evidence/v1: verdict %d: needs a block and a declared class (got %q)", i, v.Class)
		}
	}
	codes := map[string]bool{}
	for i, g := range ev.Gaps {
		if g.Code == "" || codes[g.Code] {
			return Evidence{}, fmt.Errorf("evidence/v1: gap %d: code %q is empty or repeated", i, g.Code)
		}
		codes[g.Code] = true
	}
	for i, p := range ev.Payload {
		if p.Endpoint == "" || p.Severity == "" {
			return Evidence{}, fmt.Errorf("evidence/v1: payload %d needs endpoint and severity", i)
		}
	}
	if p := ev.Package; p != nil {
		tags, concerns := map[string]bool{}, map[string]bool{}
		for i, c := range p.Components {
			if c.Tag == "" || tags[c.Tag] {
				return Evidence{}, fmt.Errorf("evidence/v1: package component %d: tag %q is empty or repeated", i, c.Tag)
			}
			tags[c.Tag] = true
		}
		for i, m := range p.Modules {
			if m.Concern == "" || concerns[m.Concern] {
				return Evidence{}, fmt.Errorf("evidence/v1: package modules %d: concern %q is empty or repeated", i, m.Concern)
			}
			concerns[m.Concern] = true
		}
	}
	return ev, nil
}

var initiativeStatuses = map[schema.InitiativeStatus]bool{
	schema.InitiativeProposed: true, schema.InitiativeAccepted: true, schema.InitiativeInProgress: true,
	schema.InitiativePaused: true, schema.InitiativeComplete: true, schema.InitiativeCancelled: true,
}

// ValidStatus reports whether s is one of the existing initiative statuses.
func ValidStatus(s schema.InitiativeStatus) bool { return initiativeStatuses[s] }

// ParseInitiative reads one initiative record strictly.
func ParseInitiative(data []byte, label string) (schema.Initiative, error) {
	var in schema.Initiative
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&in); err != nil {
		return schema.Initiative{}, fmt.Errorf("%s: %w", label, err)
	}
	switch {
	case in.ID == "":
		return schema.Initiative{}, fmt.Errorf("%s: initiative has no id", label)
	case !ValidStatus(in.Status):
		return schema.Initiative{}, fmt.Errorf("%s: initiative %s has status %q (proposed, accepted, in_progress, paused, complete, cancelled)", label, in.ID, in.Status)
	}
	if m := in.Map; m != nil {
		if m.System.Title == "" {
			return schema.Initiative{}, fmt.Errorf("%s: initiative %s: map.system needs a title", label, in.ID)
		}
		for i, x := range m.Externals {
			if x.To == "" || x.Kind == "" {
				return schema.Initiative{}, fmt.Errorf("%s: initiative %s: map.externals %d needs to and kind", label, in.ID, i)
			}
		}
	}
	for i, p := range in.Pins {
		if p.Role == "" {
			return schema.Initiative{}, fmt.Errorf("%s: initiative %s: pin %d has no role", label, in.ID, i)
		}
	}
	return in, nil
}
