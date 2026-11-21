package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// A function that trusts a verdict passed in as a parameter and is granted to
// the role every signed-in client uses: the enforcer is correct and tested,
// and still any client can call it with the verdict forged.
const exposureMigration = `-- Roles a plain Postgres lacks; a managed one has them.
do $$ begin
  if not exists (select 1 from pg_roles where rolname = 'authenticated') then create role authenticated nologin; end if;
  if not exists (select 1 from pg_roles where rolname = 'anon') then create role anon nologin; end if;
end $$;
`

const exposureSubmissions = `create table public.submissions (
  id uuid primary key default gen_random_uuid(),
  owner_id uuid not null,
  declared_size bigint not null check (declared_size > 0),
  status text not null default 'open' check (status in ('open', 'accepted', 'rejected')),
  reason text
);

-- @enforces review.submissions:INV-1
create function public.finish_submission(p_id uuid, p_ok boolean, p_size bigint) returns text
language plpgsql security definer set search_path = '' as $$
declare v public.submissions;
begin
  select * into v from public.submissions s where s.id = p_id for update;
  if not coalesce(p_ok, false) then
    update public.submissions s set status = 'rejected', reason = 'checker' where s.id = p_id;
    return 'rejected';
  end if;
  if p_size is distinct from v.declared_size then
    update public.submissions s set status = 'rejected', reason = 'size' where s.id = p_id;
    return 'rejected';
  end if;
  update public.submissions s set status = 'accepted' where s.id = p_id;
  return 'accepted';
end $$;
revoke all on function public.finish_submission(uuid, boolean, bigint) from public, anon;
grant execute on function public.finish_submission(uuid, boolean, bigint) to authenticated;
`

const exposureFix = "revoke execute on function public.finish_submission(uuid, boolean, bigint) from authenticated;\n"

const exposureTests = `begin;
select plan(1);
-- @verifies review.submissions:INV-1
select is(public.finish_submission('00000000-0000-0000-0000-00000000000a', false, 100), 'rejected', 'a refused file is rejected');
select * from finish();
rollback;
`

const exposureSecurityTest = `begin;
select plan(1);
set local role authenticated;
-- @verifies review.submissions:INV-1#a
select throws_ok($$ select public.finish_submission('00000000-0000-0000-0000-0000000000ee', true, 100) $$,
  '42501', null, 'a signed-in client cannot pass the checker''s verdict itself');
select * from finish();
rollback;
`

const exposureFeature = `id: review.submissions
version: 1
status: production
purpose: |
  A user submits a file; a server-side checker inspects it and finishes the submission.
owners:
  business: product
  engineering: backend
invariants:
  - id: INV-1
    statement: A submission becomes accepted only when the checker found the file valid and its size equals the declared size.
    verifiable_by:
      - test
`

const exposureSecurityCase = `    cases:
      - id: a
        kind: security
        statement: A signed-in client cannot call the function with a verdict it made up.
        enforcer: sql.public.finish_submission
`

const exposureFeatureParent = `id: review
version: 1
status: production
purpose: |
  Parent feature of review.submissions.
owners:
  business: product
  engineering: backend
`

// exposureRun builds a workspace on disk from the example and returns the
// open findings of one validate run, with whether it passed.
func exposureRun(t *testing.T, workspaceExtra, configExtra string, files map[string]string) ([]schema.Violation, bool) {
	t.Helper()
	repo := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("lattice/workspace.yaml", "mode: embedded\n"+workspaceExtra)
	write("lattice/adapters.yaml", "adapters:\n  postgres:\n    enabled: true\n")
	write("lattice/config.yaml", "version: 1\n"+configExtra)
	write("lattice/features/review.yaml", exposureFeatureParent)
	write("supabase/migrations/20250101000000_roles.sql", exposureMigration)
	write("supabase/migrations/20260101000000_submissions.sql", exposureSubmissions)
	write("supabase/tests/01_submissions.sql", exposureTests)
	for rel, body := range files {
		write(rel, body)
	}
	out, err := run(t, repo, newValidateCommand)
	var rep validateReport
	if jerr := json.Unmarshal([]byte(out), &rep); jerr != nil || rep.FailOn == "" {
		t.Fatalf("validate output: %v (%v)\n%s", jerr, err, out)
	}
	return rep.Violations, rep.OK
}

func exposureFindings(v []schema.Violation) []schema.Violation {
	var out []schema.Violation
	for _, f := range v {
		if f.Code == schema.CodeEnforcerExposed || f.Code == schema.CodeExposureUnknown {
			out = append(out, f)
		}
	}
	return out
}

const apiRoles = "sql:\n  api_roles: [anon, authenticated]\n  default_execute: [anon, authenticated, service_role]\n"

// The four states of the exposure example. An enforcer an API role can
// execute is reported with the grant that decides it and its SECURITY
// DEFINER; revoking the grant, or a verified security case that calls it as
// the role, clears it; with no API roles declared the workspace says so once.
// @verifies adapters.postgres:INV-5
// @verifies adapters.postgres:INV-6
func TestEnforcerExposure(t *testing.T) {
	feature := map[string]string{"lattice/features/review/submissions.yaml": exposureFeature}

	t.Run("unfixed", func(t *testing.T) {
		v, ok := exposureRun(t, apiRoles, "", feature)
		got := exposureFindings(v)
		if len(got) != 1 || got[0].Code != schema.CodeEnforcerExposed {
			t.Fatalf("findings = %+v, want one ENFORCER_EXPOSED", got)
		}
		f := got[0]
		if f.Subject != "sql.public.finish_submission" || f.FeatureID != "review.submissions" || f.InvariantID != "INV-1" || f.Severity != schema.SeverityInfo {
			t.Errorf("finding = %+v", f)
		}
		for _, want := range []string{"review.submissions:INV-1", "authenticated (supabase/migrations/20260101000000_submissions.sql:27)",
			"SECURITY DEFINER (supabase/migrations/20260101000000_submissions.sql:11)"} {
			if !strings.Contains(f.Message, want) {
				t.Errorf("message %q lacks %q", f.Message, want)
			}
		}
		if strings.Contains(f.Message, "anon") {
			t.Errorf("anon was revoked: %q", f.Message)
		}
		if !ok {
			t.Errorf("an info finding must not fail validate: %+v", v)
		}
	})

	t.Run("fixed by a later migration", func(t *testing.T) {
		files := map[string]string{"lattice/features/review/submissions.yaml": exposureFeature,
			"supabase/migrations/20260201000000_fix.sql": exposureFix}
		v, _ := exposureRun(t, apiRoles, "", files)
		if got := exposureFindings(v); len(got) != 0 {
			t.Errorf("findings = %+v, want none", got)
		}
	})

	t.Run("cleared by a verified security case", func(t *testing.T) {
		files := map[string]string{
			"lattice/features/review/submissions.yaml": strings.TrimRight(exposureFeature, "\n") + "\n" + exposureSecurityCase,
			"supabase/tests/90_security.sql":           exposureSecurityTest,
		}
		v, _ := exposureRun(t, apiRoles, "", files)
		if got := exposureFindings(v); len(got) != 0 {
			t.Errorf("findings = %+v, want none", got)
		}
		// The same case with no test that verifies it clears nothing.
		files["supabase/tests/90_security.sql"] = strings.Replace(exposureSecurityTest, "#a", "", 1)
		v, _ = exposureRun(t, apiRoles, "", files)
		if got := exposureFindings(v); len(got) != 1 || got[0].Code != schema.CodeEnforcerExposed {
			t.Errorf("an unverified case: findings = %+v, want ENFORCER_EXPOSED", got)
		}
	})

	t.Run("no API roles declared", func(t *testing.T) {
		v, _ := exposureRun(t, "", "", feature)
		got := exposureFindings(v)
		if len(got) != 1 || got[0].Code != schema.CodeExposureUnknown || got[0].Severity != schema.SeverityInfo {
			t.Fatalf("findings = %+v, want one EXPOSURE_UNKNOWN", got)
		}
		if !strings.Contains(got[0].Message, "1 SQL function") || !strings.Contains(got[0].Message, "no API roles are declared") {
			t.Errorf("message = %q", got[0].Message)
		}
	})

	t.Run("a role the statements cannot place is unknown", func(t *testing.T) {
		// PUBLIC revoked and no default_execute declared: service_role is not
		// named, so what it holds is the platform's default, which is not known.
		extra := "sql:\n  api_roles: [anon, authenticated, service_role]\n"
		files := map[string]string{"lattice/features/review/submissions.yaml": exposureFeature,
			"supabase/migrations/20260201000000_fix.sql": exposureFix}
		v, _ := exposureRun(t, extra, "", files)
		got := exposureFindings(v)
		if len(got) != 1 || got[0].Code != schema.CodeExposureUnknown || !strings.Contains(got[0].Message, "service_role") ||
			!strings.Contains(got[0].Message, "default_execute is not declared") {
			t.Errorf("findings = %+v", got)
		}
	})
}

// The exposure codes are info whatever else is configured: a workspace that
// fails on warnings still passes with an exposed enforcer, and only an
// explicit validate.severity makes the finding fail the run.
// @verifies adapters.postgres:INV-6
func TestEnforcerExposureNeverFailsByDefault(t *testing.T) {
	feature := map[string]string{"lattice/features/review/submissions.yaml": exposureFeature}
	// The example has no BRD, which is its own warning; set it aside so the
	// only open finding is the exposure.
	const failOnWarning = "validate:\n  fail_on: warning\n  severity:\n    FEATURE_NO_BRD: off\n"
	v, ok := exposureRun(t, apiRoles, failOnWarning, feature)
	if got := exposureFindings(v); len(got) != 1 || got[0].Severity != schema.SeverityInfo {
		t.Fatalf("findings = %+v, want one info ENFORCER_EXPOSED", got)
	}
	if !ok {
		t.Errorf("fail_on: warning failed on an info finding: %+v", v)
	}
	v, ok = exposureRun(t, apiRoles, failOnWarning+"    ENFORCER_EXPOSED: warning\n", feature)
	if got := exposureFindings(v); len(got) != 1 || got[0].Severity != schema.SeverityWarning || ok {
		t.Errorf("with severity set: findings = %+v, ok = %v", got, ok)
	}
}

// An API role that is not a lower-case SQL identifier is a policy error, not
// a role that silently matches nothing.
// @verifies adapters.postgres:INV-6
func TestSQLConfigBadRole(t *testing.T) {
	v, ok := exposureRun(t, "sql:\n  api_roles: [Authenticated]\n  default_execute: []\n", "",
		map[string]string{"lattice/features/review/submissions.yaml": exposureFeature})
	found := false
	for _, f := range v {
		if f.Code == schema.CodeValidatePolicy && f.Subject == "sql.api_roles[0]" {
			found = true
		}
	}
	if !found || ok {
		t.Errorf("want a VALIDATE_POLICY error for sql.api_roles[0]; ok = %v, %+v", ok, v)
	}
}
