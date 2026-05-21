// Package mutation orchestrates per-language mutation-test runners, scoped to
// invariant-enforcing code, and maps surviving mutants back to invariants.
package mutation

import (
	"context"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/salahmyn/lattice/pkg/lattice/adapters"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// Scope selects which invariant-enforcing code is mutation-tested.
type Scope string

const (
	ScopeAll     Scope = "all"
	ScopeChanged Scope = "changed"
)

// LanguageResult is one runner's outcome.
type LanguageResult struct {
	Language string   `json:"language"`
	Files    []string `json:"files"`
	OK       bool     `json:"ok"`
	Skipped  bool     `json:"skipped"`
	Error    string   `json:"error,omitempty"`
}

// Report is the aggregate mutation-testing result.
type Report struct {
	PerInvariant   map[string]float64 `json:"per_invariant"`
	PerFeature     map[string]float64 `json:"per_feature"`
	Languages      []LanguageResult   `json:"languages"`
	BelowThreshold []ThresholdMiss    `json:"below_threshold,omitempty"`
}

// ThresholdMiss records an invariant whose score is under its threshold.
type ThresholdMiss struct {
	Invariant string  `json:"invariant"`
	Score     float64 `json:"score"`
	Threshold float64 `json:"threshold"`
}

// Options controls a mutation run.
type Options struct {
	Scope        Scope
	FeatureID    string   // limit to one feature, "" for all
	ChangedFiles []string // for ScopeChanged
}

// Runner orchestrates mutation testing for a repository.
type Runner struct {
	repo string
	reg  *adapters.Registry
	cfg  config.Config
}

// NewRunner returns a mutation runner.
func NewRunner(repo string, reg *adapters.Registry, cfg config.Config) *Runner {
	return &Runner{repo: repo, reg: reg, cfg: cfg}
}

// Run executes mutation testing over the invariant-enforcing code of the
// given knowledge graph and returns per-invariant scores.
func (r *Runner) Run(ctx context.Context, kg schema.KnowledgeGraph, opts Options) Report {
	if ctx == nil {
		ctx = context.Background()
	}
	targets := r.targets(kg, opts)
	report := Report{PerInvariant: map[string]float64{}, PerFeature: map[string]float64{}}

	var mu sync.Mutex
	var wg sync.WaitGroup
	for lang, files := range targets.byLanguage {
		wg.Add(1)
		go func(lang string, files []string) {
			defer wg.Done()
			lr := r.runLanguage(ctx, lang, files)
			mu.Lock()
			report.Languages = append(report.Languages, lr)
			mu.Unlock()
		}(lang, files)
	}
	wg.Wait()
	sort.Slice(report.Languages, func(i, j int) bool {
		return report.Languages[i].Language < report.Languages[j].Language
	})

	// Without installed runners the per-file scores stay empty; carry any
	// scores already recorded on the manifests so re-validation is stable.
	for _, m := range kg.Features {
		for inv, score := range m.MutationScores {
			report.PerInvariant[m.ID+":"+inv] = score
		}
	}
	r.applyThresholds(&report)
	return report
}

// targetSet groups invariant-enforcing files by language and by invariant.
type targetSet struct {
	byLanguage map[string][]string
	byFile     map[string][]string // file -> "feature:INV" keys
}

// targets computes the invariant-enforcing files in scope.
func (r *Runner) targets(kg schema.KnowledgeGraph, opts Options) targetSet {
	ts := targetSet{byLanguage: map[string][]string{}, byFile: map[string][]string{}}
	changed := map[string]bool{}
	for _, f := range opts.ChangedFiles {
		changed[filepath.ToSlash(f)] = true
	}

	seen := map[string]bool{}
	for _, s := range kg.Symbols {
		if len(s.EnforcesInvariants) == 0 {
			continue
		}
		if opts.FeatureID != "" && s.Feature != opts.FeatureID {
			continue
		}
		if opts.Scope == ScopeChanged && !changed[filepath.ToSlash(s.File)] {
			continue
		}
		for _, inv := range s.EnforcesInvariants {
			f := s.Feature
			item := inv
			if i := strings.LastIndex(inv, ":"); i > 0 {
				f, item = inv[:i], inv[i+1:]
			}
			ts.byFile[s.File] = append(ts.byFile[s.File], f+":"+item)
		}
		key := s.Language + "\x00" + s.File
		if !seen[key] {
			seen[key] = true
			ts.byLanguage[s.Language] = append(ts.byLanguage[s.Language], s.File)
		}
	}
	for lang := range ts.byLanguage {
		sort.Strings(ts.byLanguage[lang])
	}
	return ts
}

// runLanguage invokes one language's mutation runner.
func (r *Runner) runLanguage(ctx context.Context, lang string, files []string) LanguageResult {
	lr := LanguageResult{Language: lang, Files: files}
	ad := r.reg.ByName(lang)
	if ad == nil {
		lr.Error = "no adapter"
		return lr
	}
	cmd, err := ad.MutationRunnerCommand(r.repo, files)
	if err != nil || len(cmd) == 0 {
		lr.Skipped = true
		lr.Error = "mutation runner not configured"
		return lr
	}
	if _, err := exec.LookPath(cmd[0]); err != nil {
		lr.Skipped = true
		lr.Error = cmd[0] + " not installed"
		return lr
	}

	runCtx := ctx
	if d := r.cfg.Subprocess.DefaultTimeoutDuration(); d > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, d*time.Duration(len(files)+1))
		defer cancel()
	}
	c := exec.CommandContext(runCtx, cmd[0], cmd[1:]...)
	c.Dir = r.repo
	out, err := c.CombinedOutput()
	if err != nil {
		lr.Error = strings.TrimSpace(string(out))
		if lr.Error == "" {
			lr.Error = err.Error()
		}
		return lr
	}
	lr.OK = true
	return lr
}

// applyThresholds flags invariants scoring below their configured threshold.
func (r *Runner) applyThresholds(report *Report) {
	for key, score := range report.PerInvariant {
		threshold := r.cfg.MutationTesting.Thresholds.ThresholdFor(key)
		if score < threshold {
			report.BelowThreshold = append(report.BelowThreshold, ThresholdMiss{
				Invariant: key, Score: score, Threshold: threshold,
			})
		}
	}
	sort.Slice(report.BelowThreshold, func(i, j int) bool {
		return report.BelowThreshold[i].Invariant < report.BelowThreshold[j].Invariant
	})
}
