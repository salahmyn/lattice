package coverage

// @module-feature coverage

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
)

// xmlAttr returns an element's attribute value.
func xmlAttr(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// ParseCobertura reads a cobertura XML report (coverage.py xml, c8/nyc
// --reporter cobertura, gcovr, JaCoCo converters). A class's filename is
// joined to the first <source> when it is relative; the ingest step then
// joins the path to the graph by suffix. A <method> with a hits attribute
// is a function record at its first line.
func ParseCobertura(data []byte) (Report, error) {
	r := NewReport(FormatCobertura)
	dec := xml.NewDecoder(bytes.NewReader(data))
	var source string
	var cur *FileCov
	inSource, inMethod, methodLine, methodHits := false, false, 0, -1
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Report{}, fmt.Errorf("cobertura xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "source":
				inSource = source == ""
			case "class":
				name := xmlAttr(t, "filename")
				if source != "" && !path.IsAbs(name) {
					name = strings.TrimSuffix(source, "/") + "/" + name
				}
				cur = r.File(name)
			case "method":
				inMethod, methodLine, methodHits = true, 0, -1
				if h, err := strconv.Atoi(xmlAttr(t, "hits")); err == nil {
					methodHits = h
				}
			case "line":
				if cur == nil {
					continue
				}
				l, err1 := strconv.Atoi(xmlAttr(t, "number"))
				h, err2 := strconv.Atoi(xmlAttr(t, "hits"))
				if err1 != nil || err2 != nil {
					continue
				}
				if inMethod {
					if methodLine == 0 {
						methodLine = l
					}
					continue // the class's own <lines> repeat these
				}
				if prev, seen := cur.Lines[l]; !seen || h > prev {
					cur.Lines[l] = h
				}
			}
		case xml.CharData:
			if inSource {
				source = strings.TrimSpace(string(t))
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "source":
				inSource = false
			case "method":
				if cur != nil && methodLine > 0 && methodHits >= 0 {
					cur.Funcs[methodLine] += methodHits
				}
				inMethod = false
			case "class":
				cur = nil
			}
		}
	}
	if len(r.Files) == 0 {
		return Report{}, fmt.Errorf("cobertura xml: no classes")
	}
	return r, nil
}

// ParseClover reads a clover XML report (c8/nyc --reporter clover, PHPUnit
// --coverage-clover). <line type="stmt"|"cond"> lines carry hit counts;
// <line type="method"> is a function record at its declaration line.
func ParseClover(data []byte) (Report, error) {
	r := NewReport(FormatClover)
	dec := xml.NewDecoder(bytes.NewReader(data))
	var cur *FileCov
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Report{}, fmt.Errorf("clover xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "file":
				name := xmlAttr(t, "path")
				if name == "" {
					name = xmlAttr(t, "name")
				}
				cur = r.File(name)
			case "line":
				if cur == nil {
					continue
				}
				l, err1 := strconv.Atoi(xmlAttr(t, "num"))
				h, err2 := strconv.Atoi(xmlAttr(t, "count"))
				if err1 != nil || err2 != nil {
					continue
				}
				if xmlAttr(t, "type") == "method" {
					cur.Funcs[l] += h
					continue
				}
				if prev, seen := cur.Lines[l]; !seen || h > prev {
					cur.Lines[l] = h
				}
			}
		case xml.EndElement:
			if t.Name.Local == "file" {
				cur = nil
			}
		}
	}
	if len(r.Files) == 0 {
		return Report{}, fmt.Errorf("clover xml: no files")
	}
	return r, nil
}
