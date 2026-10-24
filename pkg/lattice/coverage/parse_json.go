package coverage

// @module-feature coverage

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type istanbulPos struct {
	Line int `json:"line"`
}

type istanbulRange struct {
	Start istanbulPos `json:"start"`
	End   istanbulPos `json:"end"`
}

type istanbulFn struct {
	Name string        `json:"name"`
	Decl istanbulRange `json:"decl"`
	Loc  istanbulRange `json:"loc"`
	Line int           `json:"line"`
}

type istanbulFile struct {
	Path         string                   `json:"path"`
	StatementMap map[string]istanbulRange `json:"statementMap"`
	S            map[string]int           `json:"s"`
	FnMap        map[string]istanbulFn    `json:"fnMap"`
	F            map[string]int           `json:"f"`
}

// ParseIstanbul reads an istanbul coverage-final.json (nyc, c8 --reporter
// json, vitest and Jest json). A statement counts on its start line, the
// highest count on a line winning, as istanbul's own line summary does. Each
// fnMap entry is a function record at its declaration line.
func ParseIstanbul(data []byte) (Report, error) {
	var files map[string]istanbulFile
	if err := json.Unmarshal(data, &files); err != nil {
		return Report{}, fmt.Errorf("istanbul json: %w", err)
	}
	r := NewReport(FormatIstanbul)
	for key, f := range files {
		p := f.Path
		if p == "" {
			p = key
		}
		fc := r.File(p)
		for id, loc := range f.StatementMap {
			l, h := loc.Start.Line, f.S[id]
			if prev, seen := fc.Lines[l]; !seen || h > prev {
				fc.Lines[l] = h
			}
		}
		for id, fn := range f.FnMap {
			l := fn.Decl.Start.Line
			if l == 0 {
				l = fn.Line
			}
			if l == 0 {
				l = fn.Loc.Start.Line
			}
			fc.Funcs[l] += f.F[id]
		}
	}
	if len(r.Files) == 0 {
		return Report{}, fmt.Errorf("istanbul json: no files")
	}
	return r, nil
}

type coveragePyFile struct {
	ExecutedLines []int               `json:"executed_lines"`
	MissingLines  []int               `json:"missing_lines"`
	Contexts      map[string][]string `json:"contexts"`
}

type coveragePyReport struct {
	Meta struct {
		Version      string `json:"version"`
		ShowContexts bool   `json:"show_contexts"`
	} `json:"meta"`
	Files map[string]coveragePyFile `json:"files"`
}

// ParseCoveragePy reads `coverage json`. The suite report holds every
// measured line (executed hit once, missing zero). When the report was
// written with --show-contexts, contexts maps each test context to the
// lines it ran, one Report per context: the per-test data coverage.py
// collects in a single run (dynamic_context = test_function, or pytest-cov
// --cov-context=test). The empty context (code run outside any test, such
// as imports at collection) and setup/teardown phases are left out.
func ParseCoveragePy(data []byte) (suite Report, contexts map[string]Report, err error) {
	var raw coveragePyReport
	if err := json.Unmarshal(data, &raw); err != nil {
		return Report{}, nil, fmt.Errorf("coverage.py json: %w", err)
	}
	if raw.Files == nil {
		return Report{}, nil, fmt.Errorf("coverage.py json: no files")
	}
	suite = NewReport(FormatCoveragePy)
	contexts = map[string]Report{}
	paths := make([]string, 0, len(raw.Files))
	for p := range raw.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		f := raw.Files[p]
		fc := suite.File(p)
		for _, l := range f.MissingLines {
			fc.Lines[l] += 0
		}
		for _, l := range f.ExecutedLines {
			fc.Lines[l]++
		}
		for lineKey, ctxs := range f.Contexts {
			l, err := strconv.Atoi(lineKey)
			if err != nil {
				continue
			}
			for _, c := range ctxs {
				name, ok := testContext(c)
				if !ok {
					continue
				}
				r, seen := contexts[name]
				if !seen {
					r = NewReport(FormatCoveragePy)
					r.Universe = suite.Files
					contexts[name] = r
				}
				cf := r.File(p)
				if len(cf.Lines) == 0 {
					// Every measured line of the file is instrumented for
					// this test too; it hit only the lines it ran.
					for _, ml := range f.MissingLines {
						cf.Lines[ml] += 0
					}
					for _, el := range f.ExecutedLines {
						cf.Lines[el] += 0
					}
				}
				cf.Lines[l]++
			}
		}
	}
	return suite, contexts, nil
}

// testContext normalizes a coverage.py context to a test name: "" is no
// test; pytest-cov's "<nodeid>|run" keeps the run phase only.
func testContext(c string) (string, bool) {
	c = strings.TrimSpace(c)
	if c == "" {
		return "", false
	}
	if base, phase, ok := strings.Cut(c, "|"); ok {
		if phase != "run" {
			return "", false
		}
		c = base
	}
	return c, true
}

type pgCallsJSON struct {
	Format string `json:"format"`
	Runs   []struct {
		Test      string       `json:"test"`
		File      string       `json:"file"`
		Functions []pgFunction `json:"functions"`
		Before    []pgFunction `json:"before"`
	} `json:"runs"`
}

type pgFunction struct {
	Schema   string `json:"schema"`
	Function string `json:"function"`
	Calls    int    `json:"calls"`
}

// pgCallsMarker is the "format" value of the Postgres call-count export.
const pgCallsMarker = "pg_stat_user_functions"

// ParsePGCallsJSON reads the JSON export of pg_stat_user_functions
// snapshots (see docs/user-guide.md): {"format": "pg_stat_user_functions",
// "runs": [{"test"|"file": ..., "functions": [{schema, function, calls}],
// "before": [...]}]}. before, when present, is subtracted.
func ParsePGCallsJSON(data []byte) ([]PGRun, error) {
	var raw pgCallsJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("pg calls json: %w", err)
	}
	if raw.Format != pgCallsMarker {
		return nil, fmt.Errorf("pg calls json: format %q, want %q", raw.Format, pgCallsMarker)
	}
	var out []PGRun
	for i, run := range raw.Runs {
		if run.Test == "" && run.File == "" {
			return nil, fmt.Errorf("pg calls json: run %d names no test or file", i+1)
		}
		pr := PGRun{Test: run.Test, File: run.File, Calls: map[string]int{}}
		for _, f := range run.Functions {
			pr.Calls[pgFQN(f.Schema, f.Function)] += f.Calls
		}
		for _, f := range run.Before {
			pr.Calls[pgFQN(f.Schema, f.Function)] -= f.Calls
		}
		out = append(out, pr)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("pg calls json: no runs")
	}
	return out, nil
}

// pgReport turns call counts into symbol-level evidence.
func pgReport(calls map[string]int) Report {
	r := NewReport(FormatPGCalls)
	for fqn, n := range calls {
		r.Symbols[fqn] += n
	}
	return r
}
