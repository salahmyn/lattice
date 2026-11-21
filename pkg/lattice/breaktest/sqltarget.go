package breaktest

// @module-feature breaktest

import (
	"fmt"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// What an Altered entry changes (schema.GraphAlteration.Sets), as the
// PostgreSQL adapter writes it.
const (
	alterUsing     = "using"
	alterWithCheck = "with_check"
)

// fileEdit is one break placed in one file: the file as the worktree holds it
// and the insertions to make. line is where the edited statement starts.
type fileEdit struct {
	file  string
	src   []byte
	brk   Break
	line  int
	alter bool // the statement is an ALTER, not the symbol's CREATE
}

// breakPlan is where a generated break goes. Most breaks are one edit of the
// enforcer's own file. A SQL object that later statements changed is broken
// where its effective definition is set, which may be another file, or two
// statements in two files (a policy whose USING and WITH CHECK were last set
// by different statements).
type breakPlan struct {
	edits []fileEdit
	// swapWhy says why the object swap ({patched_sql}, {original_sql}) is not
	// offered, ": ..." after sqlUnavailable. Empty when it is.
	swapWhy string
}

// symbolOf is the generator's view of a graph symbol.
func symbolOf(s schema.GraphSymbol) Symbol {
	return Symbol{Name: s.Name, Kind: s.Kind, Line: s.Line, DeclaredAs: s.DeclaredAs, Altered: s.Altered}
}

// planBreak generates the break for the enforcer symbol sym, whose own
// statement is in file (src is its text); read gives any other file of the
// worktree. Only a SQL object that later statements changed leaves its file.
// @enforces breaktest:INV-17
func planBreak(language, file string, src []byte, sym Symbol, read func(file string) ([]byte, error)) (breakPlan, error) {
	if language != "postgres" && language != "sql" {
		brk, err := Generate(language, file, src, sym)
		if err != nil {
			return breakPlan{}, err
		}
		return breakPlan{edits: []fileEdit{{file: file, src: src, brk: brk, line: sym.Line}}}, nil
	}
	return planSQL(file, src, sym, read)
}

// planSQL is planBreak for a SQL symbol.
//
// Migrations apply in the order the PostgreSQL adapter resolves them: by file
// name, then by position in the file. sym.Altered is that order, so the last
// entry that sets an expression is the statement whose value the database
// ends up with.
func planSQL(file string, src []byte, sym Symbol, read func(string) ([]byte, error)) (breakPlan, error) {
	st, kind, next, err := locateSQL(src, sym)
	if err != nil {
		return breakPlan{}, err
	}
	create := fileEdit{file: file, src: src, line: sym.Line}
	why := ""
	if len(sym.Altered) > 0 {
		a := sym.Altered[0]
		why = fmt.Sprintf(": altered later in %s:%d", a.File, a.Line)
	}
	if kind != sqlPolicy || len(sym.Altered) == 0 {
		brk, err := generateSQLAt(st, kind, next)
		if err != nil {
			return breakPlan{}, err
		}
		create.brk = brk
		return breakPlan{edits: []fileEdit{create}, swapWhy: why}, nil
	}

	// The statement that sets each expression: the last ALTER that names it,
	// else the CREATE (index -1).
	last := func(set string) int {
		at := -1
		for i, a := range sym.Altered {
			for _, x := range a.Sets {
				if x == set {
					at = i
				}
			}
		}
		return at
	}
	type want struct{ using, check bool }
	wants := map[int]*want{}
	var order []int
	add := func(at int, set string) {
		if wants[at] == nil {
			wants[at] = &want{}
			order = append(order, at)
		}
		if set == alterUsing {
			wants[at].using = true
		} else {
			wants[at].check = true
		}
	}
	add(last(alterUsing), alterUsing)
	add(last(alterWithCheck), alterWithCheck)

	var edits []fileEdit
	var firstErr error // set when a statement had none of the clauses asked for
	for _, at := range order {
		w := wants[at]
		var e fileEdit
		var brk Break
		var err error
		if at < 0 {
			e = create
			brk, err = st.policyBreakOf(next, w.using, w.check)
		} else {
			a := sym.Altered[at]
			var asrc []byte
			if a.File == file {
				asrc = src
			} else if asrc, err = read(a.File); err != nil {
				return breakPlan{}, noBreak("%s unreadable: %v; add a script", a.File, err)
			}
			ast, anext, lerr := locateAlterPolicy(asrc, a.Line)
			if lerr != nil {
				return breakPlan{}, lerr
			}
			e = fileEdit{file: a.File, src: asrc, line: a.Line, alter: true}
			brk, err = ast.policyBreakOf(anext, w.using, w.check)
		}
		if err != nil {
			// A statement with none of the clauses asked for adds nothing;
			// the other statements may still carry the policy's expressions.
			// Any other failure means the statement cannot be broken exactly.
			if nb, ok := err.(*NoBreak); !ok || nb.Reason != noPolicyExpr {
				return breakPlan{}, err
			}
			firstErr = err
			continue
		}
		e.brk = brk
		edits = append(edits, e)
	}
	if len(edits) == 0 {
		if firstErr == nil {
			firstErr = noBreak(noPolicyExpr)
		}
		return breakPlan{}, firstErr
	}

	// The swap re-applies one statement. That is safe for the ALTER that is
	// the last statement touching the policy; the CREATE is not, because
	// later statements would be lost; and two statements cannot be swapped as
	// one.
	switch {
	case len(edits) > 1:
		why = fmt.Sprintf(": the break edits %d statements (%s)", len(edits), editSpots(edits))
	case !edits[0].alter:
		// why already names the first later statement.
	case edits[0].line == sym.Altered[len(sym.Altered)-1].Line && edits[0].file == sym.Altered[len(sym.Altered)-1].File:
		why = ""
	default:
		a := sym.Altered[len(sym.Altered)-1]
		why = fmt.Sprintf(": altered again later in %s:%d", a.File, a.Line)
	}
	return breakPlan{edits: edits, swapWhy: why}, nil
}

// generateSQLAt is generateSQL for a statement already located.
func generateSQLAt(st sqlStmt, kind sqlEnforcer, next int) (Break, error) {
	switch kind {
	case sqlPolicy:
		return st.policyBreak(next)
	case sqlTrigger:
		return st.triggerBreak(next)
	}
	return st.functionBreak(next, kind == sqlProcedure)
}

func editSpots(edits []fileEdit) string {
	parts := make([]string, len(edits))
	for i, e := range edits {
		parts[i] = fmt.Sprintf("%s:%d", e.file, e.line)
	}
	return strings.Join(parts, ", ")
}

// byFile joins the edits that land in the same file into one, so the file is
// written once with every insertion, and an earlier write is never undone by a
// later one. Insertions are offsets into the file as it was, which Break.Apply
// handles together.
func byFile(edits []fileEdit) []fileEdit {
	var out []fileEdit
	at := map[string]int{}
	for _, e := range edits {
		i, ok := at[e.file]
		if !ok {
			at[e.file] = len(out)
			out = append(out, e)
			continue
		}
		all := append([]Break{{Offset: e.brk.Offset, Text: e.brk.Text}}, e.brk.More...)
		out[i].brk.More = append(append([]Break(nil), out[i].brk.More...), all...)
	}
	return out
}
