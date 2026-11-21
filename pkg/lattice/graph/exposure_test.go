package graph

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

func execSym(roles ...ir.ExecuteGrant) ir.Symbol {
	return ir.Symbol{FQN: "sql.public.f", File: "m.sql", Line: 1, Execute: &ir.Execute{Roles: roles}}
}

func grant(role, state string, line int) ir.ExecuteGrant {
	return ir.ExecuteGrant{Role: role, State: state, File: "m.sql", Line: line}
}

var pubDefault = ir.ExecuteGrant{Role: ir.ExecPublic, State: ir.ExecGranted, File: "m.sql", Line: 1, Default: true}

// A role holds EXECUTE when it was granted it or PUBLIC holds it; a role no
// statement names, once PUBLIC is revoked, holds what the platform grants
// new functions, which sql.default_execute declares and an unset one leaves
// unknown.
// @verifies adapters.postgres:INV-5
func TestExecutableBy(t *testing.T) {
	list := func(r ...string) *[]string { return &r }
	for _, c := range []struct {
		name string
		sym  ir.Symbol
		def  *[]string
		want string
	}{
		{"public default", execSym(pubDefault), nil, "granted default, m.sql:1"},
		{"direct revoke does not close PUBLIC", execSym(pubDefault, grant("authenticated", ir.ExecRevoked, 5)), nil, "granted default, m.sql:1"},
		{"public revoked, role granted", execSym(grant("public", ir.ExecRevoked, 2), grant("authenticated", ir.ExecGranted, 3)), nil, "granted m.sql:3"},
		{"public and role revoked", execSym(grant("public", ir.ExecRevoked, 2), grant("authenticated", ir.ExecRevoked, 3)), nil, "revoked m.sql:3"},
		{"unnamed, default_execute lists it", execSym(grant("public", ir.ExecRevoked, 2)), list("authenticated"), "granted default, m.sql:1"},
		{"unnamed, default_execute lacks it", execSym(grant("public", ir.ExecRevoked, 2)), list("service_role"), "revoked m.sql:2"},
		{"unnamed, default_execute empty", execSym(grant("public", ir.ExecRevoked, 2)), list(), "revoked m.sql:2"},
		{"unnamed, default_execute unset", execSym(grant("public", ir.ExecRevoked, 2)), nil, "unknown"},
		{"unknown public", execSym(grant("public", ir.ExecUnknown, 2)), list("authenticated"), "unknown"},
		{"unknown role, public revoked", execSym(grant("public", ir.ExecRevoked, 2), grant("authenticated", ir.ExecUnknown, 3)), list("authenticated"), "unknown"},
		{"whole set unknown", ir.Symbol{FQN: "f", Execute: &ir.Execute{Unknown: "overloaded", Roles: []ir.ExecuteGrant{grant("authenticated", ir.ExecGranted, 3)}}}, nil, "unknown"},
		{"no facts", ir.Symbol{FQN: "f"}, nil, "unknown"},
	} {
		p := ExecutableBy(c.sym, "authenticated", c.def, nil)
		got := p.State
		if p.State != ir.ExecUnknown {
			got += " " + cite(p)
		}
		if got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// sql.api_roles and sql.default_execute entries are lower-case SQL
// identifiers.
// @verifies adapters.postgres:INV-6
func TestCheckSQLConfig(t *testing.T) {
	ok := []string{"anon", "service_role", "_x1"}
	if v := CheckSQLConfig(ok, &ok, &ok); len(v) != 0 {
		t.Errorf("valid entries: %+v", v)
	}
	bad := []string{"Authenticated", "has space", ""}
	v := CheckSQLConfig(bad[:1], &bad, nil)
	var subjects []string
	for _, f := range v {
		if f.Code != schema.CodeValidatePolicy || f.Severity != schema.SeverityError {
			t.Errorf("finding = %+v", f)
		}
		subjects = append(subjects, f.Subject)
	}
	if v := CheckSQLConfig(nil, nil, &bad); len(v) != 3 || v[0].Subject != "sql.default_write[0]" {
		t.Errorf("default_write: %+v", v)
	}
	want := "sql.api_roles[0] sql.default_execute[0] sql.default_execute[1] sql.default_execute[2]"
	if strings.Join(subjects, " ") != want {
		t.Errorf("subjects = %v, want %s", subjects, want)
	}
}

func member(m, r, state string, line int) ir.Membership {
	return ir.Membership{Member: m, Role: r, State: state, File: "m.sql", Line: line, Reason: "why"}
}

// A role holds EXECUTE through any role it is a member of, transitively; an
// unknown membership gives unknown unless another path grants; a cycle ends.
// @verifies adapters.postgres:INV-5
func TestExecutableByMembership(t *testing.T) {
	revokedPublic := grant("public", ir.ExecRevoked, 1)
	sym := execSym(revokedPublic, grant("authenticated", ir.ExecRevoked, 2), grant("app_writer", ir.ExecGranted, 3))
	def := &[]string{}
	show := func(p Privilege) string {
		s := p.State
		if p.State == ir.ExecGranted {
			s += " " + p.Via + " " + cite(p)
		}
		return s
	}
	for _, c := range []struct {
		name string
		sym  ir.Symbol
		mem  []ir.Membership
		want string
	}{
		{"no membership", sym, nil, "revoked"},
		{"member of a granted role", sym, []ir.Membership{member("authenticated", "app_writer", ir.ExecGranted, 4)},
			"granted via app_writer (m.sql:4) m.sql:3"},
		{"membership revoked", sym, []ir.Membership{member("authenticated", "app_writer", ir.ExecRevoked, 5)}, "revoked"},
		{"two hops", sym, []ir.Membership{member("authenticated", "mid", ir.ExecGranted, 4), member("mid", "app_writer", ir.ExecGranted, 5)},
			"granted via mid (m.sql:4) via app_writer (m.sql:5) m.sql:3"},
		{"cycle that grants nothing", execSym(revokedPublic, grant("authenticated", ir.ExecRevoked, 2)),
			[]ir.Membership{member("authenticated", "a", ir.ExecGranted, 4), member("a", "b", ir.ExecGranted, 5),
				member("b", "authenticated", ir.ExecGranted, 6), member("b", "a", ir.ExecGranted, 7)}, "revoked"},
		{"cycle that reaches a grant", sym,
			[]ir.Membership{member("authenticated", "a", ir.ExecGranted, 4), member("a", "authenticated", ir.ExecGranted, 5),
				member("a", "app_writer", ir.ExecGranted, 6)}, "granted via a (m.sql:4) via app_writer (m.sql:6) m.sql:3"},
		{"unknown membership", sym, []ir.Membership{member("authenticated", "app_writer", ir.ExecUnknown, 4)}, "unknown"},
		{"unknown, but another path grants", sym, []ir.Membership{member("authenticated", "app_writer", ir.ExecUnknown, 4),
			member("authenticated", "mid", ir.ExecGranted, 5), member("mid", "app_writer", ir.ExecGranted, 6)},
			"granted via mid (m.sql:5) via app_writer (m.sql:6) m.sql:3"},
		{"unknown membership to a role without it", sym, []ir.Membership{member("authenticated", "other", ir.ExecUnknown, 4)}, "revoked"},
		{"every role unknown", sym, []ir.Membership{member(ir.MemberAny, ir.MemberAny, ir.ExecUnknown, 4)}, "unknown"},
		{"this role unknown", sym, []ir.Membership{member("authenticated", ir.MemberAny, ir.ExecUnknown, 4)}, "unknown"},
	} {
		if got := show(ExecutableBy(c.sym, "authenticated", def, c.mem)); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
