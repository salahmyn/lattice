package graph

// @module-feature graph

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// sqlRole is a role entry of sql.api_roles, sql.default_execute or sql.default_write: a
// lower-case SQL identifier.
var sqlRole = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// CheckSQLConfig reports each entry of the sql: block of workspace.yaml that
// is not a lower-case SQL identifier as a VALIDATE_POLICY error. A role that
// is not one can never match a statement's role, so it would silently hide
// an exposure.
func CheckSQLConfig(apiRoles []string, defaultExecute, defaultWrite *[]string) []schema.Violation {
	var out []schema.Violation
	check := func(key string, roles []string) {
		for i, r := range roles {
			if sqlRole.MatchString(r) {
				continue
			}
			out = append(out, schema.Violation{
				Code: schema.CodeValidatePolicy, Severity: schema.SeverityError,
				Subject:  fmt.Sprintf("sql.%s[%d]", key, i),
				Message:  fmt.Sprintf("workspace.yaml sql.%s[%d]: %q is not a lower-case SQL identifier", key, i, r),
				Location: &schema.Location{File: "workspace.yaml"},
				NextAction: &schema.NextAction{Kind: "edit_workspace",
					Detail: `write each role as the database names it, in lower case, like "authenticated" or "service_role"`},
			})
		}
	}
	check("api_roles", apiRoles)
	if defaultExecute != nil {
		check("default_execute", *defaultExecute)
	}
	if defaultWrite != nil {
		check("default_write", *defaultWrite)
	}
	return out
}

// Privilege is whether one role can execute a function, and the statement
// that decides it.
type Privilege struct {
	State   string // ir.ExecGranted, ir.ExecRevoked or ir.ExecUnknown
	File    string
	Line    int
	Default bool   // no statement decides it: PostgreSQL's or the platform's default
	Reason  string // why the state is unknown
	// Via is the membership chain a granted privilege came through, such as
	// "via app_writer (m.sql:4)"; empty when the role holds it itself.
	Via string
}

// ExecutableBy resolves the EXECUTE privilege of a role on a SQL function from
// the statements the adapter read (Symbol.Execute) and the declared
// sql.default_execute (nil when unset).
//
// A role holds EXECUTE when it was granted directly or PUBLIC holds it; a
// REVOKE from the role alone does not remove what PUBLIC grants. A role no
// statement names, once PUBLIC is revoked, holds what the platform grants
// new functions directly: granted when default_execute lists it, revoked when
// default_execute is declared without it, unknown when it is not declared.
// A role also holds EXECUTE through any role it is a member of (mem, as the
// adapter folded GRANT role TO role), followed transitively. A membership
// that is unknown (WITH INHERIT FALSE, a NOINHERIT member, a statement the
// adapter could not read) makes what lies behind it unknown unless another
// path already grants.
// Anything the adapter could not resolve is unknown, never revoked.
// @enforces adapters.postgres:INV-5
func ExecutableBy(sym ir.Symbol, role string, defaultExecute *[]string, mem []ir.Membership) Privilege {
	own := ownPrivilege(sym, role, defaultExecute)
	if own.State == ir.ExecGranted || len(mem) == 0 {
		return own
	}
	// A statement the adapter could not read to a member (or to any role)
	// may have added or removed any membership of it, so its memberships
	// are unknown, whatever an earlier statement said.
	wildAll, wild := "", map[string]string{}
	for _, m := range mem {
		if m.Role == ir.MemberAny && m.Member != ir.MemberAny {
			wild[m.Member] = m.Reason
		} else if m.Member == ir.MemberAny {
			wildAll = m.Reason
		}
	}
	edges := map[string][]ir.Membership{}
	for _, m := range mem {
		if m.State == ir.ExecRevoked || m.Member == ir.MemberAny || m.Role == ir.MemberAny {
			continue
		}
		if why, ok := wild[m.Member]; ok && m.State == ir.ExecGranted {
			m.State, m.Reason = ir.ExecUnknown, why
		} else if wildAll != "" && m.State == ir.ExecGranted {
			m.State, m.Reason = ir.ExecUnknown, wildAll
		}
		edges[m.Member] = append(edges[m.Member], m)
	}
	// walk reaches the roles role is a member of, nearest first. With
	// certain set only granted memberships are followed.
	type hop struct {
		role  string
		chain []ir.Membership
	}
	walk := func(certain bool) (out []hop) {
		seen := map[string]bool{role: true}
		queue := []hop{{role: role}}
		for len(queue) > 0 {
			h := queue[0]
			queue = queue[1:]
			if h.role != role {
				out = append(out, h)
			}
			for _, e := range edges[h.role] {
				if seen[e.Role] || certain && e.State != ir.ExecGranted {
					continue
				}
				seen[e.Role] = true
				queue = append(queue, hop{e.Role, append(append([]ir.Membership(nil), h.chain...), e)})
			}
		}
		return out
	}
	unknown := ""
	if own.State == ir.ExecUnknown {
		unknown = own.Reason
	}
	sure := map[string]bool{}
	for _, h := range walk(true) {
		sure[h.role] = true
		p := ownPrivilege(sym, h.role, defaultExecute)
		switch p.State {
		case ir.ExecGranted:
			var via []string
			for _, e := range h.chain {
				via = append(via, fmt.Sprintf("via %s (%s:%d)", e.Role, e.File, e.Line))
			}
			p.Via = strings.Join(via, " ")
			return p
		case ir.ExecUnknown:
			if unknown == "" {
				unknown = fmt.Sprintf("%s is a member of %s, whose privilege is unknown: %s", role, h.role, p.Reason)
			}
		}
	}
	for _, h := range walk(false) {
		if sure[h.role] {
			continue
		}
		last := h.chain[len(h.chain)-1]
		if p := ownPrivilege(sym, h.role, defaultExecute); p.State != ir.ExecRevoked && unknown == "" {
			unknown = fmt.Sprintf("%s may be a member of %s (%s:%d: %s)", role, h.role, last.File, last.Line, last.Reason)
		}
	}
	if unknown == "" {
		if why, ok := wild[role]; ok {
			unknown = why
		} else if wildAll != "" {
			unknown = wildAll
		}
	}
	if unknown != "" {
		return Privilege{State: ir.ExecUnknown, Reason: unknown}
	}
	return own
}

// ownPrivilege is what the statements and the platform default give the role
// itself, with no membership followed.
func ownPrivilege(sym ir.Symbol, role string, defaultExecute *[]string) Privilege {
	ex := sym.Execute
	if ex == nil {
		return Privilege{State: ir.ExecUnknown, Reason: "the adapter read no EXECUTE facts for " + sym.FQN}
	}
	if ex.Unknown != "" {
		return Privilege{State: ir.ExecUnknown, Reason: ex.Unknown}
	}
	find := func(r string) (ir.ExecuteGrant, bool) {
		for _, g := range ex.Roles {
			if g.Role == r {
				return g, true
			}
		}
		return ir.ExecuteGrant{}, false
	}
	of := func(g ir.ExecuteGrant) Privilege {
		p := Privilege{State: g.State, File: g.File, Line: g.Line, Default: g.Default, Reason: g.Reason}
		if g.Default {
			p.File, p.Line = sym.File, sym.Line
		}
		return p
	}
	pub, ok := find(ir.ExecPublic)
	if !ok { // facts that never went through Resolve: PostgreSQL's default
		pub = ir.ExecuteGrant{Role: ir.ExecPublic, State: ir.ExecGranted, File: sym.File, Line: sym.Line, Default: true}
	}
	direct, named := find(role)
	switch {
	case named && direct.State == ir.ExecGranted:
		return of(direct)
	case pub.State == ir.ExecGranted:
		return of(pub)
	case named && direct.State == ir.ExecUnknown:
		return of(direct)
	case pub.State == ir.ExecUnknown:
		return of(pub)
	case named:
		return of(direct)
	}
	// No statement names the role and PUBLIC is revoked.
	switch {
	case defaultExecute == nil:
		return Privilege{State: ir.ExecUnknown, File: pub.File, Line: pub.Line,
			Reason: fmt.Sprintf("no statement names %s and PUBLIC is revoked at %s:%d, so it holds what the platform grants new functions, and sql.default_execute is not declared",
				role, pub.File, pub.Line)}
	case containsRole(*defaultExecute, role):
		return Privilege{State: ir.ExecGranted, File: sym.File, Line: sym.Line, Default: true}
	}
	return Privilege{State: ir.ExecRevoked, File: pub.File, Line: pub.Line}
}

func containsRole(list []string, role string) bool {
	for _, r := range list {
		if r == role {
			return true
		}
	}
	return false
}

// CheckExposure reports the SQL functions that enforce an invariant and that
// a database API role can execute.
//
// ENFORCER_EXPOSED (info): one per (enforcer, invariant), naming the roles,
// the statement that decides each, and whether the function is SECURITY
// DEFINER. It does not fire when the invariant has a live security case whose
// enforcer is that function and at least one test verifies it, because that
// test is where the call as the API role is shown to fail.
//
// EXPOSURE_UNKNOWN (info): per enforcer when an API role's state cannot be
// told from the migrations, and once for the workspace when there are SQL
// enforcers but sql.api_roles is not declared.
//
// Each ENFORCER_EXPOSED states whether the function's body reads the caller's
// identity (auth.uid(), auth.jwt(), a request.jwt claim, current_user or
// session_user, directly or through a SQL function it calls), citing the first
// read; whether it authenticates by a hash of one of its parameters; and which
// parameters are declared boolean. The findings order: reads no identity and
// takes a boolean parameter, then reads no identity, then takes a boolean
// parameter, then the rest. Each is a fact about the body, never a pass: the
// finding fires either way, because a caller-supplied parameter can still
// decide the outcome.
//
// Both are info and stay on: a code that were off would hide an unknown.
// @enforces adapters.postgres:INV-6, graph:INV-9, graph:INV-10
func CheckExposure(kg schema.KnowledgeGraph, mods []ir.Module, apiRoles []string, defaultExecute *[]string) []schema.Violation {
	var mem []ir.Membership
	for _, m := range mods {
		mem = append(mem, m.Memberships...)
	}
	fns := map[string]ir.Symbol{}
	for _, m := range mods {
		for _, s := range m.Symbols {
			if s.Execute != nil && !s.IsTest {
				fns[s.FQN] = s
			}
		}
	}
	// enforcer FQN -> the invariants it enforces, as feature:INV refs.
	enforces := map[string][]string{}
	for _, s := range kg.Symbols {
		if _, ok := fns[s.FQN]; !ok {
			continue
		}
		for _, ref := range s.EnforcesInvariants {
			f, inv := schema.ResolveRef(ref, s.Feature)
			if f == "" || strings.HasPrefix(f, "brd.") {
				continue
			}
			full := f + ":" + inv
			if !containsRole(enforces[s.FQN], full) {
				enforces[s.FQN] = append(enforces[s.FQN], full)
			}
		}
	}
	fqns := make([]string, 0, len(enforces))
	for fqn := range enforces {
		fqns = append(fqns, fqn)
	}
	sort.Strings(fqns)
	if len(fqns) == 0 {
		return nil
	}
	if len(apiRoles) == 0 {
		return []schema.Violation{{
			Code: schema.CodeExposureUnknown, Severity: schema.SeverityInfo,
			Subject: "sql.api_roles", Location: &schema.Location{File: "workspace.yaml"},
			Message: fmt.Sprintf("%d SQL function(s) enforce invariants, and no API roles are declared (sql.api_roles in workspace.yaml), so who can execute them is not checked", len(fqns)),
			NextAction: &schema.NextAction{Kind: "edit_workspace", Field: "sql.api_roles",
				Detail: "declare the roles a client reaches the database as, e.g. api_roles: [anon, authenticated]"},
		}}
	}
	roles := append([]string(nil), apiRoles...)
	sort.Strings(roles)

	verified := verifiedCases(kg)
	var out []schema.Violation
	// rankOf is the sort rank of each ENFORCER_EXPOSED finding: 0 reads no
	// identity and takes a boolean parameter, 1 reads no identity, 2 takes a
	// boolean parameter, 3 the rest.
	rankOf := map[int]int{}
	for _, fqn := range fqns {
		sym := fns[fqn]
		loc := &schema.Location{File: sym.File, Line: sym.Line}
		var exposed, unknown []string
		for _, r := range roles {
			p := ExecutableBy(sym, r, defaultExecute, mem)
			switch p.State {
			case ir.ExecGranted:
				if p.Via != "" {
					exposed = append(exposed, fmt.Sprintf("%s %s, granted at %s", r, p.Via, cite(p)))
				} else {
					exposed = append(exposed, fmt.Sprintf("%s (%s)", r, cite(p)))
				}
			case ir.ExecUnknown:
				unknown = append(unknown, fmt.Sprintf("%s: %s", r, p.Reason))
			}
		}
		if len(unknown) > 0 {
			out = append(out, schema.Violation{
				Code: schema.CodeExposureUnknown, Severity: schema.SeverityInfo,
				Subject: fqn, Location: loc,
				Message: fmt.Sprintf("who can execute %s is not known from the migrations: %s", fqn, strings.Join(unknown, "; ")),
				NextAction: &schema.NextAction{Kind: "edit_migration",
					Detail: "state the grant or revoke as a plain GRANT/REVOKE statement, or declare sql.default_execute"},
			})
		}
		if len(exposed) == 0 {
			continue
		}
		definer := ""
		if sym.Execute.SecurityDefiner {
			definer = fmt.Sprintf("; it is SECURITY DEFINER (%s:%d)", sym.Execute.DefinerFile, sym.Execute.DefinerLine)
		}
		identity := identityFact(sym.Execute)
		reads := sym.Execute.Identity != nil || sym.Execute.HashAuth != nil
		bools := len(sym.Execute.BoolParams) > 0
		rank := 3
		switch {
		case !reads && bools:
			rank = 0
		case !reads:
			rank = 1
		case bools:
			rank = 2
		}
		for _, ref := range enforces[fqn] {
			if secured(kg, verified, fqn, ref) {
				continue
			}
			f, inv, _ := strings.Cut(ref, ":")
			rankOf[len(out)] = rank
			out = append(out, schema.Violation{
				Code: schema.CodeEnforcerExposed, Severity: schema.SeverityInfo,
				FeatureID: f, InvariantID: inv, Subject: fqn, Location: loc,
				Message: fmt.Sprintf("%s enforces %s and is executable by %s%s: a client can call it directly, so its inputs are the client's. %s",
					fqn, ref, strings.Join(exposed, ", "), definer, identity),
				NextAction: &schema.NextAction{Kind: "revoke_or_test", Ref: ref,
					Detail: "revoke EXECUTE from the API role, or add a security case naming this function as its enforcer and a test tagged @verifies " + ref + "#<case> that calls it as that role with forged inputs"},
			})
		}
	}
	rank := func(i int) int {
		if r, ok := rankOf[i]; ok {
			return r
		}
		return 3 // EXPOSURE_UNKNOWN keeps its place among the rest
	}
	idx := make([]int, len(out))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return rank(idx[a]) < rank(idx[b]) })
	sorted := make([]schema.Violation, len(out))
	for i, j := range idx {
		sorted[i] = out[j]
	}
	return sorted
}

// identityFact states what a function's body does about the caller's
// identity, and which parameters it takes that are boolean. It is a fact, not
// a verdict: no answer says the function is safe.
func identityFact(ex *ir.Execute) string {
	var b strings.Builder
	if id := ex.Identity; id != nil {
		fmt.Fprintf(&b, "Its body reads the caller's identity (%s at %s:%d); a caller-supplied parameter can still decide the outcome.", id.Via, id.File, id.Line)
	} else {
		b.WriteString("Its body reads no caller identity (auth.uid(), auth.jwt(), a request.jwt claim, current_user or session_user)")
		if ex.HashAuth != nil {
			b.WriteString(";")
		} else {
			b.WriteString(".")
		}
	}
	if h := ex.HashAuth; h != nil {
		if ex.Identity != nil {
			b.WriteString(" It")
		} else {
			b.WriteString(" it")
		}
		fmt.Fprintf(&b, " authenticates by a hash of parameter %s (%s:%d).", h.Param, h.File, h.Line)
	}
	if len(ex.BoolParams) > 0 {
		fmt.Fprintf(&b, " It takes boolean parameter(s): %s.", strings.Join(ex.BoolParams, ", "))
	}
	return b.String()
}

// cite says which statement decides a privilege.
func cite(p Privilege) string {
	if p.Default {
		return fmt.Sprintf("default, %s:%d", p.File, p.Line)
	}
	return fmt.Sprintf("%s:%d", p.File, p.Line)
}

// verifiedCases lists the cases some test verifies, as feature:INV-n#id.
func verifiedCases(kg schema.KnowledgeGraph) map[string]bool {
	out := map[string]bool{}
	for _, t := range kg.Tests {
		for _, ref := range t.VerifiesCases {
			base, id, _ := schema.SplitCaseRef(ref)
			f, inv := schema.ResolveRef(base, t.Feature)
			out[f+":"+inv+"#"+id] = true
		}
	}
	return out
}

// secured reports whether the invariant ref has a live security case whose
// enforcer is the function and that a test verifies.
func secured(kg schema.KnowledgeGraph, verified map[string]bool, fqn, ref string) bool {
	f, inv, _ := strings.Cut(ref, ":")
	for i := range kg.Features {
		m := &kg.Features[i]
		if m.ID != f {
			continue
		}
		for _, in := range m.Invariants {
			if in.ID != inv {
				continue
			}
			for _, cs := range in.Cases {
				if cs.Kind == schema.CaseSecurity && !cs.Retired && cs.Enforcer == fqn && verified[ref+"#"+cs.ID] {
					return true
				}
			}
		}
	}
	return false
}
