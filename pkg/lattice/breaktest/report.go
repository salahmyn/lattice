package breaktest

// @module-feature breaktest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/results"
)

// Schema is the version stamp of breaks.json. The audit reads the file by
// this contract and does not import this package.
const Schema = "lattice.breaks/1"

// Method says how a promise's enforcer was broken.
const (
	MethodEarlyReturn = "early_return"
	MethodScripted    = "scripted"
)

// Baseline is the verifiers' result before the break.
const (
	BaselinePass  = "pass"
	BaselineFail  = "fail"
	BaselineError = "error"
)

// Outcome is what the break showed. Unknown is never a pass and never a fail.
const (
	Killed   = "killed"
	Survived = "survived"
	Unknown  = "unknown"
)

// Report is lattice/.cache/results/breaks.json.
type Report struct {
	Schema string `json:"schema"`
	Commit string `json:"commit"`
	// CIRun is the CI run that wrote the file, or "local" (results.CIRunID).
	CIRun       string `json:"ci_run,omitempty"`
	Dirty       bool   `json:"dirty"`
	GeneratedAt string `json:"generated_at"`
	Runs        []Run  `json:"runs"`
}

// Run is one enforcer of one promise, broken and checked against the
// promise's verifiers. A promise with several enforcers has one run each.
type Run struct {
	Promise      string   `json:"promise"`
	Enforcer     string   `json:"enforcer"`
	EnforcerFile string   `json:"enforcer_file"`
	EnforcerHash string   `json:"enforcer_hash"`
	Verifiers    []string `json:"verifiers"`
	VerifierHash string   `json:"verifier_hash"`
	Method       string   `json:"method"`
	Script       string   `json:"script,omitempty"`
	// ScriptHash is the sha256 of the script, part of the cache key. It is
	// an addition to the lattice.breaks/1 contract; readers ignore it.
	ScriptHash string `json:"script_hash,omitempty"`
	// Baseline is empty when the verifiers were never run because there was
	// nothing to run them against (no generated break, no verifiers).
	Baseline   string `json:"baseline"`
	Outcome    string `json:"outcome"`
	Reason     string `json:"reason,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	// Cached marks an outcome reused from an earlier run. Also an addition
	// to the contract.
	Cached bool `json:"cached,omitempty"`

	// RanVerifiers are the verifiers that executed: from the JUnit report
	// when the runner has {junit}, from Go's -v pass lines, else every
	// verifier the runner was given.
	RanVerifiers []string `json:"ran_verifiers,omitempty"`
	// NotRunVerifiers are the verifiers that did not execute: no runner
	// matches their file, or the runner's filter selected no test for them.
	// A survived outcome with any of these says nothing about them.
	NotRunVerifiers []string `json:"not_run_verifiers,omitempty"`
	// FailedTests are the names of the tests that failed after the break.
	FailedTests []string `json:"failed_tests,omitempty"`
	// ConfigHash is the sha256 of the resolved runner commands, the setup
	// commands and GeneratorVersion, part of the cache key.
	ConfigHash string `json:"config_hash,omitempty"`
}

// GeneratorVersion stamps the generators, the runner logic and the outcome
// rules. It is part of the cache key, so a change to any of them invalidates
// cached outcomes.
const GeneratorVersion = "2"

// cacheKey is what makes an earlier outcome valid again: the same enforcer
// source, the same verifier source, the same way of breaking it, and the
// same way of running the verifiers.
func (r Run) cacheKey() [5]string {
	return [5]string{r.EnforcerHash, r.VerifierHash, r.Method, r.ScriptHash, r.ConfigHash}
}

// cacheable reports whether the outcome may be reused later. Unknown never
// is: a missing runner can be configured and a timeout can be a slow
// machine, so neither says anything about the next run.
// @enforces breaktest:INV-5
func (r Run) cacheable() bool {
	return r.Outcome == Killed || r.Outcome == Survived
}

// Path is where breaks.json lives under the lattice directory.
func Path(latticeDir string) string {
	return filepath.Join(latticeDir, ".cache", "results", "breaks.json")
}

// Read loads breaks.json. A missing or unreadable file is an empty report.
func Read(latticeDir string) Report {
	var r Report
	data, err := os.ReadFile(Path(latticeDir))
	if err != nil || json.Unmarshal(data, &r) != nil || r.Schema != Schema {
		return Report{}
	}
	return r
}

// Write saves the report, runs sorted by promise then enforcer.
func Write(latticeDir string, r Report) error {
	r.Schema = Schema
	if r.CIRun == "" {
		r.CIRun = results.CIRunID()
	}
	if r.GeneratedAt == "" {
		r.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if r.Runs == nil {
		r.Runs = []Run{}
	}
	sort.SliceStable(r.Runs, func(i, j int) bool {
		if r.Runs[i].Promise != r.Runs[j].Promise {
			return r.Runs[i].Promise < r.Runs[j].Promise
		}
		return r.Runs[i].Enforcer < r.Runs[j].Enforcer
	})
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	p := Path(latticeDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0o644)
}

func sha(parts ...[]byte) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	return hex.EncodeToString(h.Sum(nil))
}
