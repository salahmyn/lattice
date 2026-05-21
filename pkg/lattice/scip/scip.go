// Package scip orchestrates per-language SCIP indexers and answers
// blast-radius queries over the resulting indexes.
package scip

import (
	"context"
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

// Orchestrate runs the SCIP indexer for every adapter concurrently. Indexers
// that are not installed are reported as skipped rather than failed.
func Orchestrate(ctx context.Context, repo string, reg *adapters.Registry, timeout time.Duration) []IndexResult {
	type job struct {
		lang string
		cmd  []string
	}
	var jobs []job
	for _, a := range reg.All() {
		cmd, err := a.SCIPIndexerCommand(repo)
		if err != nil || len(cmd) == 0 {
			continue
		}
		jobs = append(jobs, job{lang: a.Name(), cmd: cmd})
	}

	results := make([]IndexResult, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			results[i] = runIndexer(ctx, repo, j.lang, j.cmd, timeout)
		}(i, j)
	}
	wg.Wait()

	sort.Slice(results, func(i, j int) bool { return results[i].Language < results[j].Language })
	return results
}

// runIndexer executes one indexer command with a timeout.
func runIndexer(ctx context.Context, repo, lang string, cmd []string, timeout time.Duration) IndexResult {
	out := IndexResult{
		Language:   lang,
		OutputPath: filepath.ToSlash(filepath.Join(".lattice", "scip", lang+".scip")),
	}
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
	c.Dir = repo
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
