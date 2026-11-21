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
		p := ExecutableBy(c.sym, "authenticated", c.def)
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
	if v := CheckSQLConfig(ok, &ok); len(v) != 0 {
		t.Errorf("valid entries: %+v", v)
	}
	bad := []string{"Authenticated", "has space", ""}
	v := CheckSQLConfig(bad[:1], &bad)
	var subjects []string
	for _, f := range v {
		if f.Code != schema.CodeValidatePolicy || f.Severity != schema.SeverityError {
			t.Errorf("finding = %+v", f)
		}
		subjects = append(subjects, f.Subject)
	}
	want := "sql.api_roles[0] sql.default_execute[0] sql.default_execute[1] sql.default_execute[2]"
	if strings.Join(subjects, " ") != want {
		t.Errorf("subjects = %v, want %s", subjects, want)
	}
}
