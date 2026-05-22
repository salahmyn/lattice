// Package scip orchestrates per-language SCIP indexers and answers
// blast-radius queries over the resulting indexes.
package scip

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/adapters"
)

// IndexResult is the outcome of running one language's SCIP indexer.
type IndexResult struct {
	Language   string `json:"language"`
	OutputPath string `json:"output_path"`
	OK         bool   `json:"ok"`
	Skipped    bool   `json:"skipped"`
	Error      string `json:"error,omitempty"`
}

// Orchestrate runs the SCIP indexer for every adapter concurrently against a
// code root, writing indexes into outDir. Indexers that are not installed are
// reported as skipped rather than failed.
func Orchestrate(ctx context.Context, codeRoot, outDir string, reg *adapters.Registry, timeout time.Duration) []IndexResult {
	_ = os.MkdirAll(outDir, 0o755)

	type job struct {
		lang   string
		cmd    []string
		output string
	}
	var jobs []job
	for _, a := range reg.All() {
		output := filepath.Join(outDir, a.Name()+".scip")
		cmd, err := a.SCIPIndexerCommand(codeRoot, output)
		if err != nil || len(cmd) == 0 {
			continue
		}
		jobs = append(jobs, job{lang: a.Name(), cmd: cmd, output: output})
	}

	results := make([]IndexResult, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			results[i] = runIndexer(ctx, codeRoot, j.lang, j.output, j.cmd, timeout)
		}(i, j)
	}
	wg.Wait()

	sort.Slice(results, func(i, j int) bool { return results[i].Language < results[j].Language })
	return results
}

// runIndexer executes one indexer command with a timeout.
func runIndexer(ctx context.Context, codeRoot, lang, output string, cmd []string, timeout time.Duration) IndexResult {
	out := IndexResult{Language: lang, OutputPath: output}
	if _, err := exec.LookPath(cmd[0]); err != nil {
		out.Skipped = true
		out.Error = cmd[0] + " not installed"
		return out
	}

	runCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	c := exec.CommandContext(runCtx, cmd[0], cmd[1:]...)
	c.Dir = codeRoot
	combined, err := c.CombinedOutput()
	if err != nil {
		out.Error = strings.TrimSpace(string(combined))
		if out.Error == "" {
			out.Error = err.Error()
		}
		return out
	}
	out.OK = true
	return out
}
