package postgres

import "strings"

// comment is one SQL comment ("-- ..." or "/* ... */") outside any statement.
type comment struct {
	text    string
	line    int
	endLine int
}

// statement is one top-level SQL statement, from its first token to its
// terminating semicolon (or the end of the file), with the comments that
// precede it since the previous statement.
type statement struct {
	text     string
	line     int
	endLine  int
	comments []comment
}

// scan splits SQL source into top-level statements. It understands line and
// (nested) block comments, single-quoted strings (with ” and E'\” escapes),
// quoted identifiers and dollar-quoted bodies ($$ ... $$, $tag$ ... $tag$), so a
// semicolon inside a function body never ends the statement.
func scan(src string) []statement {
	var out []statement
	var pending []comment
	var cur strings.Builder
	start, ln := 0, 1
	inStmt := false

	begin := func() {
		if !inStmt {
			inStmt = true
			start = ln
		}
	}
	flush := func(endLine int) {
		if inStmt {
			out = append(out, statement{text: strings.TrimSpace(cur.String()), line: start, endLine: endLine, comments: pending})
			pending = nil
		}
		cur.Reset()
		inStmt = false
	}

	n := len(src)
	for i := 0; i < n; {
		c := src[i]
		switch {
		case c == '\n':
			if inStmt {
				cur.WriteByte(c)
			}
			ln++
			i++
		case c == '-' && i+1 < n && src[i+1] == '-':
			j := strings.IndexByte(src[i:], '\n')
			if j < 0 {
				j = n - i
			}
			if !inStmt {
				pending = append(pending, comment{text: src[i : i+j], line: ln, endLine: ln})
			}
			i += j
		case c == '/' && i+1 < n && src[i+1] == '*':
			j, depth, startLn := i+2, 1, ln
			for j < n && depth > 0 {
				switch {
				case src[j] == '\n':
					ln++
					j++
				case src[j] == '/' && j+1 < n && src[j+1] == '*':
					depth++
					j += 2
				case src[j] == '*' && j+1 < n && src[j+1] == '/':
					depth--
					j += 2
				default:
					j++
				}
			}
			if inStmt {
				cur.WriteString(" ")
			} else {
				pending = append(pending, comment{text: src[i:j], line: startLn, endLine: ln})
			}
			i = j
		case c == ';':
			if inStmt {
				cur.WriteByte(c)
			}
			flush(ln)
			i++
		case c == ' ' || c == '\t' || c == '\r':
			if inStmt {
				cur.WriteByte(c)
			}
			i++
		default:
			begin()
			j := skipToken(src, i, &ln)
			cur.WriteString(src[i:j])
			i = j
		}
	}
	flush(ln)
	return out
}

// skipToken returns the end of the token at i: a quoted string, a quoted
// identifier, a dollar-quoted body, or a single byte. Newlines inside the
// token advance *ln.
func skipToken(src string, i int, ln *int) int {
	n := len(src)
	count := func(s string) { *ln += strings.Count(s, "\n") }
	switch c := src[i]; {
	case c == '\'' || ((c == 'E' || c == 'e') && i+1 < n && src[i+1] == '\''):
		backslash := c != '\''
		j := i + 1
		if backslash {
			j++
		}
		for j < n {
			if backslash && src[j] == '\\' {
				j += 2
				continue
			}
			if src[j] == '\'' {
				if j+1 < n && src[j+1] == '\'' {
					j += 2
					continue
				}
				j++
				break
			}
			j++
		}
		if j > n {
			j = n
		}
		count(src[i:j])
		return j
	case c == '"':
		j := i + 1
		for j < n {
			if src[j] == '"' {
				if j+1 < n && src[j+1] == '"' {
					j += 2
					continue
				}
				j++
				break
			}
			j++
		}
		count(src[i:j])
		return j
	case c == '$':
		if tag, ok := dollarTag(src[i:]); ok {
			end := strings.Index(src[i+len(tag):], tag)
			j := n
			if end >= 0 {
				j = i + len(tag) + end + len(tag)
			}
			count(src[i:j])
			return j
		}
	}
	return i + 1
}

// dollarTag returns the opening "$tag$" (or "$$") at the start of s.
func dollarTag(s string) (string, bool) {
	if len(s) < 2 || s[0] != '$' {
		return "", false
	}
	for j := 1; j < len(s); j++ {
		c := s[j]
		if c == '$' {
			return s[:j+1], true
		}
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || j > 1 && c >= '0' && c <= '9') {
			return "", false
		}
	}
	return "", false
}

// attached returns the comments directly above a statement: the run of
// comments that ends on the line before it (or on its line), with no blank
// line in between.
func attached(st statement) []comment {
	want := st.line - 1
	var run []comment
	for i := len(st.comments) - 1; i >= 0; i-- {
		c := st.comments[i]
		if c.endLine != want && c.endLine != st.line {
			break
		}
		run = append([]comment{c}, run...)
		want = c.line - 1
	}
	return run
}

// callArgs splits the arguments of the first call in s ("fn(a, 'b', f(c))")
// at top-level commas, honouring quotes, dollar bodies and nested parens.
func callArgs(s string) []string {
	open := strings.IndexByte(s, '(')
	if open < 0 {
		return nil
	}
	var args []string
	depth, last, ln := 0, open+1, 0
	for i := open; i < len(s); {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				if a := strings.TrimSpace(s[last:i]); a != "" || len(args) > 0 {
					args = append(args, a)
				}
				return args
			}
		case ',':
			if depth == 1 {
				args = append(args, strings.TrimSpace(s[last:i]))
				last = i + 1
			}
		case '\'', '"', '$', 'E', 'e':
			i = skipToken(s, i, &ln)
			continue
		}
		i++
	}
	return args
}

// stringLiteral returns the value of a single-quoted SQL string literal,
// allowing a trailing ::cast. ok is false for anything else.
func stringLiteral(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "::"); i > 0 && strings.HasSuffix(strings.TrimSpace(s[:i]), "'") {
		s = strings.TrimSpace(s[:i])
	}
	backslash := false
	if strings.HasPrefix(s, "E'") || strings.HasPrefix(s, "e'") {
		backslash, s = true, s[1:]
	}
	if len(s) < 2 || s[0] != '\'' || s[len(s)-1] != '\'' {
		return "", false
	}
	body := strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	if backslash {
		body = strings.ReplaceAll(body, `\'`, `'`)
	}
	return body, true
}
