package entrypoints

// @module-feature entrypoints

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Decision values for entry-point review. Mirror the v0.2.1 importer's
// DecisionAccepted / DecisionRejected so a reviewer's vocabulary
// matches across both axes.
const (
	DecisionAcceptEP = "accept"
	DecisionRejectEP = "reject"
)

// DecideResult reports what Decide actually did, for honest CLI/UI
// summaries (the user sees "moved to production" or "removed and
// archived to .rejected/", not just "ok").
type DecideResult struct {
	ID         string `json:"id"`
	Decision   string `json:"decision"`
	NewStatus  string `json:"new_status,omitempty"`  // accept: "production"
	Path       string `json:"path"`                  // current file path (post-decision)
	ArchivedAt string `json:"archived_at,omitempty"` // reject: path in .rejected/
}

// Decide flips an EP's status to production (accept) or moves the
// file under .rejected/ (reject). The .rejected/ archive — rather
// than delete — means a reviewer can recover a wrong call without
// re-running extract, and Merge() will skip rejected files because
// they're under a hidden directory the walker ignores.
// @enforces entrypoints:INV-3
func Decide(entryPointsDir, id, decision string) (DecideResult, error) {
	res := DecideResult{ID: id, Decision: decision}
	relPath := filepath.FromSlash(strings.ReplaceAll(id, ".", "/")) + ".yaml"
	path := filepath.Join(entryPointsDir, relPath)
	data, err := os.ReadFile(path)
	if err != nil {
		return res, fmt.Errorf("no entry-point manifest at %s: %w", path, err)
	}
	switch decision {
	case DecisionAcceptEP:
		var ep schema.EntryPoint
		if err := yaml.Unmarshal(data, &ep); err != nil {
			return res, fmt.Errorf("parse %s: %w", path, err)
		}
		ep.Status = schema.StatusProduction
		out, err := schema.MarshalCanonical(ep)
		if err != nil {
			return res, err
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			return res, err
		}
		res.NewStatus = string(schema.StatusProduction)
		res.Path = path
		return res, nil
	case DecisionRejectEP:
		archive := filepath.Join(entryPointsDir, ".rejected", relPath)
		if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
			return res, err
		}
		if err := os.Rename(path, archive); err != nil {
			return res, err
		}
		res.ArchivedAt = archive
		res.Path = ""
		return res, nil
	default:
		return res, fmt.Errorf("decision must be %q or %q, got %q", DecisionAcceptEP, DecisionRejectEP, decision)
	}
}

// LoadStatus is a small helper that returns just the status of an EP
// on disk without reading the whole manifest — used by the CLI list
// command to colour rows by status.
func LoadStatus(entryPointsDir, id string) string {
	path := filepath.Join(entryPointsDir,
		filepath.FromSlash(strings.ReplaceAll(id, ".", "/"))+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var head struct {
		Status string `yaml:"status"`
	}
	if yaml.Unmarshal(data, &head) != nil {
		return ""
	}
	return head.Status
}

// DecideDerived records a reviewer's decision on a proposal that exists only
// in the derived graph (see ProposeFromRuntime), so no manifest is on disk
// yet. Accept writes it with status production; reject writes it straight
// into .rejected/, which IsRejected reads. The flow is left out: it is
// derived. A manifest that already exists is never overwritten.
// @enforces entrypoints:INV-3
func DecideDerived(entryPointsDir string, ep schema.EntryPoint, decision string) (DecideResult, error) {
	res := DecideResult{ID: ep.ID, Decision: decision}
	rel := filepath.FromSlash(strings.ReplaceAll(ep.ID, ".", "/")) + ".yaml"
	path := filepath.Join(entryPointsDir, rel)
	ep.Flow, ep.SideEffects = nil, nil
	switch decision {
	case DecisionAcceptEP:
		ep.Status = schema.StatusProduction
		res.NewStatus = string(schema.StatusProduction)
		res.Path = path
	case DecisionRejectEP:
		path = filepath.Join(entryPointsDir, ".rejected", rel)
		res.ArchivedAt = path
	default:
		return res, fmt.Errorf("decision must be %q or %q, got %q", DecisionAcceptEP, DecisionRejectEP, decision)
	}
	if _, err := os.Stat(path); err == nil {
		return res, fmt.Errorf("%s already exists", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return res, err
	}
	out, err := schema.MarshalCanonical(ep)
	if err != nil {
		return res, err
	}
	return res, os.WriteFile(path, out, 0o644)
}
