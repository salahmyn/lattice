package postgres

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/astutil"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// reSelectCall matches a top-level "select fn(" with an unqualified function.
var reSelectCall = regexp.MustCompile(`(?is)^select\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)

// notAssertions are pgTAP and helper calls that print no test line.
var notAssertions = map[string]bool{
	"plan": true, "no_plan": true, "finish": true, "diag": true, "set_config": true,
	"current_setting": true, "todo": true, "todo_start": true, "todo_end": true, "in_todo": true,
	"num_failed": true, "pg_sleep": true,
}

// parseTests reads a pgTAP file: every "select <assertion>(..., 'description')"
// is a test whose FQN is <file>.test.<slug of the description>, the key TAP and
// JUnit results join on. Tags above select plan(...) apply to every assertion
// in the file; tags above an assertion apply to it.
func parseTests(mod *ir.Module, stmts []statement) {
	modulePath := astutil.ModulePath(mod.File)
	counts := map[string]int{}
	var fileTags []ir.Annotation
	for _, st := range stmts {
		anns := tags(mod, attached(st))
		m := reSelectCall.FindStringSubmatch(st.text)
		if m == nil {
			orphan(mod, anns, st)
			continue
		}
		fn := strings.ToLower(m[1])
		if fn == "plan" || fn == "no_plan" {
			fileTags = append(fileTags, anns...)
			continue
		}
		if notAssertions[fn] {
			orphan(mod, anns, st)
			continue
		}
		args := callArgs(st.text)
		desc := ""
		if len(args) > 0 {
			desc, _ = stringLiteral(args[len(args)-1])
		}
		if strings.TrimSpace(desc) == "" {
			if len(anns) > 0 {
				mod.Diagnostics = append(mod.Diagnostics, ir.Diagnostic{
					Line: anns[0].Line, Code: schema.CodeAnnotationOrphaned,
					Message: fmt.Sprintf("this %s() assertion has no description, so its result cannot be joined; give it one as the last argument", fn),
				})
			}
			continue
		}
		base := modulePath + ".test." + slugify(desc)
		fqn := base
		if c := counts[base]; c > 0 {
			fqn = fmt.Sprintf("%s_%d", base, c+1)
		}
		counts[base]++
		all := append(append([]ir.Annotation{}, fileTags...), anns...)
		mod.Symbols = append(mod.Symbols, ir.Symbol{
			Name: desc, FQN: fqn, Kind: ir.KindFunction, File: mod.File,
			Line: st.line, EndLine: st.endLine, IsTest: true, Annotations: all,
		})
	}
}

// slugify is the test-title slug shared with the TypeScript adapter and the
// results join: lowercase ASCII letters and digits, other runs one dash.
func slugify(s string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	if out := strings.Trim(b.String(), "-"); out != "" {
		return out
	}
	return "test"
}
