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

	// Baselines counts the baseline invocations the run that made this
	// report started and reused. It is not part of breaks.json.
	Baselines BaselineCounts `json:"-"`
	// SetupRuns counts the times break.setup ran in the run that made this
	// report: once, plus each re-run after a failed restore or a baseline
	// healed. It is not part of breaks.json.
	SetupRuns int `json:"-"`
	// baselines are the passing baselines Write keeps for the tree, nil
	// when none are kept (a dirty tree).
	baselines *baselineFile
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

	// RanVerifiers are the verifiers that executed at baseline and, when no
	// group failed after the break, after it too: from the JUnit report when
	// the runner has {junit}, from Go's -v pass lines, else every verifier
	// the runner was given.
	RanVerifiers []string `json:"ran_verifiers,omitempty"`
	// NotRunVerifiers are the verifiers that did not execute at baseline or
	// after the break: no runner matches their file, the runner's filter
	// selected no test for them, or the JUnit report does not show them run.
	// A run with any of these is never survived: it is killed when a group
	// failed after the break, else unknown with a PartialPrefix reason.
	NotRunVerifiers []string `json:"not_run_verifiers,omitempty"`
	// FailedTests are the names of the tests that failed after the break.
	FailedTests []string `json:"failed_tests,omitempty"`
	// ConfigHash is the sha256 of the resolved runner commands, the setup
	// commands and GeneratorVersion, part of the cache key.
	ConfigHash string `json:"config_hash,omitempty"`
	// InputsHash names what the outcome was measured on, part of the cache
	// key: "tree:<tree sha>" for HEAD's tree (the default), or "inputs:<sha256>"
	// of the files the runners declare as their inputs (break.runners[].inputs).
	// Empty for a run on a dirty tree, which has no tree to name, and in
	// reports written before the field existed: neither is ever reused. An
	// addition to the lattice.breaks/1 contract; readers ignore it.
	InputsHash string `json:"inputs_hash,omitempty"`
}

// GeneratorVersion stamps the generators, the runner logic and the outcome
// rules. It is part of the cache key, so a change to any of them invalidates
// cached outcomes. 3: a break that left the verifiers that ran green while
// others did not run reads unknown, not survived. 4: a SQL break edits the
// statement that sets the object's effective definition (the last ALTER
// POLICY that sets an expression, not the CREATE a later one overrides), and
// the object swap is offered only when no later statement touches the object.
const GeneratorVersion = "4"

// cacheKey is what makes an earlier outcome valid again: the same enforcer
// source, the same verifier source, the same way of breaking it, the same
// way of running the verifiers, and the same inputs (by default, the same
// tree): an outcome is a measurement of one tree, and says nothing about
// another unless the runners declare which files they read.
// @enforces breaktest:INV-16
func (r Run) cacheKey() [6]string {
	return [6]string{r.EnforcerHash, r.VerifierHash, r.Method, r.ScriptHash, r.ConfigHash, r.InputsHash}
}

// cacheable reports whether the outcome may be reused later. Unknown never
// is: a missing runner can be configured and a timeout can be a slow
// machine, so neither says anything about the next run. Nor is a run with no
// InputsHash (a dirty tree, or a report from before the field existed): it
// names no tree, so nothing shows where it holds.
// @enforces breaktest:INV-5, breaktest:INV-16
func (r Run) cacheable() bool {
	return (r.Outcome == Killed || r.Outcome == Survived) && r.InputsHash != ""
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

// Write saves the report, runs sorted by promise then enforcer, and the
// passing baselines of a clean tree to break-baselines.json beside it.
func Write(latticeDir string, r Report) error {
	if r.baselines != nil {
		if err := r.baselines.write(latticeDir); err != nil {
			return err
		}
	}
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
