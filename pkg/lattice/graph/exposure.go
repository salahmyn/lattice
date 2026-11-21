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

// sqlRole is a role entry of sql.api_roles or sql.default_execute: a
// lower-case SQL identifier.
var sqlRole = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// CheckSQLConfig reports each entry of the sql: block of workspace.yaml that
// is not a lower-case SQL identifier as a VALIDATE_POLICY error. A role that
// is not one can never match a statement's role, so it would silently hide
// an exposure.
func CheckSQLConfig(apiRoles []string, defaultExecute *[]string) []schema.Violation {
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
// Anything the adapter could not resolve is unknown, never revoked.
// @enforces adapters.postgres:INV-5
func ExecutableBy(sym ir.Symbol, role string, defaultExecute *[]string) Privilege {
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
// Both are info and stay on: a code that were off would hide an unknown.
// @enforces adapters.postgres:INV-6
func CheckExposure(kg schema.KnowledgeGraph, mods []ir.Module, apiRoles []string, defaultExecute *[]string) []schema.Violation {
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
	for _, fqn := range fqns {
		sym := fns[fqn]
		loc := &schema.Location{File: sym.File, Line: sym.Line}
		var exposed, unknown []string
		for _, r := range roles {
			p := ExecutableBy(sym, r, defaultExecute)
			switch p.State {
			case ir.ExecGranted:
				exposed = append(exposed, fmt.Sprintf("%s (%s)", r, cite(p)))
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
		for _, ref := range enforces[fqn] {
			if secured(kg, verified, fqn, ref) {
				continue
			}
			f, inv, _ := strings.Cut(ref, ":")
			out = append(out, schema.Violation{
				Code: schema.CodeEnforcerExposed, Severity: schema.SeverityInfo,
				FeatureID: f, InvariantID: inv, Subject: fqn, Location: loc,
				Message: fmt.Sprintf("%s enforces %s and is executable by %s%s: a client can call it directly, so its inputs are the client's",
					fqn, ref, strings.Join(exposed, ", "), definer),
				NextAction: &schema.NextAction{Kind: "revoke_or_test", Ref: ref,
					Detail: "revoke EXECUTE from the API role, or add a security case naming this function as its enforcer and a test tagged @verifies " + ref + "#<case> that calls it as that role with forged inputs"},
			})
		}
	}
	return out
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
