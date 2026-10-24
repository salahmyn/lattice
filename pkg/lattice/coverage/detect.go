package coverage

// @module-feature coverage

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// Detect names the coverage format of a report from its content, or ""
// when the content is not a coverage report (a JUnit or TAP test report,
// say). It reads the content, never the file name, the way `results
// ingest` tells JUnit from TAP:
//
//   - Go: the first line is "mode: set|count|atomic";
//   - LCOV: the first record is TN: or SF:;
//   - JSON: coverage.py has meta and files; the Postgres export has
//     "format": "pg_stat_user_functions"; istanbul maps files to
//     objects with a statementMap;
//   - XML: a <coverage> root holding <project> is clover, any other
//     <coverage> root is cobertura;
//   - CSV: a header with schema, function and calls columns is the
//     Postgres export.
//
// @enforces coverage:INV-1
func Detect(data []byte) Format {
	text := strings.TrimSpace(strings.TrimPrefix(string(data), "\xef\xbb\xbf"))
	if text == "" {
		return ""
	}
	first, _, _ := strings.Cut(text, "\n")
	first = strings.TrimSpace(first)
	switch {
	case strings.HasPrefix(first, "mode:"):
		return FormatGo
	case strings.HasPrefix(first, "TN:") || strings.HasPrefix(first, "SF:"):
		return FormatLCOV
	case text[0] == '{':
		return detectJSON(data)
	case text[0] == '<':
		return detectXML(data)
	}
	cols := map[string]bool{}
	for _, c := range splitCSV(first) {
		cols[strings.ToLower(strings.TrimSpace(c))] = true
	}
	if cols["schema"] && cols["function"] && cols["calls"] {
		return FormatPGCalls
	}
	return ""
}

func detectJSON(data []byte) Format {
	var top map[string]json.RawMessage
	if json.Unmarshal(data, &top) != nil {
		return ""
	}
	if _, ok := top["meta"]; ok {
		if _, ok := top["files"]; ok {
			return FormatCoveragePy
		}
	}
	if raw, ok := top["format"]; ok {
		var f string
		if json.Unmarshal(raw, &f) == nil && f == pgCallsMarker {
			return FormatPGCalls
		}
	}
	for _, raw := range top {
		var file struct {
			StatementMap json.RawMessage `json:"statementMap"`
		}
		if json.Unmarshal(raw, &file) == nil && file.StatementMap != nil {
			return FormatIstanbul
		}
		break
	}
	return ""
}

func detectXML(data []byte) Format {
	dec := xml.NewDecoder(bytes.NewReader(data))
	depth := 0
	isCoverage := false
	for {
		tok, err := dec.Token()
		if err != nil {
			if err == io.EOF && isCoverage {
				return FormatCobertura
			}
			return ""
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth == 1 {
				if t.Name.Local != "coverage" {
					return ""
				}
				isCoverage = true
				continue
			}
			if depth == 2 {
				if t.Name.Local == "project" {
					return FormatClover
				}
				return FormatCobertura
			}
		case xml.EndElement:
			depth--
		}
	}
}

// Parsed is one report read from disk: a coverage report, or per-test data
// (coverage.py contexts, Postgres call-count runs) that one file can hold.
type Parsed struct {
	Format   Format
	Report   Report
	Contexts map[string]Report // coverage.py contexts: test context -> its lines
	PGRuns   []PGRun           // Postgres call counts per test or test file
}

// Parse reads a report in the given format ("" detects it).
func Parse(data []byte, f Format) (Parsed, error) {
	if f == "" {
		f = Detect(data)
	}
	p := Parsed{Format: f}
	var err error
	switch f {
	case FormatLCOV:
		p.Report, err = ParseLCOV(data)
	case FormatGo:
		p.Report, err = ParseGoProfile(data)
	case FormatIstanbul:
		p.Report, err = ParseIstanbul(data)
	case FormatCoveragePy:
		p.Report, p.Contexts, err = ParseCoveragePy(data)
	case FormatCobertura:
		p.Report, err = ParseCobertura(data)
	case FormatClover:
		p.Report, err = ParseClover(data)
	case FormatPGCalls:
		text := strings.TrimSpace(string(data))
		if strings.HasPrefix(text, "{") {
			p.PGRuns, err = ParsePGCallsJSON(data)
		} else {
			p.PGRuns, err = ParsePGCallsCSV(data)
		}
		if err == nil {
			all := map[string]int{}
			for _, run := range p.PGRuns {
				for fqn, n := range run.Calls {
					all[fqn] += n
				}
			}
			p.Report = pgReport(all)
		}
	case "":
		return p, fmt.Errorf("not a coverage report (lcov, go, istanbul, coveragepy, cobertura, clover or pg_stat_functions)")
	default:
		return p, fmt.Errorf("unknown coverage format %q (want one of lcov, go, istanbul, coveragepy, cobertura, clover, pg_stat_functions)", f)
	}
	return p, err
}
