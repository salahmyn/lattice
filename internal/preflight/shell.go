package preflight

// @module-feature cli

import (
	"regexp"
	"strings"
)

// commandWords returns the executable of each simple command in a shell
// command line, in order: `FOO=1 npm ci && cd web && pnpm build | tee out`
// gives npm, pnpm and tee. It reads only what is certain: quotes are
// honoured, variable assignments and redirections are dropped, shell
// builtins and keywords run nothing to look up, and a construct it does not
// follow (a for or case) is skipped rather than guessed.
func commandWords(line string) []string {
	var out []string
	for _, cmd := range simpleCommands(line) {
		if w := executableOf(cmd); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// simpleCommands splits a command line on ; & | ( ) and newlines, outside
// quotes, into the words of each simple command.
func simpleCommands(line string) [][]string {
	var cmds [][]string
	var words []string
	var cur strings.Builder
	inWord, dropWord, dropNext := false, false, false

	endWord := func() {
		if inWord {
			switch {
			case dropWord:
				dropWord = false
			case dropNext:
				dropNext = false
			default:
				words = append(words, cur.String())
			}
		}
		cur.Reset()
		inWord = false
	}
	endCmd := func() {
		endWord()
		dropNext = false
		if len(words) > 0 {
			cmds = append(cmds, words)
		}
		words = nil
	}

	rs := []rune(line)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '\\' && i+1 < len(rs):
			i++
			cur.WriteRune(rs[i])
			inWord = true
		case c == '\'':
			inWord = true
			for i++; i < len(rs) && rs[i] != '\''; i++ {
				cur.WriteRune(rs[i])
			}
		case c == '"':
			inWord = true
			for i++; i < len(rs) && rs[i] != '"'; i++ {
				if rs[i] == '\\' && i+1 < len(rs) {
					i++
				}
				cur.WriteRune(rs[i])
			}
		case c == '#' && !inWord:
			for i < len(rs) && rs[i] != '\n' {
				i++
			}
			endCmd()
		case c == ' ' || c == '\t':
			endWord()
		case c == '>' || c == '<':
			// A redirection: the fd digits before it and the target after it
			// are not words of the command.
			if inWord && isDigits(cur.String()) {
				dropWord = true
			}
			endWord()
			for i+1 < len(rs) && strings.ContainsRune("<>&|", rs[i+1]) {
				i++
			}
			dropNext = true
		case c == ';' || c == '&' || c == '|' || c == '\n' || c == '(' || c == ')':
			endCmd()
		default:
			cur.WriteRune(c)
			inWord = true
		}
	}
	endCmd()
	return cmds
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

var assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// prefixes run the command that follows them.
var prefixes = map[string]bool{
	"exec": true, "nohup": true, "time": true, "env": true, "sudo": true,
	"if": true, "then": true, "else": true, "elif": true, "do": true,
	"while": true, "until": true, "!": true, "{": true,
}

// builtins run nothing to look up on PATH; skipped constructs (for, case,
// function definitions) end the reading of their command.
var builtins = map[string]bool{
	"cd": true, "export": true, "set": true, "unset": true, "true": true, "false": true,
	"echo": true, "printf": true, "test": true, "[": true, "[[": true, "exit": true,
	"source": true, ".": true, "eval": true, "fi": true, "done": true, "esac": true,
	"for": true, "case": true, "in": true, "}": true, "read": true, "shift": true,
	"trap": true, "wait": true, "ulimit": true, "umask": true, "local": true,
	"return": true, ":": true, "pwd": true, "type": true, "alias": true,
	"command": true, "hash": true, "pushd": true, "popd": true, "function": true,
	"break": true, "continue": true, "readonly": true, "declare": true, "let": true,
}

// executableOf returns the executable a simple command runs, or "".
func executableOf(words []string) string {
	for i := 0; i < len(words); i++ {
		w := words[i]
		switch {
		case assignment.MatchString(w):
			continue
		case prefixes[w]:
			// Options of a prefix (env -i, sudo -u root) come before the command.
			for i+1 < len(words) && strings.HasPrefix(words[i+1], "-") {
				i++
				if (w == "sudo" || w == "env") && (words[i] == "-u" || words[i] == "-g" || words[i] == "-C") {
					i++
				}
			}
			continue
		case builtins[w]:
			return ""
		}
		return w
	}
	return ""
}
