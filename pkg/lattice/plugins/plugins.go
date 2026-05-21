// Package plugins runs structural-invariant checks. A structural check is any
// executable: Lattice spawns it as a subprocess, passes the scope and config
// as JSON on stdin, and reads structured findings as JSON on stdout.
package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Input is the JSON object passed to a structural check on stdin.
type Input struct {
	Scope    Scope                  `json:"scope"`
	Config   map[string]interface{} `json:"config"`
	RepoPath string                 `json:"repo_path"`
}

// Scope narrows where a check applies.
type Scope struct {
	Modules []string `json:"modules,omitempty"`
	Files   []string `json:"files,omitempty"`
}

// Output is the JSON object a structural check writes to stdout.
type Output struct {
	Violations []CheckViolation `json:"violations"`
}

// CheckViolation is one finding from a structural check.
type CheckViolation struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// Result is the outcome of running one structural check.
type Result struct {
	CheckID    string           `json:"check_id"`
	Feature    string           `json:"feature"`
	OK         bool             `json:"ok"`
	TimedOut   bool             `json:"timed_out"`
	Error      string           `json:"error,omitempty"`
	Violations []CheckViolation `json:"violations,omitempty"`
}

// Run executes one structural check as a subprocess.
func Run(ctx context.Context, repo string, check schema.GraphStructuralCheck, timeout time.Duration) Result {
	res := Result{CheckID: check.ID, Feature: check.Feature}
	if len(check.Command) == 0 {
		res.Error = "structural check has no command"
		return res
	}

	in := Input{
		Scope:    Scope{Modules: check.Scope.Modules, Files: check.Scope.Files},
		Config:   map[string]interface{}{},
		RepoPath: repo,
	}
	stdin, err := json.Marshal(in)
	if err != nil {
		res.Error = err.Error()
		return res
	}

	runCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(runCtx, check.Command[0], check.Command[1:]...)
	cmd.Dir = repo
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if runCtx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
		res.Error = "structural check timed out"
		return res
	}
	if err != nil {
		res.Error = fmt.Sprintf("structural check failed: %v: %s", err, stderr.String())
		return res
	}

	var out Output
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		res.Error = "structural check produced invalid JSON: " + err.Error()
		return res
	}
	res.OK = true
	res.Violations = out.Violations
	return res
}

// ToViolations converts a check result into schema violations.
func (r Result) ToViolations() []schema.Violation {
	var out []schema.Violation
	if r.TimedOut {
		return []schema.Violation{{
			Code: schema.CodeStructuralCheckTimedOut, Severity: schema.SeverityError,
			FeatureID: r.Feature,
			Message:   fmt.Sprintf("structural check %q timed out", r.CheckID),
		}}
	}
	if r.Error != "" {
		return []schema.Violation{{
			Code: schema.CodeStructuralCheckFailed, Severity: schema.SeverityError,
			FeatureID: r.Feature,
			Message:   fmt.Sprintf("structural check %q errored: %s", r.CheckID, r.Error),
		}}
	}
	for _, v := range r.Violations {
		out = append(out, schema.Violation{
			Code: schema.CodeStructuralCheckFailed, Severity: schema.SeverityError,
			FeatureID: r.Feature,
			Message:   fmt.Sprintf("[%s] %s", r.CheckID, v.Message),
			Location:  &schema.Location{File: v.File, Line: v.Line},
		})
	}
	return out
}

// RunAll runs every structural check concurrently.
func RunAll(ctx context.Context, repo string, checks []schema.GraphStructuralCheck, timeout time.Duration) []Result {
	results := make([]Result, len(checks))
	done := make(chan int, len(checks))
	for i, c := range checks {
		go func(i int, c schema.GraphStructuralCheck) {
			results[i] = Run(ctx, repo, c, timeout)
			done <- i
		}(i, c)
	}
	for range checks {
		<-done
	}
	return results
}
