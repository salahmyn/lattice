package coverage

// @module-feature coverage

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// ParseLCOV reads an LCOV tracefile (c8, nyc, vitest, Jest, Node's built-in
// runner, Deno, coverage.py `lcov`). Records it does not use (branches,
// summaries) are skipped; Node's BRDA records are noise anyway. Several
// records for one file are merged by adding hits.
func ParseLCOV(data []byte) (Report, error) {
	r := NewReport(FormatLCOV)
	var cur *FileCov
	fnLine := map[string]int{} // function name -> declaration line, per file
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	n := 0
	for sc.Scan() {
		n++
		tag, val, _ := strings.Cut(strings.TrimSpace(sc.Text()), ":")
		switch tag {
		case "SF":
			cur = r.File(val)
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
			// LCOV 2 writes FN:<start>,<end>,<name>.
			if a, b, two := strings.Cut(name, ","); two {
				if _, err := strconv.Atoi(a); err == nil {
					name = b
				}
			}
			l, err := strconv.Atoi(ln)
			if err != nil {
				return Report{}, fmt.Errorf("lcov line %d: bad FN %q", n, val)
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
				return Report{}, fmt.Errorf("lcov line %d: bad FNDA %q", n, val)
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
				return Report{}, fmt.Errorf("lcov line %d: bad DA %q", n, val)
			}
			cur.Lines[l] += h
		}
	}
	if err := sc.Err(); err != nil {
		return Report{}, err
	}
	if len(r.Files) == 0 {
		return Report{}, fmt.Errorf("lcov: no SF records")
	}
	return r, nil
}

// ParseGoProfile reads a `go test -coverprofile` file. Each block
// "<import path>/<file>.go:L1.C1,L2.C2 <stmts> <count>" gives its count to
// every line L1..L2; where blocks share a line the highest count wins, so a
// line is hit when any block on it ran. File keys stay import paths: the
// ingest step joins them to the graph's paths by suffix, so the module need
// not sit at the code root.
func ParseGoProfile(data []byte) (Report, error) {
	r := NewReport(FormatGo)
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
			return Report{}, fmt.Errorf("go profile line %d: no file", n)
		}
		fields := strings.Fields(line[colon+1:])
		if len(fields) != 3 {
			return Report{}, fmt.Errorf("go profile line %d: want 3 fields", n)
		}
		from, to, ok := strings.Cut(fields[0], ",")
		if !ok {
			return Report{}, fmt.Errorf("go profile line %d: bad range", n)
		}
		l1, err1 := strconv.Atoi(strings.SplitN(from, ".", 2)[0])
		l2, err2 := strconv.Atoi(strings.SplitN(to, ".", 2)[0])
		count, err3 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil || err3 != nil {
			return Report{}, fmt.Errorf("go profile line %d: bad numbers", n)
		}
		f := r.File(line[:colon])
		for l := l1; l <= l2; l++ {
			if prev, seen := f.Lines[l]; !seen || count > prev {
				f.Lines[l] = count
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Report{}, err
	}
	return r, nil
}

// PGRun is one snapshot of Postgres function call counts: the calls each
// function took during one pgTAP test (Test) or one test file (File).
type PGRun struct {
	Test  string
	File  string
	Calls map[string]int // sql.<schema>.<function> -> calls
}

// ParsePGCallsCSV reads the CSV export of pg_stat_user_functions snapshots
// (see docs/user-guide.md). The header names the columns: schema, function
// and calls are required, with test or file (or both) to say which run a row
// belongs to; calls_before, when present, is subtracted (a snapshot taken
// before the run instead of a reset).
func ParsePGCallsCSV(data []byte) ([]PGRun, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return nil, fmt.Errorf("pg calls csv: empty")
	}
	col := map[string]int{}
	for i, h := range splitCSV(lines[0]) {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, need := range []string{"schema", "function", "calls"} {
		if _, ok := col[need]; !ok {
			return nil, fmt.Errorf("pg calls csv: no %q column", need)
		}
	}
	_, hasTest := col["test"]
	_, hasFile := col["file"]
	if !hasTest && !hasFile {
		return nil, fmt.Errorf("pg calls csv: needs a test or file column")
	}
	get := func(row []string, name string) string {
		if i, ok := col[name]; ok && i < len(row) {
			return strings.TrimSpace(row[i])
		}
		return ""
	}
	byKey := map[string]*PGRun{}
	var order []string
	for n, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		row := splitCSV(line)
		calls, err := strconv.Atoi(get(row, "calls"))
		if err != nil {
			return nil, fmt.Errorf("pg calls csv line %d: bad calls %q", n+2, get(row, "calls"))
		}
		if b := get(row, "calls_before"); b != "" {
			before, err := strconv.Atoi(b)
			if err != nil {
				return nil, fmt.Errorf("pg calls csv line %d: bad calls_before %q", n+2, b)
			}
			calls -= before
		}
		test, file := get(row, "test"), get(row, "file")
		key := test + "\x00" + file
		run := byKey[key]
		if run == nil {
			run = &PGRun{Test: test, File: file, Calls: map[string]int{}}
			byKey[key] = run
			order = append(order, key)
		}
		run.Calls[pgFQN(get(row, "schema"), get(row, "function"))] += calls
	}
	out := make([]PGRun, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("pg calls csv: no rows")
	}
	return out, nil
}

// pgFQN is the postgres adapter's symbol name for a function.
func pgFQN(schema, fn string) string {
	schema = strings.ToLower(strings.Trim(schema, `" `))
	if schema == "" {
		schema = "public"
	}
	return "sql." + schema + "." + strings.ToLower(strings.Trim(fn, `" `))
}

// splitCSV splits one CSV line, honouring double quotes.
func splitCSV(line string) []string {
	var out []string
	var b strings.Builder
	quoted := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '"' && quoted && i+1 < len(line) && line[i+1] == '"':
			b.WriteByte('"')
			i++
		case c == '"':
			quoted = !quoted
		case c == ',' && !quoted:
			out = append(out, b.String())
			b.Reset()
		default:
			b.WriteByte(c)
		}
	}
	return append(out, b.String())
}
