package breaktest

// @module-feature breaktest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The placeholders a runner command may use for a SQL break, in addition to
// {files}, {filter} and {junit}.
const (
	patchedSQLToken  = "{patched_sql}"
	originalSQLToken = "{original_sql}"
)

// sqlUnavailable is the reason of a break whose command asks for the SQL
// object files and the break has none to give.
const sqlUnavailable = patchedSQLToken + " not available for this break"

// sqlFiles are the two files a SQL break offers a runner: the path of a
// .sql file holding the broken object's statement, and of one holding the
// same statement unbroken. The zero value means there is nothing to fill.
type sqlFiles struct {
	patched, original string
}

func (f sqlFiles) ok() bool { return f.patched != "" && f.original != "" }

// usesSQLFiles reports whether the group's command names {patched_sql} or
// {original_sql}.
// @enforces breaktest:INV-15
func (g group) usesSQLFiles() bool {
	has := func(s string) bool {
		return strings.Contains(s, patchedSQLToken) || strings.Contains(s, originalSQLToken)
	}
	if g.kind == kindShell {
		return has(g.tmpl)
	}
	for _, a := range g.runner.Command {
		if has(a) {
			return true
		}
	}
	return false
}

// anyUsesSQLFiles reports whether any group's command names the SQL files.
func anyUsesSQLFiles(groups []group) bool {
	for _, g := range groups {
		if g.usesSQLFiles() {
			return true
		}
	}
	return false
}

// swapStatements isolates the statement that defines an enforcer and returns
// it twice, with the break applied and without. Each text is a complete
// statement, so a runner can apply one with psql -f instead of rebuilding the
// database:
//
//   - a function or procedure is CREATE OR REPLACE (the keywords are added
//     when the migration wrote a plain CREATE);
//   - a policy cannot be replaced in place, so both texts start with
//     DROP POLICY IF EXISTS <name> ON <table>;
//   - a trigger likewise starts with DROP TRIGGER IF EXISTS <name> ON <table>;.
//
// ok is false when the statement cannot be isolated.
// @enforces breaktest:INV-15
func swapStatements(src []byte, brk Break, sym Symbol) (patched, original string, ok bool) {
	st, kind, next, err := locateSQL(src, sym)
	if err != nil || len(st.toks) == 0 {
		return "", "", false
	}
	lo, hi := st.toks[0].start, st.toks[len(st.toks)-1].end
	var extra []Break
	prefix := ""
	switch kind {
	case sqlFunction, sqlProcedure:
		if !st.is(1, "or") {
			extra = append(extra, Break{Offset: st.toks[0].end, Text: " OR REPLACE"})
		}
	case sqlPolicy, sqlTrigger:
		if prefix, ok = st.dropStatement(kind, next); !ok {
			return "", "", false
		}
	}
	inside := func(b Break) bool { return b.Offset >= lo && b.Offset <= hi }
	all := append([]Break{{Offset: brk.Offset, Text: brk.Text}}, brk.More...)
	for _, b := range all {
		if !inside(b) {
			return "", "", false
		}
	}
	patched, ok = applyWithin(src, Break{Offset: brk.Offset, Text: brk.Text, More: append(append([]Break{}, brk.More...), extra...)}, lo, hi)
	if !ok {
		return "", "", false
	}
	if len(extra) == 0 {
		original = string(src[lo:hi])
	} else if original, ok = applyWithin(src, Break{Offset: extra[0].Offset, Text: extra[0].Text}, lo, hi); !ok {
		return "", "", false
	}
	return prefix + patched + ";\n", prefix + original + ";\n", true
}

// swapAlter is swapStatements for a break placed in an ALTER POLICY
// statement: the statement as it is, broken and unbroken. It sets the
// expression and nothing else, so it can be re-applied on a database that
// already has the policy, with no DROP.
// @enforces breaktest:INV-15
func swapAlter(e fileEdit) (patched, original string, ok bool) {
	st, _, err := locateAlterPolicy(e.src, e.line)
	if err != nil || len(st.toks) == 0 {
		return "", "", false
	}
	lo, hi := st.toks[0].start, st.toks[len(st.toks)-1].end
	all := append([]Break{{Offset: e.brk.Offset, Text: e.brk.Text}}, e.brk.More...)
	for _, b := range all {
		if b.Offset < lo || b.Offset > hi {
			return "", "", false
		}
	}
	patched, ok = applyWithin(e.src, e.brk, lo, hi)
	if !ok {
		return "", "", false
	}
	return patched + ";\n", string(e.src[lo:hi]) + ";\n", true
}

// applyWithin applies b to src and returns the text of the span [lo, hi) of
// src as it then reads, given that every insertion lies inside the span.
func applyWithin(src []byte, b Break, lo, hi int) (string, bool) {
	out := b.Apply(src)
	end := hi + len(out) - len(src)
	if lo < 0 || end < lo || end > len(out) {
		return "", false
	}
	return string(out[lo:end]), true
}

// dropStatement is the DROP ... IF EXISTS that makes a policy's or trigger's
// CREATE repeatable, written with the name and table as the migration spelled
// them. next is the index of the token after the table.
func (s sqlStmt) dropStatement(kind sqlEnforcer, next int) (string, bool) {
	word, noun := "policy", "POLICY"
	if kind == sqlTrigger {
		word, noun = "trigger", "TRIGGER"
	}
	k := -1
	for j := 1; j < len(s.toks) && j < 5; j++ {
		if s.is(j, word) {
			k = j
			break
		}
	}
	if k < 0 || k+1 >= len(s.toks) {
		return "", false
	}
	on := k + 2
	if kind == sqlTrigger {
		for on = k + 2; on < len(s.toks) && !s.is(on, "on"); on++ {
		}
	}
	if !s.is(on, "on") || on+1 >= next || next > len(s.toks) {
		return "", false
	}
	text := func(a, b int) string { return string(s.src[s.toks[a].start:s.toks[b].end]) }
	return fmt.Sprintf("DROP %s IF EXISTS %s ON %s;\n", noun, text(k+1, k+1), text(on+1, next-1)), true
}

// swapFiles writes the patched and original statements of a SQL break to
// files beside the worktree, never inside it, and returns their paths. A
// break that is not SQL, a scripted break, and a statement that cannot be
// isolated give the zero value, with why saying so when it is known: the
// object swap is offered only when the broken statement is the last one that
// touches the object, because the original would otherwise undo the later
// statements.
// @enforces breaktest:INV-16
func (r *runner) swapFiles(language string, scripted bool, plan breakPlan, sym Symbol) (f sqlFiles, why string, err error) {
	if scripted || (language != "postgres" && language != "sql") || len(plan.edits) == 0 {
		return sqlFiles{}, "", nil
	}
	if plan.swapWhy != "" {
		return sqlFiles{}, plan.swapWhy, nil
	}
	e := plan.edits[0]
	var patched, original string
	var ok bool
	if e.alter {
		patched, original, ok = swapAlter(e)
	} else {
		patched, original, ok = swapStatements(e.src, e.brk, sym)
	}
	if !ok {
		return sqlFiles{}, "", nil
	}
	r.sqlSeq++
	f = sqlFiles{
		patched:  filepath.Join(r.wt.tmp, fmt.Sprintf("patched-%d.sql", r.sqlSeq)),
		original: filepath.Join(r.wt.tmp, fmt.Sprintf("original-%d.sql", r.sqlSeq)),
	}
	if err := os.WriteFile(f.patched, []byte(patched), 0o644); err != nil {
		return sqlFiles{}, "", err
	}
	if err := os.WriteFile(f.original, []byte(original), 0o644); err != nil {
		return sqlFiles{}, "", err
	}
	return f, "", nil
}

// asBaseline is what a baseline command sees: it has no patched object, so
// the original statement stands for both.
func (f sqlFiles) asBaseline() sqlFiles {
	return sqlFiles{patched: f.original, original: f.original}
}
