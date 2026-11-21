package postgres

// @module-feature adapters.postgres

import (
	"regexp"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

var (
	rePgNotify = regexp.MustCompile(`(?is)\b(?:pg_catalog\s*\.\s*)?pg_notify\s*\(`)
	rePgmq     = regexp.MustCompile(`(?is)\bpgmq\s*\.\s*(send_batch|send|read_with_poll|read|pop)\s*\(`)
	// reNotify is the NOTIFY statement of a body: it must start a statement
	// (after a semicolon, begin, then, else, loop or the opening of a dollar
	// quote), so a column or a word that happens to be called notify is not
	// one. NOTIFY takes an identifier, never an expression.
	reNotify   = regexp.MustCompile(`(?is)(?:^|;|\bbegin\b|\bthen\b|\belse\b|\bloop\b|\$)\s*notify\s+("(?:[^"]|"")+"|[A-Za-z_][A-Za-z0-9_$]*)\s*(?:,|;|$|\$)`)
	reQueueArg = regexp.MustCompile(`(?is)^queue_name\s*=>\s*(.*)$`)
)

// channelRefs reads the channel ends of a statement's text, as refs:
//
//   - pg_notify('c', ...) and NOTIFY c publish to notify:c;
//   - pgmq.send('q', ...) and pgmq.send_batch('q', ...) publish to queue:q;
//   - pgmq.read('q', ...), pgmq.read_with_poll and pgmq.pop('q') consume
//     queue:q: the function that holds the call is the consumer.
//
// A channel name that is not a string literal gives a ref with no name, which
// the core counts as dynamic. LISTEN is no end: it names a channel but no
// handler. Comments, and string literals unless withStrings, are skipped, so
// a call quoted in a message makes nothing.
func channelRefs(text string, line int, withStrings bool) []ir.Ref {
	code := maskSQL(text, withStrings)
	at := func(i int) int { return line + strings.Count(text[:i], "\n") }
	var out []ir.Ref
	for _, m := range rePgNotify.FindAllStringIndex(code, -1) {
		out = append(out, ir.Ref{Name: ir.ChannelKey(ir.ChannelNotify, literalArg(text, m[1]-1, 0)), Kind: ir.RefPublish, Line: at(m[0])})
	}
	for _, m := range rePgmq.FindAllStringSubmatchIndex(code, -1) {
		fn := strings.ToLower(code[m[2]:m[3]])
		kind := ir.RefPublish
		if fn == "read" || fn == "read_with_poll" || fn == "pop" {
			kind = ir.RefConsume
		}
		out = append(out, ir.Ref{Name: ir.ChannelKey(ir.ChannelQueue, literalArg(text, m[1]-1, 0)), Kind: kind, Line: at(m[0])})
	}
	for _, m := range reNotify.FindAllStringSubmatchIndex(code, -1) {
		name := text[m[2]:m[3]]
		if strings.HasPrefix(name, `"`) {
			name = strings.ReplaceAll(strings.Trim(name, `"`), `""`, `"`)
		} else {
			name = strings.ToLower(name) // an unquoted identifier folds to lower case
		}
		out = append(out, ir.Ref{Name: ir.ChannelKey(ir.ChannelNotify, name), Kind: ir.RefPublish, Line: at(m[2])})
	}
	return out
}

// literalArg returns the string literal at argument index i of the call whose
// opening parenthesis is at text[open], or "" when it is not one. A named
// argument (queue_name => 'q') is read through its name.
func literalArg(text string, open, i int) string {
	args := callArgs(text[open:])
	if i >= len(args) {
		return ""
	}
	a := args[i]
	if m := reQueueArg.FindStringSubmatch(a); m != nil {
		a = m[1]
	} else {
		for _, o := range args {
			if m := reQueueArg.FindStringSubmatch(o); m != nil {
				a = m[1]
				break
			}
		}
	}
	lit, ok := stringLiteral(a)
	if !ok {
		return ""
	}
	return lit
}

// maskSQL returns text with every comment, and every single-quoted string
// unless keepStrings, replaced by spaces of the same length (newlines are
// kept), so an offset in the result is an offset in text.
func maskSQL(text string, keepStrings bool) string {
	b := []byte(text)
	n := len(b)
	blank := func(from, to int) {
		for k := from; k < to && k < n; k++ {
			if b[k] != '\n' {
				b[k] = ' '
			}
		}
	}
	for i := 0; i < n; {
		switch {
		case b[i] == '-' && i+1 < n && b[i+1] == '-':
			j := i
			for j < n && b[j] != '\n' {
				j++
			}
			blank(i, j)
			i = j
		case b[i] == '/' && i+1 < n && b[i+1] == '*':
			end := strings.Index(text[i+2:], "*/")
			j := n
			if end >= 0 {
				j = i + 2 + end + 2
			}
			blank(i, j)
			i = j
		case b[i] == '\'' && !keepStrings:
			j := i + 1
			for j < n {
				if b[j] == '\'' {
					if j+1 < n && b[j+1] == '\'' {
						j += 2
						continue
					}
					break
				}
				j++
			}
			blank(i, j+1)
			i = j + 1
		case b[i] == '$':
			if tag, ok := dollarTag(text[i:]); ok {
				i += len(tag) // a body is code: read on
				continue
			}
			i++
		default:
			i++
		}
	}
	return string(b)
}
