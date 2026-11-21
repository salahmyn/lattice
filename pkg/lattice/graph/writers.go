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
}

// WriterReport is what `lattice writers` prints.
type WriterReport struct {
	Table   string        `json:"table"`
	Column  string        `json:"column,omitempty"`
	Writers []WriterEntry `json:"writers"`
	// Incomplete says why the writer set is not complete; empty when it is.
	Incomplete []ir.WriterGap `json:"incomplete,omitempty"`
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
}

// WriterIndex lists the writers of every table the SQL facts name.
type WriterIndex struct {
	writers []Writer
	gaps    []ir.WriterGap
	invs    map[string]*invWrites
	tables  map[string]bool
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

// BuildWriterIndex builds the index from the adapter's facts. apiRoles are the
// roles a client reaches the database as (workspace sql.api_roles); a role a
// table privilege names is a writer whether or not it is listed.
//
// A role no statement names holds what the platform grants new tables. That is
// not read: sql.default_execute is the platform's default for functions, not
// for tables, so a table's writers through a platform default privilege are not
// in the index (see the user guide).
// @enforces graph:INV-7
func BuildWriterIndex(kg schema.KnowledgeGraph, mods []ir.Module, apiRoles []string) *WriterIndex {
	ix := &WriterIndex{invs: map[string]*invWrites{}, tables: map[string]bool{}}
	syms := map[string]ir.Symbol{}
	var mem []ir.Membership
	var grants []ir.TableGrant
	for _, m := range mods {
		mem = append(mem, m.Memberships...)
		grants = append(grants, m.TableGrants...)
		ix.gaps = append(ix.gaps, m.WriterGaps...)
		for _, s := range m.Symbols {
			if s.IsTest {
				continue
			}
			syms[s.FQN] = s
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
	ix.roleWriters(grants, apiRoles, mem)

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
			for _, w := range sym.Writes {
				if w.Op == ir.WriteDelete {
					continue
				}
				if iw.columns[w.Table] == nil {
					iw.columns[w.Table] = map[string]bool{}
				}
				for _, c := range w.Columns {
					iw.columns[w.Table][c] = true
				}
			}
		}
	}
	for _, w := range ix.writers {
		ix.tables[w.Table] = true
	}
	for _, iw := range ix.invs {
		for t := range iw.columns {
			ix.tables[t] = true
		}
	}
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
	return ix
}

// roleWriters adds a writer for each role that holds INSERT, UPDATE or DELETE
// on a table, resolved as EXECUTE is: a role holds what it was granted or what
// PUBLIC holds, and what any role it is a member of holds, and a state the
// statements do not settle is an unknown that leaves the table's writer set
// incomplete.
func (ix *WriterIndex) roleWriters(grants []ir.TableGrant, apiRoles []string, mem []ir.Membership) {
	type tkey struct{ table, op string }
	by := map[tkey][]ir.TableGrant{}
	for _, g := range grants {
		k := tkey{g.Table, g.Op}
		by[k] = append(by[k], g)
	}
	keys := make([]tkey, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].table != keys[j].table {
			return keys[i].table < keys[j].table
		}
		return keys[i].op < keys[j].op
	})
	none := []string{} // the platform default for tables is not read: no role holds one
	for _, k := range keys {
		gs := by[k]
		roleSet := map[string]bool{ir.ExecPublic: true}
		cols := map[string]bool{"": true}
		for _, g := range gs {
			roleSet[g.Role] = true
			for _, c := range g.Columns {
				cols[c] = true
			}
		}
		for _, r := range apiRoles {
			roleSet[r] = true
		}
		type got struct {
			cols map[string]bool
			p    Privilege
		}
		granted := map[string]*got{}
		for _, col := range setList(cols) {
			sym := ir.Symbol{FQN: k.table, Execute: &ir.Execute{Roles: tableRoles(gs, col)}}
			for _, role := range setList(roleSet) {
				p := ExecutableBy(sym, role, &none, mem)
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
					ix.gaps = append(ix.gaps, ir.WriterGap{Table: k.table, File: p.File, Line: p.Line,
						Reason: fmt.Sprintf("whether %s can %s %s is not known from the migrations: %s", roleName(role), k.op, k.table, p.Reason)})
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
			columns := []string{wildcard}
			if !g.cols[""] {
				columns = setList(g.cols)
			}
			ix.writers = append(ix.writers, Writer{Kind: WriterRole, Name: role, Table: k.table, Op: k.op,
				Columns: columns, File: g.p.File, Line: g.p.Line, Via: g.p.Via})
		}
	}
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
func CheckWriters(kg schema.KnowledgeGraph, mods []ir.Module, apiRoles []string) []schema.Violation {
	return BuildWriterIndex(kg, mods, apiRoles).Findings()
}

// Findings are the UNGUARDED_WRITER and WRITERS_UNKNOWN findings.
func (ix *WriterIndex) Findings() []schema.Violation {
	var out []schema.Violation
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
					at = "granted at " + at
					if w.Via != "" {
						at += " " + w.Via
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
				Message: fmt.Sprintf("%s writes columns that %s's enforcers write, and is neither an enforcer of %s nor under a trigger or policy that enforces it, so no break ever targets it. %s",
					who, ref, ref, strings.Join(parts, "; ")),
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
	rep := WriterReport{Table: table, Column: column, Writers: []WriterEntry{}}
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
	rep.Incomplete = gapsFor(ix.gaps, table, cols)
	sort.SliceStable(rep.Incomplete, func(i, j int) bool { return rep.Incomplete[i].Reason < rep.Incomplete[j].Reason })
	return rep
}
