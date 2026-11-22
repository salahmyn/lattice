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

// ENFORCER_EXPOSED states whether the enforcer's body reads the caller's
// identity, citing its first read, or that it reads none. It fires either way,
// never words the function as safe, and lists the findings that read none
// first.
// @verifies graph:INV-9
func TestExposureStatesCallerIdentity(t *testing.T) {
	open := []ir.ExecuteGrant{grant(ir.ExecPublic, ir.ExecGranted, 2)}
	reads := ir.Symbol{FQN: "sql.public.a_reads", File: "m.sql", Line: 1, Execute: &ir.Execute{Roles: open,
		Identity: &ir.CallerIdentity{Via: "auth.uid()", File: "m.sql", Line: 7}}}
	none := ir.Symbol{FQN: "sql.public.b_none", File: "m.sql", Line: 20, Execute: &ir.Execute{Roles: open}}
	mods := []ir.Module{{File: "m.sql", Symbols: []ir.Symbol{reads, none}}}
	var kg schema.KnowledgeGraph
	for _, f := range []string{reads.FQN, none.FQN} {
		kg.Symbols = append(kg.Symbols, schema.GraphSymbol{FQN: f, Feature: "pay.cards", EnforcesInvariants: []string{"pay.cards:INV-1"}})
	}
	got := CheckExposure(kg, mods, []string{"authenticated"}, &[]string{})
	if len(got) != 2 || got[0].Code != schema.CodeEnforcerExposed || got[1].Code != schema.CodeEnforcerExposed {
		t.Fatalf("findings = %v", codesOf(got))
	}
	if got[0].Subject != none.FQN || !strings.Contains(got[0].Message, "reads no caller identity") {
		t.Errorf("the function that reads none comes first: %q", got[0].Message)
	}
	if got[1].Subject != reads.FQN || !strings.Contains(got[1].Message, "reads the caller's identity (auth.uid() at m.sql:7); a caller-supplied parameter can still decide the outcome") {
		t.Errorf("message = %q", got[1].Message)
	}
	for _, f := range got {
		if f.Severity != schema.SeverityInfo || strings.Contains(f.Message, "safe") {
			t.Errorf("finding = %+v: info, and never worded as a pass", f)
		}
	}
}

// ENFORCER_EXPOSED lists the findings in five ranks: takes a boolean parameter
// and reads no identity, takes a boolean parameter, reads no identity and
// authenticates by no hash, authenticates by a hash only, the rest. A boolean
// parameter that reads an identity still ranks before every function that has
// none: a caller-supplied verdict is the likelier hole. Each fact is stated,
// none is worded as a pass.
// @verifies graph:INV-10
func TestExposureFactsAndOrder(t *testing.T) {
	open := []ir.ExecuteGrant{grant(ir.ExecPublic, ir.ExecGranted, 2)}
	id := &ir.CallerIdentity{Via: "via public.is_admin() → auth.uid()", File: "m.sql", Line: 3}
	syms := []ir.Symbol{
		{FQN: "sql.public.a_rest", File: "m.sql", Line: 1, Execute: &ir.Execute{Roles: open, Identity: id}},
		{FQN: "sql.public.b_bool_reads", File: "m.sql", Line: 2, Execute: &ir.Execute{Roles: open, Identity: id, BoolParams: []string{"p_ok"}}},
		{FQN: "sql.public.c_none", File: "m.sql", Line: 3, Execute: &ir.Execute{Roles: open}},
		{FQN: "sql.public.d_hash", File: "m.sql", Line: 4, Execute: &ir.Execute{Roles: open, HashAuth: &ir.HashAuth{Param: "p_token", File: "m.sql", Line: 9}}},
		{FQN: "sql.public.e_none_bool", File: "m.sql", Line: 5, Execute: &ir.Execute{Roles: open, BoolParams: []string{"p_ok", "p_force"}}},
		{FQN: "sql.public.f_hash_bool", File: "m.sql", Line: 6, Execute: &ir.Execute{Roles: open, BoolParams: []string{"p_ok"},
			HashAuth: &ir.HashAuth{Param: "p_token", File: "m.sql", Line: 9}}},
	}
	mods := []ir.Module{{File: "m.sql", Symbols: syms}}
	var kg schema.KnowledgeGraph
	for _, s := range syms {
		kg.Symbols = append(kg.Symbols, schema.GraphSymbol{FQN: s.FQN, Feature: "pay.cards", EnforcesInvariants: []string{"pay.cards:INV-1"}})
	}
	got := CheckExposure(kg, mods, []string{"authenticated"}, &[]string{})
	var order []string
	for _, f := range got {
		order = append(order, strings.TrimPrefix(f.Subject, "sql.public."))
	}
	want := []string{"e_none_bool", "b_bool_reads", "f_hash_bool", "c_none", "d_hash", "a_rest"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", order, want)
	}
	msg := map[string]string{}
	for _, f := range got {
		msg[strings.TrimPrefix(f.Subject, "sql.public.")] = f.Message
		if f.Severity != schema.SeverityInfo || strings.Contains(f.Message, "safe") {
			t.Errorf("finding = %+v: info, and never worded as a pass", f)
		}
	}
	for fn, wants := range map[string][]string{
		"e_none_bool":  {"reads no caller identity", "It takes boolean parameter(s): p_ok, p_force."},
		"b_bool_reads": {"(via public.is_admin() → auth.uid() at m.sql:3)", "It takes boolean parameter(s): p_ok."},
		"d_hash":       {"authenticates by a hash of parameter p_token (m.sql:9)"},
	} {
		for _, w := range wants {
			if !strings.Contains(msg[fn], w) {
				t.Errorf("%s: %q lacks %q", fn, msg[fn], w)
			}
		}
	}
	if strings.Contains(msg["a_rest"], "boolean") || strings.Contains(msg["c_none"], "hash") {
		t.Errorf("a fact that does not hold is stated: %q / %q", msg["a_rest"], msg["c_none"])
	}
}
