// Package coverage is the rnd/0009 PROTOTYPE of coverage ingestion and
// reachability (docs/rnd/0009-coverage-and-reachability.md).
//
// It is not wired into extract, validate, rtm or the CLI, and no other
// package imports it. It exists so the design can be critiqued against real
// coverage files before anything is built. Its finding codes are local
// constants, not schema codes, and every finding is opt-in.
package coverage

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// FileCov is the coverage of one source file.
type FileCov struct {
	// Lines maps a 1-based line to its hit count. A line absent from the map
	// was not instrumented (blank, comment, declaration only).
	Lines map[int]int
	// Funcs maps a function's declaration line to its call count (LCOV
	// FN/FNDA). Empty for formats without function records.
	Funcs map[int]int
	// BranchesFound and BranchesHit count branch records (LCOV BRDA).
	BranchesFound, BranchesHit int
}

// Report is the coverage of one run, keyed by slash-separated file path
// relative to the code root.
type Report map[string]*FileCov

func (r Report) file(p string) *FileCov {
	f := r[p]
	if f == nil {
		f = &FileCov{Lines: map[int]int{}, Funcs: map[int]int{}}
		r[p] = f
	}
	return f
}

// ParseLCOV reads an LCOV tracefile (c8, nyc, vitest, Jest, Node's built-in
// runner, Deno, coverage.py `lcov`, genhtml input). Records it does not use
// are skipped. Several records for one file are merged by adding hits.
func ParseLCOV(data []byte) (Report, error) {
	r := Report{}
	var cur *FileCov
	fnLine := map[string]int{} // function name -> declaration line, per file
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		tag, val, _ := strings.Cut(line, ":")
		switch tag {
		case "SF":
			cur = r.file(val)
			fnLine = map[string]int{}
		case "end_of_record":
			cur = nil
		case "FN":
			if cur == nil {
				continue
			}
			ln, name, ok := strings.Cut(val, ",")
			if !ok {
				continue
			}
			l, err := strconv.Atoi(ln)
			if err != nil {
				return nil, fmt.Errorf("lcov line %d: %w", n, err)
			}
			fnLine[name] = l
			if _, seen := cur.Funcs[l]; !seen {
				cur.Funcs[l] = 0
			}
		case "FNDA":
			if cur == nil {
				continue
			}
			hits, name, ok := strings.Cut(val, ",")
			if !ok {
				continue
			}
			h, err := strconv.Atoi(hits)
			if err != nil {
				return nil, fmt.Errorf("lcov line %d: %w", n, err)
			}
			if l, ok := fnLine[name]; ok {
				cur.Funcs[l] += h
			}
		case "DA":
			if cur == nil {
				continue
			}
			parts := strings.Split(val, ",")
			if len(parts) < 2 {
				continue
			}
			l, err1 := strconv.Atoi(parts[0])
			h, err2 := strconv.Atoi(parts[1])
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("lcov line %d: bad DA %q", n, val)
			}
			cur.Lines[l] += h
		case "BRDA":
			if cur == nil {
				continue
			}
			parts := strings.Split(val, ",")
			if len(parts) != 4 {
				continue
			}
			cur.BranchesFound++
			if parts[3] != "-" && parts[3] != "0" {
				cur.BranchesHit++
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(r) == 0 {
		return nil, fmt.Errorf("lcov: no SF records")
	}
	return r, nil
}

// ParseGoProfile reads a `go test -coverprofile` file. Each block
// "<import path>/<file>.go:L1.C1,L2.C2 <stmts> <count>" gives its count to
// every line L1..L2. modulePath is trimmed from the import path so the file
// joins the Go adapter's repo-relative paths (module at the code root).
func ParseGoProfile(data []byte, modulePath string) (Report, error) {
	r := Report{}
	prefix := strings.TrimSuffix(modulePath, "/") + "/"
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		colon := strings.LastIndex(line, ":")
		if colon < 0 {
			return nil, fmt.Errorf("go profile line %d: no file", n)
		}
		file := strings.TrimPrefix(line[:colon], prefix)
		fields := strings.Fields(line[colon+1:])
		if len(fields) != 3 {
			return nil, fmt.Errorf("go profile line %d: want 3 fields", n)
		}
		from, to, ok := strings.Cut(fields[0], ",")
		if !ok {
			return nil, fmt.Errorf("go profile line %d: bad range", n)
		}
		l1, err1 := strconv.Atoi(strings.SplitN(from, ".", 2)[0])
		l2, err2 := strconv.Atoi(strings.SplitN(to, ".", 2)[0])
		count, err3 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil || err3 != nil {
			return nil, fmt.Errorf("go profile line %d: bad numbers", n)
		}
		f := r.file(file)
		for l := l1; l <= l2; l++ {
			// Blocks of one line can overlap; keep the highest count so a
			// line is hit when any block on it ran.
			if count > f.Lines[l] {
				f.Lines[l] = count
			} else if _, ok := f.Lines[l]; !ok {
				f.Lines[l] = count
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return r, nil
}

// Merge adds other's line and function hits into r (the union of two runs).
// Branch counts are not merged: LCOV numbers branches per run, so two runs'
// counts cannot be added.
func (r Report) Merge(other Report) {
	for p, o := range other {
		f := r.file(p)
		for l, h := range o.Lines {
			f.Lines[l] += h
		}
		for l, h := range o.Funcs {
			f.Funcs[l] += h
		}
	}
}
