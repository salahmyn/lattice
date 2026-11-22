package graph

import (
	"fmt"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// noDefault is sql.default_write: [], the platform grants no role a table privilege.
var noDefault = &[]string{}

const wRef = "queue.jobs:INV-1"

func upd(table string, line int, cols ...string) ir.TableWrite {
	return ir.TableWrite{Table: table, Op: ir.WriteUpdate, Columns: cols, Line: line}
}

func fn(fqn string, line int, writes ...ir.TableWrite) ir.Symbol {
	return ir.Symbol{FQN: fqn, File: "m.sql", Line: line, Writes: writes, Execute: &ir.Execute{SecurityDefiner: true}}
}

func guardSym(fqn, kind, table string, cmds []string, cols ...string) ir.Symbol {
	return ir.Symbol{FQN: fqn, File: "m.sql", Line: 1, Guard: &ir.Guard{Kind: kind, Table: table, Commands: cmds, Columns: cols}}
}

// enforcing builds the graph symbols that enforce wRef.
func enforcing(fqns ...string) schema.KnowledgeGraph {
	var kg schema.KnowledgeGraph
	for _, f := range fqns {
		kg.Symbols = append(kg.Symbols, schema.GraphSymbol{FQN: f, Feature: "queue.jobs", EnforcesInvariants: []string{wRef}})
	}
	return kg
}

func codesOf(v []schema.Violation) []string {
	var out []string
	for _, f := range v {
		out = append(out, f.Code+" "+f.Subject)
	}
	return out
}

func grantOf(role, op string, state string, line int, cols ...string) ir.TableGrant {
	return ir.TableGrant{Table: "public.jobs", Role: role, Op: op, Columns: cols, State: state, File: "m.sql", Line: line}
}

// The columns of an invariant are the columns its enforcers write. A second
// writer of one of them that is not an enforcer is UNGUARDED_WRITER, listing
// the shared columns with their file and line; one that writes other columns
// only, or that is itself an enforcer, is not.
// @verifies graph:INV-7
func TestUnguardedWriterSharedColumns(t *testing.T) {
	mods := []ir.Module{{File: "m.sql", Symbols: []ir.Symbol{
		fn("sql.public.fail_job", 5, upd("public.jobs", 7, "status", "updated_at")),
		fn("sql.public.claim", 10, upd("public.jobs", 12, "attempts", "status", "updated_at")),
		fn("sql.public.touch", 20, upd("public.jobs", 22, "note")),
	}}}
	v := CheckWriters(enforcing("sql.public.fail_job"), mods, nil, noDefault)
	if len(v) != 1 || v[0].Code != schema.CodeUnguardedWriter || v[0].Subject != "sql.public.claim" || v[0].Severity != schema.SeverityInfo {
		t.Fatalf("findings = %v", codesOf(v))
	}
	for _, want := range []string{"status (m.sql:12)", "updated_at (m.sql:12)", "public.jobs by update"} {
		if !strings.Contains(v[0].Message, want) {
			t.Errorf("message %q lacks %q", v[0].Message, want)
		}
	}
	if strings.Contains(v[0].Message, "attempts") {
		t.Errorf("attempts is not written by an enforcer: %q", v[0].Message)
	}
	if v[0].FeatureID != "queue.jobs" || v[0].InvariantID != "INV-1" || v[0].Location.Line != 12 {
		t.Errorf("finding = %+v", v[0])
	}
	// Once claim is an enforcer too, it writes the columns the invariant is
	// about and is guarded.
	if v := CheckWriters(enforcing("sql.public.fail_job", "sql.public.claim"), mods, nil, noDefault); len(v) != 0 {
		t.Errorf("an enforcer is never an unguarded writer: %v", codesOf(v))
	}
}

// A trigger that enforces the invariant guards every writer of its table for
// its commands; a policy guards only API roles, since a SECURITY DEFINER
// function bypasses row-level security.
// @verifies graph:INV-7
func TestWriterGuardedByTriggerOrPolicy(t *testing.T) {
	base := func(g ir.Symbol, grants ...ir.TableGrant) ([]ir.Module, schema.KnowledgeGraph) {
		mods := []ir.Module{{File: "m.sql", TableGrants: grants, Symbols: []ir.Symbol{
			fn("sql.public.fail_job", 5, upd("public.jobs", 7, "status")),
			fn("sql.public.claim", 10, upd("public.jobs", 12, "status")),
			g,
		}}}
		return mods, enforcing("sql.public.fail_job", g.FQN)
	}
	got := func(mods []ir.Module, kg schema.KnowledgeGraph) string {
		return strings.Join(codesOf(CheckWriters(kg, mods, []string{"authenticated"}, noDefault)), ", ")
	}

	mods, kg := base(guardSym("sql.trigger.public.jobs.t", ir.GuardTrigger, "public.jobs", []string{"update"}))
	if g := got(mods, kg); g != "" {
		t.Errorf("a trigger on update guards the function's update: %s", g)
	}
	mods, kg = base(guardSym("sql.trigger.public.jobs.t", ir.GuardTrigger, "public.jobs", []string{"insert"}))
	if g := got(mods, kg); g != "UNGUARDED_WRITER sql.public.claim" {
		t.Errorf("a trigger on insert does not guard an update: %s", g)
	}
	mods, kg = base(guardSym("sql.trigger.public.jobs.t", ir.GuardTrigger, "public.jobs", []string{"update"}, "attempts"))
	if g := got(mods, kg); g != "UNGUARDED_WRITER sql.public.claim" {
		t.Errorf("a trigger on update of attempts does not fire for status: %s", g)
	}
	mods, kg = base(guardSym("sql.trigger.public.other.t", ir.GuardTrigger, "public.other", []string{"update"}))
	if g := got(mods, kg); g != "UNGUARDED_WRITER sql.public.claim" {
		t.Errorf("a trigger on another table guards nothing: %s", g)
	}
	// A FOR UPDATE policy does not guard a SECURITY DEFINER function.
	mods, kg = base(guardSym("sql.policy.public.jobs.p", ir.GuardPolicy, "public.jobs", []string{"update"}))
	if g := got(mods, kg); g != "UNGUARDED_WRITER sql.public.claim" {
		t.Errorf("a policy does not guard a function: %s", g)
	}
}

// An API role holding UPDATE (column) on the table is a writer of that column:
// unguarded without an enforcing policy for the command, guarded with one
// FOR UPDATE, and still unguarded under a policy for another command.
// @verifies graph:INV-7
func TestRoleWriterFromTableGrant(t *testing.T) {
	grants := []ir.TableGrant{grantOf("authenticated", "update", ir.ExecGranted, 30, "status")}
	build := func(policy *ir.Symbol) ([]ir.Module, schema.KnowledgeGraph) {
		syms := []ir.Symbol{fn("sql.public.fail_job", 5, upd("public.jobs", 7, "status"))}
		kg := enforcing("sql.public.fail_job")
		if policy != nil {
			syms = append(syms, *policy)
			kg = enforcing("sql.public.fail_job", policy.FQN)
		}
		return []ir.Module{{File: "m.sql", TableGrants: grants, Symbols: syms}}, kg
	}
	mods, kg := build(nil)
	v := CheckWriters(kg, mods, []string{"anon", "authenticated"}, noDefault)
	if len(v) != 1 || v[0].Subject != "sql.role.authenticated" {
		t.Fatalf("findings = %v", codesOf(v))
	}
	if !strings.Contains(v[0].Message, "role authenticated") || !strings.Contains(v[0].Message, "status (granted at m.sql:30)") {
		t.Errorf("message = %q", v[0].Message)
	}
	p := guardSym("sql.policy.public.jobs.p", ir.GuardPolicy, "public.jobs", []string{"update"})
	mods, kg = build(&p)
	if v := CheckWriters(kg, mods, []string{"authenticated"}, noDefault); len(v) != 0 {
		t.Errorf("a FOR UPDATE policy that enforces the invariant guards the role: %v", codesOf(v))
	}
	p = guardSym("sql.policy.public.jobs.p", ir.GuardPolicy, "public.jobs", []string{"insert"})
	mods, kg = build(&p)
	if v := CheckWriters(kg, mods, []string{"authenticated"}, noDefault); len(v) != 1 {
		t.Errorf("a FOR INSERT policy does not guard an update: %v", codesOf(v))
	}
	// A grant of another column writes nothing the invariant is about.
	grants = []ir.TableGrant{grantOf("authenticated", "update", ir.ExecGranted, 30, "note")}
	mods, kg = build(nil)
	if v := CheckWriters(kg, mods, nil, noDefault); len(v) != 0 {
		t.Errorf("UPDATE (note) is not a writer of status: %v", codesOf(v))
	}
	// A whole-table grant writes every column.
	grants = []ir.TableGrant{grantOf("authenticated", "update", ir.ExecGranted, 30)}
	mods, kg = build(nil)
	if v := CheckWriters(kg, mods, nil, noDefault); len(v) != 1 || !strings.Contains(v[0].Message, "status") {
		t.Errorf("a whole-table UPDATE grant writes status: %v", codesOf(v))
	}
}

// PUBLIC and role membership count as for EXECUTE, a revoke removes the
// writer, and a role no statement names holds no table privilege.
// @verifies graph:INV-7
func TestRoleWriterResolution(t *testing.T) {
	mods := func(grants []ir.TableGrant, mem ...ir.Membership) []ir.Module {
		return []ir.Module{{File: "m.sql", TableGrants: grants, Memberships: mem, Symbols: []ir.Symbol{
			fn("sql.public.fail_job", 5, upd("public.jobs", 7, "status"))}}}
	}
	kg := enforcing("sql.public.fail_job")
	roles := func(m []ir.Module) string {
		var out []string
		for _, f := range CheckWriters(kg, m, []string{"authenticated"}, noDefault) {
			out = append(out, f.Subject)
		}
		return strings.Join(out, ",")
	}
	if got := roles(mods([]ir.TableGrant{grantOf("public", "update", ir.ExecGranted, 3)})); got != "sql.role.authenticated,sql.role.public" {
		t.Errorf("PUBLIC: %q", got)
	}
	if got := roles(mods([]ir.TableGrant{grantOf("authenticated", "update", ir.ExecGranted, 3), grantOf("authenticated", "update", ir.ExecRevoked, 4)})); got != "" {
		t.Errorf("a revoke removes the writer: %q", got)
	}
	if got := roles(mods(nil)); got != "" {
		t.Errorf("no statement names a role: %q", got)
	}
	via := mods([]ir.TableGrant{grantOf("app_writer", "update", ir.ExecGranted, 3)},
		ir.Membership{Member: "authenticated", Role: "app_writer", State: ir.ExecGranted, File: "m.sql", Line: 4})
	if got := roles(via); got != "sql.role.app_writer,sql.role.authenticated" {
		t.Errorf("membership: %q", got)
	}
}

// A write the adapter could not read makes the writer set incomplete:
// WRITERS_UNKNOWN names the invariant and the reason, and a gap on another
// table, or on other columns, does not.
// @verifies graph:INV-7
func TestWritersUnknown(t *testing.T) {
	build := func(gaps ...ir.WriterGap) []ir.Module {
		return []ir.Module{{File: "m.sql", WriterGaps: gaps, Symbols: []ir.Symbol{
			fn("sql.public.fail_job", 5, upd("public.jobs", 7, "status"))}}}
	}
	kg := enforcing("sql.public.fail_job")
	run := func(gaps ...ir.WriterGap) []schema.Violation { return CheckWriters(kg, build(gaps...), nil, noDefault) }

	v := run(ir.WriterGap{Table: ir.WriteAll, Reason: "an EXECUTE at m.sql:9 builds its statement at run time", File: "m.sql", Line: 9})
	if len(v) != 1 || v[0].Code != schema.CodeWritersUnknown || v[0].Subject != wRef || v[0].Severity != schema.SeverityInfo ||
		!strings.Contains(v[0].Message, "builds its statement at run time") || !strings.Contains(v[0].Message, "public.jobs.status") {
		t.Fatalf("findings = %+v", v)
	}
	if v := run(ir.WriterGap{Table: "public.other", Reason: "x"}); len(v) != 0 {
		t.Errorf("a gap on another table: %v", codesOf(v))
	}
	if v := run(ir.WriterGap{Table: "public.jobs", Columns: []string{"owner_id"}, Reason: "x"}); len(v) != 0 {
		t.Errorf("a gap on other columns: %v", codesOf(v))
	}
	if v := run(ir.WriterGap{Table: "public.jobs", Columns: []string{"status"}, Reason: "x"}); len(v) != 1 {
		t.Errorf("a gap on the column: %v", codesOf(v))
	}
	// An unknown role membership leaves the table unknown, not closed.
	m := build()
	m[0].TableGrants = []ir.TableGrant{grantOf("app_writer", "update", ir.ExecGranted, 3)}
	m[0].Memberships = []ir.Membership{{Member: "authenticated", Role: "app_writer", State: ir.ExecUnknown, File: "m.sql", Line: 4, Reason: "WITH INHERIT FALSE"}}
	got := CheckWriters(kg, m, []string{"authenticated"}, noDefault)
	found := false
	for _, f := range got {
		found = found || f.Code == schema.CodeWritersUnknown && strings.Contains(f.Message, "whether authenticated can update public.jobs")
	}
	if !found {
		t.Errorf("findings = %v", codesOf(got))
	}
}

// A migration that runs DML itself is a writer of kind migration; the report
// lists writers with what guards each, and says when the set is complete.
// @verifies graph:INV-7
func TestWriterReport(t *testing.T) {
	mods := []ir.Module{{File: "m.sql",
		Writes: []ir.TableWrite{upd("public.jobs", 40, "status")},
		Symbols: []ir.Symbol{
			fn("sql.public.fail_job", 5, upd("public.jobs", 7, "status")),
			fn("sql.public.claim", 10, upd("public.jobs", 12, "attempts")),
		}}}
	ix := BuildWriterIndex(enforcing("sql.public.fail_job"), mods, nil, noDefault)
	if table, col := ix.ResolveTable("jobs.status"); table != "public.jobs" || col != "status" {
		t.Errorf("ResolveTable = %q %q", table, col)
	}
	if table, col := ix.ResolveTable("public.jobs"); table != "public.jobs" || col != "" {
		t.Errorf("ResolveTable = %q %q", table, col)
	}
	rep := ix.Report("public.jobs", "status")
	var got []string
	for _, w := range rep.Writers {
		got = append(got, fmt.Sprintf("%s %s %s unguarded=%v guarded=%d", w.Kind, w.Name, w.Op, w.Unguarded, len(w.Guarded)))
	}
	want := "function sql.public.fail_job update unguarded=[] guarded=1|migration m.sql update unguarded=[queue.jobs:INV-1] guarded=0"
	if strings.Join(got, "|") != want {
		t.Errorf("writers = %q, want %q", strings.Join(got, "|"), want)
	}
	if len(rep.Incomplete) != 0 {
		t.Errorf("incomplete = %+v", rep.Incomplete)
	}
}

func manifestWithColumns(columns ...string) schema.KnowledgeGraph {
	kg := enforcing("sql.public.fail_job")
	kg.Features = []schema.Manifest{{ID: "queue.jobs", SourcePath: "lattice/features/jobs.yaml",
		Invariants: []schema.Invariant{{ID: "INV-1", Columns: columns}}}}
	return kg
}

// A declared column set is the invariant's columns exactly: the enforcer's
// other writes are not used, and a column no enforcer writes still counts.
// @verifies graph:INV-8
func TestDeclaredColumnsAreExact(t *testing.T) {
	mods := []ir.Module{{File: "m.sql",
		Tables: []ir.TableColumns{{Table: "public.jobs", Columns: []string{"attempts", "note", "status", "updated_at"}}},
		Symbols: []ir.Symbol{
			fn("sql.public.fail_job", 5, upd("public.jobs", 7, "status", "updated_at")),
			fn("sql.public.claim", 10, upd("public.jobs", 12, "attempts", "status", "updated_at")),
		}}}
	if v := CheckWriters(manifestWithColumns("public.jobs.note"), mods, nil, noDefault); len(v) != 0 {
		t.Errorf("claim never writes note: %v", codesOf(v))
	}
	v := CheckWriters(manifestWithColumns("public.jobs.attempts"), mods, nil, noDefault)
	if len(v) != 1 || v[0].Subject != "sql.public.claim" || !strings.Contains(v[0].Message, "declares") || strings.Contains(v[0].Message, "status") {
		t.Errorf("findings = %+v", v)
	}
	v = CheckWriters(enforcing("sql.public.fail_job"), mods, nil, noDefault)
	if len(v) != 1 || !strings.Contains(v[0].Message, "derived") || !strings.Contains(v[0].Message, "status") {
		t.Errorf("without a declaration the columns are derived: %+v", v)
	}
}

// Each declared column must name a column the migrations leave. A table whose
// columns are not all known accepts any column, and a table a statement names
// but no CREATE shows is not an error either, since its columns cannot be told.
// @verifies graph:INV-8
func TestDeclaredColumnErrors(t *testing.T) {
	mods := []ir.Module{{File: "m.sql",
		Tables: []ir.TableColumns{{Table: "public.jobs", Columns: []string{"status"}},
			{Table: "public.copy", Unknown: "created LIKE another table"}},
		Symbols: []ir.Symbol{fn("sql.public.fail_job", 5, upd("public.jobs", 7, "status"), upd("public.legacy", 8, "x"))}}}
	cases := []struct{ entry, want string }{
		{"public.jobs", "is not <schema>.<table>.<column>"},
		{"a.b.c.d", "is not <schema>.<table>.<column>"},
		{"public..status", "is not <schema>.<table>.<column>"},
		{"public.nope.status", "which the migrations do not create"},
		{"public.jobs.nope", "which table public.jobs does not have"},
		{"public.copy.anything", ""},
		{"public.legacy.anything", ""},
		{`"public"."jobs"."status"`, ""},
		{`public.jobs."Status"`, "which table public.jobs does not have"},
	}
	for _, tc := range cases {
		v := CheckWriters(manifestWithColumns(tc.entry), mods, nil, noDefault)
		var errs []schema.Violation
		for _, f := range v {
			if f.Severity == schema.SeverityError {
				errs = append(errs, f)
			}
		}
		switch {
		case tc.want == "" && len(errs) != 0:
			t.Errorf("%q: unexpected %+v", tc.entry, errs)
		case tc.want != "" && (len(errs) != 1 || errs[0].Code != schema.CodeValidatePolicy || !strings.Contains(errs[0].Message, tc.want) || errs[0].Location.File != "lattice/features/jobs.yaml"):
			t.Errorf("%q: want a VALIDATE_POLICY error containing %q, got %+v", tc.entry, tc.want, errs)
		}
	}
}

func reads(cols ...string) []ir.CondRead {
	var out []ir.CondRead
	for _, c := range cols {
		out = append(out, ir.CondRead{Table: "public.jobs", Column: c, Qualified: true, Line: 6})
	}
	return out
}

var jobsTable = ir.TableColumns{Table: "public.jobs", Columns: []string{"attempts", "id", "owner_id", "status", "updated_at"}}

// An invariant's columns are the columns its enforcers read in a condition on a
// table they write, whether or not they write them. A writer of one is
// reported with columns from conditions; a bookkeeping column that is only
// assigned no longer links an unrelated writer. Without condition reads the
// columns are what the enforcers write, as before.
// @verifies graph:INV-11
func TestColumnsFromConditions(t *testing.T) {
	enforcer := fn("sql.public.fail_job", 5, upd("public.jobs", 7, "status", "updated_at"))
	enforcer.CondReads = reads("attempts", "id")
	build := func(withReads bool) []ir.Module {
		e := enforcer
		if !withReads {
			e.CondReads = nil
		}
		return []ir.Module{{File: "m.sql", Tables: []ir.TableColumns{jobsTable}, Symbols: []ir.Symbol{
			e,
			fn("sql.public.claim", 10, upd("public.jobs", 12, "attempts", "status", "updated_at")),
			fn("sql.public.touch", 20, upd("public.jobs", 22, "updated_at")),
		}}}
	}
	after := CheckWriters(enforcing("sql.public.fail_job"), build(true), nil, noDefault)
	if len(after) != 1 || after[0].Subject != "sql.public.claim" {
		t.Fatalf("findings = %v, want only claim", codesOf(after))
	}
	m := after[0].Message
	if !strings.Contains(m, "columns from conditions") || !strings.Contains(m, "attempts (m.sql:12)") || strings.Contains(m, "status (") || strings.Contains(m, "updated_at (") {
		t.Errorf("message = %q", m)
	}
	before := CheckWriters(enforcing("sql.public.fail_job"), build(false), nil, noDefault)
	if len(before) != 2 || !strings.Contains(before[0].Message, "columns derived from writes") {
		t.Fatalf("without condition reads: %v, want claim and touch on the derived columns", codesOf(before))
	}
	t.Logf("findings on the example: %d from writes, %d from conditions", len(before), len(after))
	for _, f := range after {
		if f.Severity != schema.SeverityInfo {
			t.Errorf("%s is %s, want info", f.Code, f.Severity)
		}
	}
}

// An enforcer whose condition reads are empty, unknown, on a table it does not
// write, or a bare name its table lacks, falls back to the columns it writes:
// the set never shrinks to nothing. The message says which enforcer fell back.
// @verifies graph:INV-11
func TestColumnsFallBackToWrites(t *testing.T) {
	other := ir.CondRead{Table: "public.audit", Column: "attempts", Qualified: true, Line: 6}
	param := ir.CondRead{Table: "public.jobs", Column: "p_id", Line: 6}
	for name, cr := range map[string][]ir.CondRead{"none": nil, "other table": {other}, "not a column": {param}} {
		t.Run(name, func(t *testing.T) {
			e := fn("sql.public.fail_job", 5, upd("public.jobs", 7, "status"))
			e.CondReads = cr
			mods := []ir.Module{{File: "m.sql", Tables: []ir.TableColumns{jobsTable}, Symbols: []ir.Symbol{
				e, fn("sql.public.claim", 10, upd("public.jobs", 12, "status"))}}}
			v := CheckWriters(enforcing("sql.public.fail_job"), mods, nil, noDefault)
			if len(v) != 1 || !strings.Contains(v[0].Message, "columns derived from writes") || !strings.Contains(v[0].Message, "status (m.sql:12)") {
				t.Fatalf("findings = %+v, want claim on the columns derived from writes", v)
			}
		})
	}
	// Two enforcers: one reads a condition, one has none. The message names the one
	// that fell back, and its columns are used too.
	a := fn("sql.public.fail_job", 5, upd("public.jobs", 7, "status"))
	a.CondReads = reads("attempts")
	b := fn("sql.public.retry", 30, upd("public.jobs", 32, "updated_at"))
	mods := []ir.Module{{File: "m.sql", Tables: []ir.TableColumns{jobsTable}, Symbols: []ir.Symbol{
		a, b, fn("sql.public.claim", 10, upd("public.jobs", 12, "attempts", "updated_at"))}}}
	v := CheckWriters(enforcing("sql.public.fail_job", "sql.public.retry"), mods, nil, noDefault)
	if len(v) != 1 || !strings.Contains(v[0].Message, "sql.public.retry no condition read was known") ||
		!strings.Contains(v[0].Message, "attempts (") || !strings.Contains(v[0].Message, "updated_at (") {
		t.Errorf("findings = %+v", v)
	}
}

// A CHECK enforcer writes nothing; its condition columns count on the tables
// the invariant's other enforcers write. Declared columns still win.
// @verifies graph:INV-11
func TestCheckConditionColumns(t *testing.T) {
	check := ir.Symbol{FQN: "sql.check.public.jobs.cap", File: "m.sql", Line: 3, CondReads: []ir.CondRead{{Table: "public.jobs", Column: "attempts", Line: 3}}}
	enforcer := fn("sql.public.fail_job", 5, upd("public.jobs", 7, "status"))
	enforcer.CondReads = reads("id")
	mods := []ir.Module{{File: "m.sql", Tables: []ir.TableColumns{jobsTable}, Symbols: []ir.Symbol{
		check, enforcer,
		fn("sql.public.claim", 10, upd("public.jobs", 12, "attempts", "status")),
	}}}
	v := CheckWriters(enforcing("sql.public.fail_job", "sql.check.public.jobs.cap"), mods, nil, noDefault)
	if len(v) != 1 || !strings.Contains(v[0].Message, "attempts (") || strings.Contains(v[0].Message, "status (") {
		t.Errorf("findings = %+v, want claim on attempts only", v)
	}
	kg := manifestWithColumns("public.jobs.status")
	kg.Symbols = append(kg.Symbols, schema.GraphSymbol{FQN: "sql.check.public.jobs.cap", Feature: "queue.jobs", EnforcesInvariants: []string{wRef}})
	v = CheckWriters(kg, mods, nil, noDefault)
	if len(v) != 1 || !strings.Contains(v[0].Message, "declares") {
		t.Errorf("declared columns must win: %+v", v)
	}
}

// The writers report says where the columns came from.
// @verifies graph:INV-11
func TestReportColumnsFromConditions(t *testing.T) {
	e := fn("sql.public.fail_job", 5, upd("public.jobs", 7, "status"))
	e.CondReads = reads("attempts")
	mods := []ir.Module{{File: "m.sql", Tables: []ir.TableColumns{jobsTable}, Symbols: []ir.Symbol{
		e, fn("sql.public.claim", 10, upd("public.jobs", 12, "attempts"))}}}
	rep := BuildWriterIndex(enforcing("sql.public.fail_job"), mods, nil, noDefault).Report("public.jobs", "attempts")
	if len(rep.Writers) != 1 || rep.Writers[0].ColumnsFrom[wRef] != ColumnsConditions {
		t.Errorf("report = %+v, want columns_from conditions", rep.Writers)
	}
}

// An enforcer that writes nothing, such as a read-only function that filters
// on a column, contributes its condition reads on any table: a writer that
// changes that column changes what the function returns. Selectors do not
// count, and an enforcer that writes keeps the tables it writes.
// @verifies graph:INV-12
func TestReadOnlyEnforcerConditionColumns(t *testing.T) {
	videos := ir.TableColumns{Table: "public.videos", Columns: []string{"ai_pending", "id", "title", "updated_at"}}
	reader := ir.Symbol{FQN: "sql.public.list_videos", File: "m.sql", Line: 3, Execute: &ir.Execute{},
		CondReads: []ir.CondRead{{Table: "public.videos", Column: "ai_pending", Qualified: true, Line: 5}}}
	writer := fn("sql.public.finish_enrich", 10, ir.TableWrite{Table: "public.videos", Op: ir.WriteUpdate, Columns: []string{"ai_pending", "updated_at"}, Line: 12})
	touch := fn("sql.public.touch_video", 20, ir.TableWrite{Table: "public.videos", Op: ir.WriteUpdate, Columns: []string{"updated_at"}, Line: 22})
	mods := []ir.Module{{File: "m.sql", Tables: []ir.TableColumns{videos}, Symbols: []ir.Symbol{reader, writer, touch}}}
	got := CheckWriters(enforcing("sql.public.list_videos"), mods, nil, noDefault)
	if len(got) != 1 || got[0].Code != schema.CodeUnguardedWriter || got[0].Subject != "sql.public.finish_enrich" {
		t.Fatalf("findings = %v, want only finish_enrich", codesOf(got))
	}
	if m := got[0].Message; !strings.Contains(m, "columns from conditions") || !strings.Contains(m, "ai_pending (m.sql:12)") || strings.Contains(m, "updated_at (") {
		t.Errorf("message = %q", m)
	}
	if got[0].Severity != schema.SeverityInfo {
		t.Errorf("severity = %s, want info", got[0].Severity)
	}
	// The same read on an enforcer that writes another table does not count.
	other := fn("sql.public.list_videos", 3, ir.TableWrite{Table: "public.audit", Op: ir.WriteInsert, Columns: []string{"note"}, Line: 4})
	other.CondReads = reader.CondReads
	mods = []ir.Module{{File: "m.sql", Tables: []ir.TableColumns{videos}, Symbols: []ir.Symbol{other, writer, touch}}}
	for _, f := range CheckWriters(enforcing("sql.public.list_videos"), mods, nil, noDefault) {
		if f.Subject == "sql.public.finish_enrich" && strings.Contains(f.Message, "columns from conditions") {
			t.Errorf("an enforcer that writes keeps its tables: %s", f.Message)
		}
	}
}
