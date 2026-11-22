package graph

// @module-feature graph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// Writers of the columns a promise is about.
//
// A promise enforced in one SQL function is only as strong as the set of code
// that writes the columns the function writes. The columns of an invariant are
// the columns its enforcers write; any other writer of one of them, a second
// function, a migration, or an API role holding a table privilege, can break
// the promise with no break ever aimed at it.
//
// An invariant's columns are decided in this order:
//
//  1. declared: columns: in its feature YAML, exactly those;
//  2. from conditions: for each enforcer, the columns it reads in a condition
//     (an IF, ELSIF or CASE WHEN, the WHERE of its own UPDATE, DELETE or
//     SELECT ... INTO, a CHECK, a column loaded by SELECT col INTO v and tested
//     as v) on a table it writes; an enforcer that writes nothing (a read-only
//     function, a CHECK) counts its condition reads on any table. Whether the enforcer also writes the column
//     does not matter: a function that tests attempts and sets status is about
//     attempts, and a second writer of attempts can break the promise;
//  3. from writes: for an enforcer whose condition reads on its tables are
//     empty or not known (a body the adapter could not read), the columns it
//     writes. It never shrinks to an empty set, which would read as "no
//     column".
//
// A column every enforcer happens to write (a status or a timestamp) therefore
// no longer ties the invariant to every other writer of that column.
//
// A writer is guarded for an invariant when it is an enforcer of it, or when a
// trigger that enforces it applies to the writer's table and command, or when
// a policy that enforces it does and the writer is an API role (a SECURITY
// DEFINER function, and the table's owner, bypass row-level security).

// Writer kinds.
const (
	WriterFunction  = "function"  // a SQL function, procedure, or trigger function
	WriterRole      = "role"      // a role that holds a table privilege
	WriterMigration = "migration" // top-level DML a migration runs itself
)

// Writer is one write to a table by one writer.
type Writer struct {
	Kind string `json:"kind"`
	// Name is the function's FQN, the role, or the migration's file.
	Name  string `json:"name"`
	Table string `json:"table"`
	Op    string `json:"op"`
	// Columns are the columns written, or "*" for every column.
	Columns []string `json:"columns,omitempty"`
	File    string   `json:"file"`
	Line    int      `json:"line"`
	// Via, for a role, is the membership chain the privilege came through.
	Via string `json:"via,omitempty"`
	// Definer is true for a SECURITY DEFINER function.
	Definer bool `json:"definer,omitempty"`
}

// WriterGuard says by what a writer is guarded for an invariant.
type WriterGuard struct {
	Invariant string `json:"invariant"`
	// By is "enforcer", or the FQN of the trigger or policy.
	By string `json:"by"`
}

// WriterEntry is a Writer with the invariants it is guarded or unguarded for.
type WriterEntry struct {
	Writer
	Guarded   []WriterGuard `json:"guarded,omitempty"`
	Unguarded []string      `json:"unguarded,omitempty"`
	// ColumnsFrom says, for each invariant the writer is guarded or
	// unguarded for, whether its columns were declared in the feature YAML
	// ("declared"), read in the conditions of its enforcers ("conditions"),
	// or derived from the columns its enforcers write ("derived").
	ColumnsFrom map[string]string `json:"columns_from,omitempty"`
}

// How the columns of an invariant were decided.
const (
	ColumnsDeclared   = "declared"
	ColumnsConditions = "conditions"
	ColumnsDerived    = "derived"
)

// WriterReport is what `lattice writers` prints.
type WriterReport struct {
	Table   string        `json:"table"`
	Column  string        `json:"column,omitempty"`
	Writers []WriterEntry `json:"writers"`
	// Incomplete says why the writer set is not complete; empty when it is.
	Incomplete []ir.WriterGap `json:"incomplete,omitempty"`
	// RolesChecked is true when sql.api_roles is declared, so roles that no
	// statement names were resolved too.
	RolesChecked bool `json:"roles_checked"`
}

// writeGuard is a trigger or policy that enforces an invariant.
type writeGuard struct {
	fqn      string
	kind     string
	table    string
	commands []string
	columns  []string
}

// invWrites is what the index knows about one invariant.
type invWrites struct {
	ref       string
	columns   map[string]map[string]bool // table -> column
	enforcers map[string]bool
	guards    []writeGuard
	file      string
	line      int
	// declared is true when columns came from the invariant's columns: field.
	declared bool
	// fromConditions are the enforcers whose columns are the columns they read
	// in a condition; fromWrites are those that fell back to the columns they
	// write, because no condition read on their tables was known.
	fromConditions, fromWrites []string
}

// columnsFrom is how the invariant's columns were decided: declared, from
// conditions when any enforcer contributed its condition reads, else derived
// from writes.
func (iw *invWrites) columnsFrom() string {
	switch {
	case iw.declared:
		return ColumnsDeclared
	case len(iw.fromConditions) > 0:
		return ColumnsConditions
	}
	return ColumnsDerived
}

// WriterIndex lists the writers of every table the SQL facts name.
type WriterIndex struct {
	writers []Writer
	gaps    []ir.WriterGap
	invs    map[string]*invWrites
	tables  map[string]bool
	// schema is what the migrations leave of each table's columns.
	schema map[string]ir.TableColumns
	// errs are the declared columns that name no table or column the
	// migrations leave.
	errs []schema.Violation

	apiRoles     []string
	defaultWrite *[]string
	mem          []ir.Membership
	grants       map[string][]ir.TableGrant
	rls          map[string]ir.TableRLS
	policies     map[string][]policyFact
	rolesDone    map[string]bool
}

const wildcard = ir.WriteAll

// overlap returns the columns two column sets share. "*" stands for every
// column, so it shares whatever the other holds, and shares "*" with "*".
func overlap(a, b []string) []string {
	var out []string
	for _, x := range a {
		for _, y := range b {
			switch {
			case x == y:
				out = append(out, x)
			case x == wildcard:
				out = append(out, y)
			case y == wildcard:
				out = append(out, x)
			}
		}
	}
	return uniqSorted(out)
}

func uniqSorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func setList(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// policyFact is a policy as the row-level security check reads it.
type policyFact struct {
	fqn         string
	commands    []string
	roles       []string
	restrictive bool
	altered     bool // an ALTER POLICY changed its roles: any role may be allowed
}

// BuildWriterIndex builds the index from the adapter's facts. apiRoles are the
// roles a client reaches the database as (workspace sql.api_roles); a role a
// table privilege names is a writer whether or not it is listed.
//
// A role's write privilege on a table is decided, in order, by the statement
// that names it, by what PUBLIC holds (nothing on a table, by default), and by
// the platform default, defaultWrite (sql.default_write): listed is granted,
// declared without the role is revoked, unset is unknown, which leaves the
// table's writers incomplete. With row-level security enabled on the table, a
// role that holds the privilege still needs a permissive policy for the
// command whose TO list reaches it.
// @enforces graph:INV-7
func BuildWriterIndex(kg schema.KnowledgeGraph, mods []ir.Module, apiRoles []string, defaultWrite *[]string) *WriterIndex {
	ix := &WriterIndex{invs: map[string]*invWrites{}, tables: map[string]bool{}, schema: map[string]ir.TableColumns{}, apiRoles: apiRoles, defaultWrite: defaultWrite,
		grants: map[string][]ir.TableGrant{}, rls: map[string]ir.TableRLS{}, policies: map[string][]policyFact{}, rolesDone: map[string]bool{}}
	syms := map[string]ir.Symbol{}
	for _, m := range mods {
		ix.mem = append(ix.mem, m.Memberships...)
		for _, g := range m.TableGrants {
			ix.grants[g.Table] = append(ix.grants[g.Table], g)
		}
		for _, r := range m.RLS {
			ix.rls[r.Table] = r
		}
		ix.gaps = append(ix.gaps, m.WriterGaps...)
		for _, t := range m.Tables {
			ix.schema[t.Table] = t
		}
		for _, s := range m.Symbols {
			if s.IsTest {
				continue
			}
			syms[s.FQN] = s
			if g := s.Guard; g != nil && g.Kind == ir.GuardPolicy {
				altered := false
				for _, al := range s.Altered {
					altered = altered || contains(al.Sets, "to")
				}
				ix.policies[g.Table] = append(ix.policies[g.Table], policyFact{fqn: s.FQN, commands: g.Commands,
					roles: g.Roles, restrictive: g.Restrictive, altered: altered})
			}
			for _, w := range s.Writes {
				definer := s.Execute != nil && s.Execute.SecurityDefiner
				ix.writers = append(ix.writers, Writer{Kind: WriterFunction, Name: s.FQN, Table: w.Table, Op: w.Op,
					Columns: w.Columns, File: s.File, Line: w.Line, Definer: definer})
			}
		}
		for _, w := range m.Writes {
			ix.writers = append(ix.writers, Writer{Kind: WriterMigration, Name: m.File, Table: w.Table, Op: w.Op,
				Columns: w.Columns, File: m.File, Line: w.Line})
		}
	}

	for _, s := range kg.Symbols {
		sym, ok := syms[s.FQN]
		if !ok {
			continue
		}
		for _, ref := range s.EnforcesInvariants {
			f, inv := schema.ResolveRef(ref, s.Feature)
			if f == "" || strings.HasPrefix(f, "brd.") {
				continue
			}
			full := f + ":" + inv
			iw := ix.invs[full]
			if iw == nil {
				iw = &invWrites{ref: full, columns: map[string]map[string]bool{}, enforcers: map[string]bool{}}
				ix.invs[full] = iw
			}
			if iw.file == "" {
				iw.file, iw.line = sym.File, sym.Line
			}
			iw.enforcers[s.FQN] = true
			if g := sym.Guard; g != nil {
				iw.guards = append(iw.guards, writeGuard{fqn: s.FQN, kind: g.Kind, table: g.Table, commands: g.Commands, columns: g.Columns})
			}
		}
	}
	for _, iw := range ix.invs {
		ix.deriveColumns(iw, syms)
	}
	ix.declareColumns(kg)
	for _, w := range ix.writers {
		ix.tables[w.Table] = true
	}
	for t := range ix.schema {
		ix.tables[t] = true
	}
	for t := range ix.grants {
		ix.tables[t] = true
	}
	for _, iw := range ix.invs {
		for t := range iw.columns {
			ix.tables[t] = true
		}
	}
	for _, t := range setList(ix.tables) {
		ix.roleWritersFor(t)
	}
	ix.sortWriters()
	return ix
}

// deriveColumns decides the columns of an invariant from its enforcers: for
// each, the columns it reads in a condition on a table it writes, or, when it
// has none on those tables, the columns it writes. An enforcer that writes
// nothing (a read-only function, a CHECK) contributes its condition reads on
// any table. A bare name in a condition is a column
// only if the table is known to have it.
// @enforces graph:INV-11, graph:INV-12
func (ix *WriterIndex) deriveColumns(iw *invWrites, syms map[string]ir.Symbol) {
	enforcers := setList(iw.enforcers)
	add := func(table, col string) {
		if iw.columns[table] == nil {
			iw.columns[table] = map[string]bool{}
		}
		iw.columns[table][col] = true
	}
	for _, f := range enforcers {
		sym := syms[f]
		// An enforcer that writes nothing (a read-only function, a CHECK)
		// contributes its condition reads on any table: a writer of what it
		// filters on changes what it returns. One that writes keeps the
		// tables it writes.
		anyTable := len(sym.Writes) == 0
		tables := map[string]bool{}
		for _, w := range sym.Writes {
			tables[w.Table] = true
		}
		found := false
		for _, c := range sym.CondReads {
			if (anyTable || tables[c.Table]) && ix.isColumn(c) {
				add(c.Table, c.Column)
				found = true
			}
		}
		if found {
			iw.fromConditions = append(iw.fromConditions, f)
			continue
		}
		wrote := false
		for _, w := range sym.Writes {
			if w.Op == ir.WriteDelete {
				continue
			}
			wrote = true
			for _, c := range w.Columns {
				add(w.Table, c)
			}
		}
		if wrote {
			iw.fromWrites = append(iw.fromWrites, f)
		}
	}
}

// isColumn says whether a condition read names a column of its table: a name
// the text qualified does, unless the table's columns are known and lack it; a
// bare name does only when the table is known to have it.
func (ix *WriterIndex) isColumn(c ir.CondRead) bool {
	t, ok := ix.schema[c.Table]
	if c.Qualified {
		return !ok || t.Unknown != "" || contains(t.Columns, c.Column)
	}
	return ok && contains(t.Columns, c.Column)
}

// foldColumnRef folds a declared "<schema>.<table>.<column>" as PostgreSQL
// folds names: a part in double quotes keeps its case, any other is lower
// case. ok is false unless it has exactly three non-empty parts.
func foldColumnRef(ref string) (table, column string, ok bool) {
	var parts []string
	var cur strings.Builder
	quoted := false
	for i := 0; i < len(ref); i++ {
		c := ref[i]
		switch {
		case c == '"' && quoted && i+1 < len(ref) && ref[i+1] == '"':
			cur.WriteByte('"')
			i++
		case c == '"':
			quoted = !quoted
		case c == '.' && !quoted:
			parts = append(parts, cur.String())
			cur.Reset()
		case quoted:
			cur.WriteByte(c)
		default:
			cur.WriteString(strings.ToLower(string(c)))
		}
	}
	parts = append(parts, cur.String())
	if quoted || len(parts) != 3 {
		return "", "", false
	}
	for _, p := range parts {
		if p == "" || p == wildcard {
			return "", "", false
		}
	}
	return parts[0] + "." + parts[1], parts[2], true
}

// declareColumns applies the columns an invariant declares. They replace the
// columns derived from its enforcers, for an invariant that has enforcers, and
// each must name a column the migrations leave: a table the index does not know
// and a column a known table does not have are errors, but a table whose
// columns are not all known (LIKE, inheritance) accepts any column.
// @enforces graph:INV-8
func (ix *WriterIndex) declareColumns(kg schema.KnowledgeGraph) {
	for i := range kg.Features {
		m := &kg.Features[i]
		for _, in := range m.Invariants {
			if len(in.Columns) == 0 {
				continue
			}
			ref := m.ID + ":" + in.ID
			declared := map[string]map[string]bool{}
			for _, raw := range in.Columns {
				table, col, ok := foldColumnRef(raw)
				if !ok {
					ix.declaredErr(m, ref, fmt.Sprintf("%s: columns entry %q is not <schema>.<table>.<column>", ref, raw))
					continue
				}
				if why := ix.unknownColumn(table, col); why != "" {
					ix.declaredErr(m, ref, fmt.Sprintf("%s: columns entry %q %s", ref, raw, why))
					continue
				}
				if declared[table] == nil {
					declared[table] = map[string]bool{}
				}
				declared[table][col] = true
			}
			if iw := ix.invs[ref]; iw != nil {
				iw.columns, iw.declared = declared, true
			}
		}
	}
}

// unknownColumn says why a table or column is not one the migrations leave,
// or "" when it is, or when that cannot be told.
func (ix *WriterIndex) unknownColumn(table, col string) string {
	t, ok := ix.schema[table]
	if !ok {
		if ix.knownByStatement(table) {
			return ""
		}
		return fmt.Sprintf("names table %s, which the migrations do not create", table)
	}
	if t.Unknown != "" || contains(t.Columns, col) {
		return ""
	}
	return fmt.Sprintf("names column %s, which table %s does not have (it has %s)", col, table, strings.Join(t.Columns, ", "))
}

// knownByStatement is true for a table some statement names even though no
// CREATE TABLE of it was read: its columns are not known, so a column of it
// cannot be checked.
func (ix *WriterIndex) knownByStatement(table string) bool {
	for _, w := range ix.writers {
		if w.Table == table {
			return true
		}
	}
	return len(ix.grants[table]) > 0
}

func (ix *WriterIndex) declaredErr(m *schema.Manifest, ref, msg string) {
	ix.errs = append(ix.errs, schema.Violation{
		Code: schema.CodeValidatePolicy, Severity: schema.SeverityError,
		FeatureID: m.ID, InvariantID: strings.TrimPrefix(ref, m.ID+":"), Subject: ref,
		Message:  msg,
		Location: &schema.Location{File: m.SourcePath},
		NextAction: &schema.NextAction{Kind: "edit_feature", Ref: ref,
			Detail: "write each entry of columns: as <schema>.<table>.<column>, naming a column the migrations create"},
	})
}

func (ix *WriterIndex) sortWriters() {
	sort.SliceStable(ix.writers, func(i, j int) bool {
		a, b := ix.writers[i], ix.writers[j]
		if a.Table != b.Table {
			return a.Table < b.Table
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Op < b.Op
	})
}

var writeOps = []string{ir.WriteInsert, ir.WriteUpdate, ir.WriteDelete}

// roleWritersFor adds a writer for each role that can INSERT, UPDATE or DELETE
// on the table, resolved as EXECUTE is: the statement that names the role, then
// PUBLIC (nothing on a table by default), then sql.default_write, and what any
// role it is a member of holds. A state nothing settles is an unknown that
// leaves the table's writer set incomplete, never an absent writer.
func (ix *WriterIndex) roleWritersFor(table string) {
	if ix.rolesDone[table] {
		return
	}
	ix.rolesDone[table] = true
	if len(ix.grants[table]) == 0 && len(ix.apiRoles) == 0 {
		return
	}
	reasons := map[string]bool{}
	gap := func(file string, line int, reason string) {
		if !reasons[reason] {
			reasons[reason] = true
			ix.gaps = append(ix.gaps, ir.WriterGap{Table: table, File: file, Line: line, Reason: reason})
		}
	}
	for _, op := range writeOps {
		var gs []ir.TableGrant
		for _, g := range ix.grants[table] {
			if g.Op == op {
				gs = append(gs, g)
			}
		}
		roleSet := map[string]bool{ir.ExecPublic: true}
		cols := map[string]bool{"": true}
		named := map[string]bool{}
		for _, g := range gs {
			roleSet[g.Role] = true
			named[g.Role] = true
			for _, c := range g.Columns {
				cols[c] = true
			}
		}
		for _, r := range ix.apiRoles {
			roleSet[r] = true
		}
		type got struct {
			cols map[string]bool
			p    Privilege
		}
		granted := map[string]*got{}
		for _, col := range setList(cols) {
			sym := ir.Symbol{FQN: table, File: "workspace.yaml", Execute: &ir.Execute{Roles: tableRoles(gs, col)}}
			for _, role := range setList(roleSet) {
				p := ExecutableBy(sym, role, ix.defaultWrite, ix.mem)
				switch p.State {
				case ir.ExecGranted:
					g := granted[role]
					if g == nil {
						g = &got{cols: map[string]bool{}, p: p}
						granted[role] = g
					}
					g.cols[col] = true
					if col == "" {
						g.p = p
					}
				case ir.ExecUnknown:
					if strings.Contains(p.Reason, "sql.default_execute is not declared") {
						gap("workspace.yaml", 0, fmt.Sprintf("no statement names %s on %s, and sql.default_write is not declared", roleName(role), table))
						continue
					}
					gap(p.File, p.Line, fmt.Sprintf("whether %s can %s %s is not known from the migrations: %s", roleName(role), op, table, p.Reason))
				}
			}
		}
		var grantedRoles []string
		for role := range granted {
			grantedRoles = append(grantedRoles, role)
		}
		sort.Strings(grantedRoles)
		for _, role := range grantedRoles {
			g := granted[role]
			if !ix.rlsAllows(table, op, role, gap) {
				continue
			}
			columns := []string{wildcard}
			if !g.cols[""] {
				columns = setList(g.cols)
			}
			via := g.p.Via
			if g.p.Default {
				via = "platform default (sql.default_write)"
			}
			ix.writers = append(ix.writers, Writer{Kind: WriterRole, Name: role, Table: table, Op: op,
				Columns: columns, File: g.p.File, Line: g.p.Line, Via: via})
		}
	}
}

// rlsAllows says whether row-level security lets the role run the command on
// the table. Without RLS enabled it does. With it enabled, the role needs a
// permissive policy for the command (or ALL) whose TO list is PUBLIC, the role,
// or a role it is a member of; any policy allows, even USING (false). A
// restrictive policy never allows on its own. When membership leaves it
// unknown the role is kept as a writer and the table gets a gap.
func (ix *WriterIndex) rlsAllows(table, op, role string, gap func(string, int, string)) bool {
	r, ok := ix.rls[table]
	if !ok || !r.Enabled {
		return true
	}
	unknown := ""
	for _, p := range ix.policies[table] {
		if p.restrictive || !contains(p.commands, op) {
			continue
		}
		if p.altered || len(p.roles) == 0 {
			return true
		}
		var rs []ir.ExecuteGrant
		for _, to := range p.roles {
			rs = append(rs, ir.ExecuteGrant{Role: to, State: ir.ExecGranted, File: "policy", Line: 1})
		}
		if !contains(p.roles, ir.ExecPublic) {
			rs = append(rs, ir.ExecuteGrant{Role: ir.ExecPublic, State: ir.ExecRevoked, Default: true})
		}
		pr := ExecutableBy(ir.Symbol{FQN: p.fqn, Execute: &ir.Execute{Roles: rs}}, role, &[]string{}, ix.mem)
		switch pr.State {
		case ir.ExecGranted:
			return true
		case ir.ExecUnknown:
			unknown = pr.Reason
		}
	}
	if unknown != "" {
		gap(r.File, r.Line, fmt.Sprintf("whether a policy on %s allows %s to %s is not known: %s", table, roleName(role), op, unknown))
		return true
	}
	return false
}

func roleName(r string) string {
	if r == ir.ExecPublic {
		return "PUBLIC"
	}
	return r
}

// tableRoles builds the per-role entries of one table privilege for one
// column ("" is the whole table): a role holds it when it was granted the
// table or the column, and a state no statement settles is unknown.
func tableRoles(gs []ir.TableGrant, col string) []ir.ExecuteGrant {
	type pair struct{ whole, col *ir.TableGrant }
	by := map[string]*pair{}
	for i := range gs {
		g := &gs[i]
		p := by[g.Role]
		if p == nil {
			p = &pair{}
			by[g.Role] = p
		}
		switch {
		case len(g.Columns) == 0:
			p.whole = g
		case col != "" && contains(g.Columns, col):
			p.col = g
		}
	}
	var out []ir.ExecuteGrant
	for role, p := range by {
		var pick *ir.TableGrant
		for _, want := range []string{ir.ExecGranted, ir.ExecUnknown, ir.ExecRevoked} {
			if p.whole != nil && p.whole.State == want {
				pick = p.whole
			} else if p.col != nil && p.col.State == want {
				pick = p.col
			}
			if pick != nil {
				break
			}
		}
		if pick != nil {
			out = append(out, ir.ExecuteGrant{Role: role, State: pick.State, File: pick.File, Line: pick.Line, Reason: pick.Reason})
		}
	}
	if _, ok := by[ir.ExecPublic]; !ok {
		out = append(out, ir.ExecuteGrant{Role: ir.ExecPublic, State: ir.ExecRevoked, Default: true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Role < out[j].Role })
	return out
}

func contains(list []string, s string) bool { return containsRole(list, s) }

// guardedBy says what guards the writer for an invariant, or "".
func (iw *invWrites) guardedBy(w Writer) string {
	if w.Kind == WriterFunction && iw.enforcers[w.Name] {
		return "enforcer"
	}
	for _, g := range iw.guards {
		if g.table != w.Table || !contains(g.commands, w.Op) {
			continue
		}
		if len(g.columns) > 0 && len(overlap(w.Columns, g.columns)) == 0 {
			continue
		}
		if g.kind == ir.GuardTrigger || g.kind == ir.GuardPolicy && w.Kind == WriterRole {
			return g.fqn
		}
	}
	return ""
}

// columnsPhrase says whose columns they are and how they were decided.
func (iw *invWrites) columnsPhrase(ref string) string {
	if iw.declared {
		return ref + " declares (columns: in its feature YAML)"
	}
	if len(iw.fromConditions) == 0 {
		return ref + "'s enforcers write (columns derived from writes; declare columns: on the invariant to narrow them)"
	}
	phrase := ref + "'s enforcers test (columns from conditions: read in an IF, CASE WHEN, WHERE or CHECK of an enforcer, on a table it writes"
	if len(iw.fromWrites) > 0 {
		phrase += "; for " + strings.Join(iw.fromWrites, ", ") + " no condition read was known, so its columns derived from writes"
	}
	return phrase + "; declare columns: on the invariant to override)"
}

// shared are the columns a writer's write has in common with the invariant's
// columns on the table.
func (iw *invWrites) shared(w Writer) []string {
	if w.Op == ir.WriteDelete {
		return nil
	}
	return overlap(w.Columns, setList(iw.columns[w.Table]))
}

// gapsFor are the gaps that leave the columns of the table incomplete.
func gapsFor(gaps []ir.WriterGap, table string, columns []string) []ir.WriterGap {
	var out []ir.WriterGap
	for _, g := range gaps {
		if g.Table != wildcard && g.Table != table {
			continue
		}
		if len(g.Columns) > 0 && len(columns) > 0 && len(overlap(g.Columns, columns)) == 0 {
			continue
		}
		out = append(out, g)
	}
	return out
}

func (ix *WriterIndex) refs() []string {
	refs := make([]string, 0, len(ix.invs))
	for r := range ix.invs {
		refs = append(refs, r)
	}
	sort.Strings(refs)
	return refs
}

// CheckWriters reports the writers of an invariant's columns that nothing
// guards, and the invariants whose writers are not completely known.
//
// UNGUARDED_WRITER (info): one per (writer, invariant) where the writer writes
// at least one of the invariant's columns and is neither an enforcer of it nor
// under a trigger or policy that enforces it.
//
// WRITERS_UNKNOWN (info): one per invariant whose columns lie on a table with
// an incomplete writer set, with the reasons.
//
// Both are info and stay on: a code that were off would hide an unknown.
// @enforces graph:INV-7
func CheckWriters(kg schema.KnowledgeGraph, mods []ir.Module, apiRoles []string, defaultWrite *[]string) []schema.Violation {
	return BuildWriterIndex(kg, mods, apiRoles, defaultWrite).Findings()
}

// Findings are the UNGUARDED_WRITER and WRITERS_UNKNOWN findings, preceded by a
// VALIDATE_POLICY error for each declared column the migrations do not leave.
func (ix *WriterIndex) Findings() []schema.Violation {
	out := append([]schema.Violation(nil), ix.errs...)
	for _, ref := range ix.refs() {
		iw := ix.invs[ref]
		if len(iw.columns) == 0 {
			continue
		}
		f, inv, _ := strings.Cut(ref, ":")
		type group struct {
			writer Writer
			lines  map[string]map[string][]string // "table by op" -> column -> where
			first  Writer
		}
		groups := map[string]*group{}
		var order []string
		for _, w := range ix.writers {
			shared := iw.shared(w)
			if len(shared) == 0 || iw.guardedBy(w) != "" {
				continue
			}
			key := w.Kind + "\x00" + w.Name
			g := groups[key]
			if g == nil {
				g = &group{writer: w, first: w, lines: map[string]map[string][]string{}}
				groups[key] = g
				order = append(order, key)
			}
			label := w.Table + " by " + w.Op
			if g.lines[label] == nil {
				g.lines[label] = map[string][]string{}
			}
			for _, c := range shared {
				at := fmt.Sprintf("%s:%d", w.File, w.Line)
				if w.Kind == WriterRole {
					if w.Line == 0 {
						at = "granted by the " + w.Via
					} else {
						at = "granted at " + at
						if w.Via != "" {
							at += " " + w.Via
						}
					}
				}
				if !contains(g.lines[label][c], at) {
					g.lines[label][c] = append(g.lines[label][c], at)
				}
			}
		}
		for _, key := range order {
			g := groups[key]
			w := g.writer
			var labels []string
			for l := range g.lines {
				labels = append(labels, l)
			}
			sort.Strings(labels)
			var parts []string
			for _, l := range labels {
				var cols []string
				for c := range g.lines[l] {
					cols = append(cols, c)
				}
				sort.Strings(cols)
				var cs []string
				for _, c := range cols {
					name := c
					if c == wildcard {
						name = "every column"
					}
					cs = append(cs, fmt.Sprintf("%s (%s)", name, strings.Join(g.lines[l][c], "; ")))
				}
				parts = append(parts, fmt.Sprintf("%s: %s", l, strings.Join(cs, ", ")))
			}
			subject := w.Name
			who := w.Name
			switch w.Kind {
			case WriterRole:
				subject = "sql.role." + w.Name
				who = "role " + roleName(w.Name)
			case WriterMigration:
				who = "migration " + w.Name
			}
			out = append(out, schema.Violation{
				Code: schema.CodeUnguardedWriter, Severity: schema.SeverityInfo,
				FeatureID: f, InvariantID: inv, Subject: subject,
				Location: &schema.Location{File: g.first.File, Line: g.first.Line},
				Message: fmt.Sprintf("%s writes columns that %s, and is neither an enforcer of %s nor under a trigger or policy that enforces it, so no break ever targets it. %s",
					who, iw.columnsPhrase(ref), ref, strings.Join(parts, "; ")),
				NextAction: &schema.NextAction{Kind: "tag_or_guard", Ref: ref,
					Detail: "tag the writer -- @enforces " + ref + " so it is break-tested, put it under a trigger or policy that enforces " + ref + ", or allowlist it with a reason"},
			})
		}

		var reasons []string
		seen := map[string]bool{}
		var tables []string
		for t := range iw.columns {
			tables = append(tables, t)
		}
		sort.Strings(tables)
		for _, t := range tables {
			for _, g := range gapsFor(ix.gaps, t, setList(iw.columns[t])) {
				if !seen[g.Reason] {
					seen[g.Reason] = true
					reasons = append(reasons, g.Reason)
				}
			}
		}
		if len(reasons) > 0 {
			sort.Strings(reasons)
			out = append(out, schema.Violation{
				Code: schema.CodeWritersUnknown, Severity: schema.SeverityInfo,
				FeatureID: f, InvariantID: inv, Subject: ref,
				Location: &schema.Location{File: iw.file, Line: iw.line},
				Message: fmt.Sprintf("the writers of %s's columns (%s) are not completely known, so an unguarded writer may exist: %s",
					ref, strings.Join(tableColumns(iw), ", "), strings.Join(reasons, "; ")),
				NextAction: &schema.NextAction{Kind: "edit_migration", Ref: ref,
					Detail: "state the write as plain INSERT, UPDATE or DELETE, GRANT or REVOKE statements the adapter reads, or allowlist the finding with a reason"},
			})
		}
	}
	return out
}

func tableColumns(iw *invWrites) []string {
	var tables []string
	for t := range iw.columns {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	var out []string
	for _, t := range tables {
		out = append(out, t+"."+strings.Join(setList(iw.columns[t]), ","))
	}
	return out
}

// ResolveTable reads the argument of `lattice writers`: [schema.]table or
// [schema.]table.column. A two-part name is a schema-qualified table when the
// index knows one, else a column of a table in public.
func (ix *WriterIndex) ResolveTable(arg string) (table, column string) {
	parts := strings.Split(strings.TrimSpace(arg), ".")
	switch len(parts) {
	case 1:
		return "public." + strings.ToLower(parts[0]), ""
	case 2:
		whole := strings.ToLower(parts[0]) + "." + strings.ToLower(parts[1])
		if ix.tables[whole] {
			return whole, ""
		}
		if t := "public." + strings.ToLower(parts[0]); ix.tables[t] {
			return t, strings.ToLower(parts[1])
		}
		return whole, ""
	default:
		return strings.ToLower(parts[0]) + "." + strings.ToLower(parts[1]), strings.ToLower(strings.Join(parts[2:], "."))
	}
}

// Report lists the writers of a table, or of one of its columns, with what
// guards each for the invariants whose columns it writes.
func (ix *WriterIndex) Report(table, column string) WriterReport {
	if !ix.rolesDone[table] {
		ix.roleWritersFor(table)
		ix.sortWriters()
	}
	rep := WriterReport{Table: table, Column: column, Writers: []WriterEntry{}, RolesChecked: len(ix.apiRoles) > 0}
	for _, w := range ix.writers {
		if w.Table != table {
			continue
		}
		if column != "" {
			if w.Op == ir.WriteDelete || len(overlap(w.Columns, []string{column})) == 0 {
				continue
			}
		}
		e := WriterEntry{Writer: w}
		for _, ref := range ix.refs() {
			iw := ix.invs[ref]
			if len(iw.shared(w)) == 0 {
				continue
			}
			if e.ColumnsFrom == nil {
				e.ColumnsFrom = map[string]string{}
			}
			e.ColumnsFrom[ref] = iw.columnsFrom()
			if by := iw.guardedBy(w); by != "" {
				e.Guarded = append(e.Guarded, WriterGuard{Invariant: ref, By: by})
			} else {
				e.Unguarded = append(e.Unguarded, ref)
			}
		}
		rep.Writers = append(rep.Writers, e)
	}
	var cols []string
	if column != "" {
		cols = []string{column}
	}
	seen := map[string]bool{}
	for _, g := range gapsFor(ix.gaps, table, cols) {
		if !seen[g.Reason] {
			seen[g.Reason] = true
			rep.Incomplete = append(rep.Incomplete, g)
		}
	}
	sort.SliceStable(rep.Incomplete, func(i, j int) bool { return rep.Incomplete[i].Reason < rep.Incomplete[j].Reason })
	return rep
}
