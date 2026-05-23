// Package importer implements brownfield adoption: discovering the features
// already latent in an existing codebase so Lattice artifacts can be drafted
// from them.
//
// This package covers Stage 1 of the import pipeline — Discover. It is pure
// static analysis: it consumes parsed IR modules and produces feature
// candidates (clusters of code symbols plus the evidence that grouped them).
// No LLM is involved and nothing is named — naming is a later stage's job.
//
// Discovery is deterministic: identical code produces a byte-identical
// candidates.json, so a re-scan reconciles rather than churns.
package importer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sort"
	"strings"
)

// candidatesVersion is the schema version of candidates.json.
const candidatesVersion = 1

// CandidatesFileName is the Stage-1 artifact written under the import dir.
const CandidatesFileName = "candidates.json"

// Clustering signals, recorded as evidence on a candidate.
const (
	// SignalPackage: the symbols share a source directory — the team's own
	// first decomposition.
	SignalPackage = "package_structure"
	// SignalSurface: the cluster exposes a harvested surface (route, event).
	SignalSurface = "harvested_surface"
	// SignalTestGroup: a co-located test file exercises the cluster.
	SignalTestGroup = "test_group"
	// SignalSupertype: several symbols in the cluster share a base type.
	SignalSupertype = "shared_supertype"
)

// Evidence is one signal that supports a candidate. Evidence is non-negotiable:
// it is what lets a reviewer trust — or reject — the candidate.
type Evidence struct {
	Signal string `json:"signal"`
	Detail string `json:"detail"`
}

// Candidate is a discovered feature candidate: a cluster of code symbols and
// the evidence that grouped them. It carries no name and no prose.
type Candidate struct {
	// ID is stable — hashed from the symbol set — so a re-scan of unchanged
	// code yields the same ID and decisions can be reconciled.
	ID         string     `json:"id"`
	Package    string     `json:"package"`  // source directory, code-root-relative
	Language   string     `json:"language"` // dominant language of the cluster
	Symbols    []string   `json:"symbols"`  // production symbol FQNs, sorted
	Files      []string   `json:"files"`    // source files, sorted
	Confidence float64    `json:"confidence"`
	Evidence   []Evidence `json:"evidence"`
}

// CandidatesFile is the on-disk Stage-1 artifact (import/candidates.json).
type CandidatesFile struct {
	Version int `json:"version"`
	// Scopes records the --scope values the scan was run with. It is
	// informational (the candidates themselves are already filtered).
	Scopes     []string    `json:"scopes,omitempty"`
	Candidates []Candidate `json:"candidates"`
	Coverage   Coverage    `json:"coverage"`
}

// UnmarshalJSON accepts both the legacy {"scope": "<string>"} and the
// {"scopes": [...]} multi-scope form so v0.2.0 candidates files still load.
func (cf *CandidatesFile) UnmarshalJSON(data []byte) error {
	type alias CandidatesFile
	aux := struct {
		Scope string `json:"scope,omitempty"`
		*alias
	}{alias: (*alias)(cf)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.Scope != "" && len(cf.Scopes) == 0 {
		cf.Scopes = []string{aux.Scope}
	}
	return nil
}

// candidateID derives a stable ID from the cluster's symbol set.
func candidateID(symbols []string) string {
	sorted := append([]string(nil), symbols...)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return "cand_" + hex.EncodeToString(sum[:])[:12]
}

// Marshal renders a CandidatesFile as deterministic, indented JSON.
func Marshal(cf CandidatesFile) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(cf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Write emits the candidates file to path.
func Write(path string, cf CandidatesFile) error {
	data, err := Marshal(cf)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadCandidates reads a candidates file from path.
func LoadCandidates(path string) (CandidatesFile, error) {
	var cf CandidatesFile
	data, err := os.ReadFile(path)
	if err != nil {
		return cf, err
	}
	err = json.Unmarshal(data, &cf)
	return cf, err
}
