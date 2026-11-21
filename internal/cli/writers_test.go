package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// A job queue whose promise is enforced in one function while a second
// function writes the same columns with no cap.
const writersPreamble = `do $$ begin
  if not exists (select 1 from pg_roles where rolname = 'authenticated') then create role authenticated nologin; end if;
  if not exists (select 1 from pg_roles where rolname = 'anon') then create role anon nologin; end if;
end $$;
`

const writersJobs = `create table public.jobs (
  id bigint generated always as identity primary key,
  status text not null default 'pending' check (status in ('pending', 'running', 'done', 'failed')),
  attempts int not null default 0,
  run_after timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

-- A second writer of status and attempts, with no cap.
create function public.claim_jobs(p_limit int) returns setof public.jobs
language sql security definer set search_path = '' as $$
  update public.jobs j set status = 'running', attempts = j.attempts + 1, updated_at = now()
   where j.id in (
     select c.id from public.jobs c
      where (c.status = 'pending' and c.run_after <= now())
         or (c.status = 'running' and c.updated_at < now() - interval '15 minutes')
      order by c.id limit p_limit for update skip locked)
  returning j.*;
$$;

-- @enforces queue.jobs:INV-1
create function public.fail_job(p_id bigint, p_retry_seconds int) returns text
language plpgsql security definer set search_path = '' as $$
declare v_attempts int;
begin
  select j.attempts into v_attempts from public.jobs j where j.id = p_id for update;
  if v_attempts >= 3 then
    update public.jobs j set status = 'failed', updated_at = now() where j.id = p_id;
    return 'failed';
  end if;
  update public.jobs j set status = 'pending', run_after = now() + make_interval(secs => p_retry_seconds), updated_at = now()
   where j.id = p_id;
  return 'pending';
end $$;
revoke all on function public.claim_jobs(int) from public, anon, authenticated;
revoke all on function public.fail_job(bigint, int) from public, anon, authenticated;
`

// The later migration rewrites claim_jobs so that a job past its 3rd attempt
// ends as failed. It carries no tag.
const writersFix = `create or replace function public.claim_jobs(p_limit int) returns setof public.jobs
language plpgsql security definer set search_path = '' as $$
begin
  update public.jobs j set status = 'failed', updated_at = now()
   where j.status = 'running' and j.updated_at < now() - interval '15 minutes' and j.attempts >= 3;
  return query
  update public.jobs j set status = 'running', attempts = j.attempts + 1, updated_at = now()
   where j.id in (
     select c.id from public.jobs c
      where (c.status = 'pending' and c.run_after <= now())
         or (c.status = 'running' and c.updated_at < now() - interval '15 minutes')
      order by c.id limit p_limit for update skip locked)
  returning j.*;
end $$;
`

const writersTests = `begin;
select plan(4);
insert into public.jobs default values;
select is((select count(*) from public.claim_jobs(1))::int, 1, 'a pending job is claimed');
-- @verifies queue.jobs:INV-1
select is(public.fail_job((select max(id) from public.jobs), 0), 'pending', 'a failure before the 3rd attempt retries');
update public.jobs set attempts = 3, status = 'running';
-- @verifies queue.jobs:INV-1
select is(public.fail_job((select max(id) from public.jobs), 0), 'failed', 'a failure on the 3rd attempt ends the job as failed');
-- @verifies queue.jobs:INV-1
select is((select count(*) from public.claim_jobs(1))::int, 0, 'and a failed job is never claimed again');
select * from finish();
rollback;
`

// A test that drives the claim path until the cap should hold.
const writersCrashLoop = `begin;
-- @verifies queue.jobs:INV-1
select plan(1);
insert into public.jobs default values;
do $$ begin
  for i in 1..5 loop
    perform public.claim_jobs(1);
    update public.jobs set updated_at = now() - interval '16 minutes';
  end loop;
end $$;
select ok((select attempts from public.jobs order by id desc limit 1) <= 3, 'a job is never tried more than 3 times');
select * from finish();
rollback;
`

const writersFeatureParent = `id: queue
version: 1
status: production
purpose: |
  Parent feature of queue.jobs.
owners:
  business: product
  engineering: backend
`

const writersFeature = `id: queue.jobs
version: 1
status: production
purpose: |
  A worker claims jobs, runs them and reports failures. A job that keeps failing must stop being retried.
owners:
  business: product
  engineering: backend
invariants:
  - id: INV-1
    statement: A job is tried at most 3 times; after its 3rd attempt it is never claimed again and ends as failed.
    verifiable_by:
      - test
`

// writersRepo writes the example to disk. files are added or replace.
func writersRepo(t *testing.T, workspaceExtra, configExtra string, files map[string]string) string {
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
	write("lattice/features/queue.yaml", writersFeatureParent)
	write("lattice/features/queue/jobs.yaml", writersFeature)
	write("supabase/migrations/20250101000000_roles.sql", writersPreamble)
	write("supabase/migrations/20260101000000_jobs.sql", writersJobs)
	write("supabase/tests/01_jobs.sql", writersTests)
	for rel, body := range files {
		write(rel, body)
	}
	return repo
}

func validateWriters(t *testing.T, repo string) ([]schema.Violation, validateReport) {
	t.Helper()
	out, err := run(t, repo, newValidateCommand)
	var rep validateReport
	if jerr := json.Unmarshal([]byte(out), &rep); jerr != nil || rep.FailOn == "" {
		t.Fatalf("validate output: %v (%v)\n%s", jerr, err, out)
	}
	var got []schema.Violation
	for _, f := range rep.Violations {
		if f.Code == schema.CodeUnguardedWriter || f.Code == schema.CodeWritersUnknown {
			got = append(got, f)
		}
	}
	return got, rep
}

// The four states of the competing-writers example. An untagged second writer
// of the columns an enforcer writes is reported with the shared columns and
// where they are written; rewriting it in a later migration changes nothing
// Lattice can see; tagging it makes it an enforcer, which the break runs then
// cover; and an allowlist entry with a reason accepts it.
// @verifies graph:INV-7
func TestUnguardedWriterExample(t *testing.T) {
	t.Run("unfixed", func(t *testing.T) {
		got, rep := validateWriters(t, writersRepo(t, "", "", nil))
		if len(got) != 1 || got[0].Code != schema.CodeUnguardedWriter {
			t.Fatalf("findings = %+v, want one UNGUARDED_WRITER", got)
		}
		f := got[0]
		if f.Subject != "sql.public.claim_jobs" || f.FeatureID != "queue.jobs" || f.InvariantID != "INV-1" || f.Severity != schema.SeverityInfo {
			t.Errorf("finding = %+v", f)
		}
		const file = "supabase/migrations/20260101000000_jobs.sql:12"
		for _, want := range []string{"status (" + file + ")", "updated_at (" + file + ")", "public.jobs by update", "queue.jobs:INV-1"} {
			if !strings.Contains(f.Message, want) {
				t.Errorf("message %q lacks %q", f.Message, want)
			}
		}
		if strings.Contains(f.Message, "attempts") || strings.Contains(f.Message, "run_after") {
			t.Errorf("only the columns both write are shared: %q", f.Message)
		}
		if !rep.OK {
			t.Errorf("an info finding must not fail validate")
		}
	})

	t.Run("rewritten in a later migration, no tag", func(t *testing.T) {
		got, _ := validateWriters(t, writersRepo(t, "", "", map[string]string{"supabase/migrations/20260201000000_fix.sql": writersFix}))
		if len(got) != 1 || got[0].Code != schema.CodeUnguardedWriter || got[0].Subject != "sql.public.claim_jobs" ||
			!strings.Contains(got[0].Message, "20260201000000_fix.sql") {
			t.Errorf("findings = %+v, want the rewritten claim_jobs still unguarded", got)
		}
	})

	t.Run("rewritten and tagged as an enforcer", func(t *testing.T) {
		files := map[string]string{
			"supabase/migrations/20260201000000_fix.sql": "-- @enforces queue.jobs:INV-1\n" + writersFix,
			"supabase/tests/90_crash_loop.sql":           writersCrashLoop,
		}
		got, _ := validateWriters(t, writersRepo(t, "", "", files))
		if len(got) != 0 {
			t.Errorf("findings = %+v, want none: claim_jobs is an enforcer now", got)
		}
	})

	t.Run("allowlisted with a reason", func(t *testing.T) {
		allow := "entries:\n  - code: UNGUARDED_WRITER\n    subject: sql.public.claim_jobs\n    reason: the claim path is reviewed by hand\n"
		got, rep := validateWriters(t, writersRepo(t, "", "", map[string]string{"lattice/allowlist.yaml": allow}))
		if len(got) != 0 {
			t.Errorf("findings = %+v, want none", got)
		}
		if len(rep.Allowlisted) != 1 || rep.Allowlisted[0].Code != schema.CodeUnguardedWriter {
			t.Errorf("allowlisted = %+v", rep.Allowlisted)
		}
	})
}

// Both codes are info whatever else is configured: a workspace that fails on
// warnings still passes with an unguarded writer, and only an explicit
// validate.severity makes the finding fail the run.
// @verifies graph:INV-7
func TestUnguardedWriterNeverFailsByDefault(t *testing.T) {
	// The example has no BRD, which is its own warning; set it aside so the
	// only open finding is the writer.
	const failOnWarning = "validate:\n  fail_on: warning\n  severity:\n    FEATURE_NO_BRD: off\n"
	got, rep := validateWriters(t, writersRepo(t, "", failOnWarning, nil))
	if len(got) != 1 || got[0].Severity != schema.SeverityInfo {
		t.Fatalf("findings = %+v, want one info UNGUARDED_WRITER", got)
	}
	if !rep.OK {
		t.Errorf("fail_on: warning failed on an info finding: %+v", rep.Violations)
	}
	got, rep = validateWriters(t, writersRepo(t, "", failOnWarning+"    UNGUARDED_WRITER: warning\n", nil))
	if len(got) != 1 || got[0].Severity != schema.SeverityWarning || rep.OK {
		t.Errorf("with severity set: findings = %+v, ok = %v", got, rep.OK)
	}
}

const writersGrant = "grant update (status, attempts) on public.jobs to authenticated;\n"

const writersPolicy = `-- @enforces queue.jobs:INV-1
create policy jobs_update_capped on public.jobs for update to authenticated using (attempts < 3) with check (attempts <= 3);
`

// A table privilege makes an API role a writer the code does not show:
// UPDATE (status) granted to authenticated is reported, and an enforcing
// policy FOR UPDATE guards it.
// @verifies graph:INV-7
func TestRoleWriterFromGrant(t *testing.T) {
	roles := "sql:\n  api_roles: [anon, authenticated]\n"
	fix := map[string]string{"supabase/migrations/20260201000000_fix.sql": "-- @enforces queue.jobs:INV-1\n" + writersFix}
	with := func(extra map[string]string) map[string]string {
		out := map[string]string{"supabase/migrations/20260301000000_grant.sql": writersGrant}
		for k, v := range fix {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	got, _ := validateWriters(t, writersRepo(t, roles, "", with(nil)))
	if len(got) != 1 || got[0].Subject != "sql.role.authenticated" ||
		!strings.Contains(got[0].Message, "role authenticated") ||
		!strings.Contains(got[0].Message, "status (granted at supabase/migrations/20260301000000_grant.sql:1)") {
		t.Fatalf("findings = %+v, want one for the role", got)
	}
	got, _ = validateWriters(t, writersRepo(t, roles, "", with(map[string]string{"supabase/migrations/20260301000001_policy.sql": writersPolicy})))
	if len(got) != 0 {
		t.Errorf("findings = %+v, want none under an enforcing FOR UPDATE policy", got)
	}
}

// A dynamic EXECUTE and an ON DELETE SET NULL foreign key make the writer set
// unknown: WRITERS_UNKNOWN, never an empty set.
// @verifies graph:INV-7
func TestWritersUnknownExample(t *testing.T) {
	files := map[string]string{
		"supabase/migrations/20260201000000_fix.sql": "-- @enforces queue.jobs:INV-1\n" + writersFix,
		"supabase/migrations/20260301000000_dyn.sql": `create function public.bump(t text) returns void language plpgsql as $$
begin execute format('update %I set updated_at = now()', t); end $$;
`,
	}
	got, _ := validateWriters(t, writersRepo(t, "", "", files))
	if len(got) != 1 || got[0].Code != schema.CodeWritersUnknown || got[0].Subject != "queue.jobs:INV-1" ||
		!strings.Contains(got[0].Message, "an EXECUTE at supabase/migrations/20260301000000_dyn.sql:2") {
		t.Fatalf("findings = %+v, want one WRITERS_UNKNOWN", got)
	}
	files = map[string]string{
		"supabase/migrations/20260201000000_fix.sql": "-- @enforces queue.jobs:INV-1\n" + writersFix,
		"supabase/migrations/20260301000000_fk.sql": `create table public.job_statuses (code text primary key);
alter table public.jobs add constraint jobs_status_fk foreign key (status) references public.job_statuses (code) on update cascade;
`,
	}
	got, _ = validateWriters(t, writersRepo(t, "", "", files))
	if len(got) != 1 || got[0].Code != schema.CodeWritersUnknown || !strings.Contains(got[0].Message, "ON UPDATE CASCADE on the foreign key of public.jobs (status)") {
		t.Errorf("findings = %+v, want WRITERS_UNKNOWN for the foreign key that rewrites status", got)
	}
}

// `lattice writers` lists each writer of a table or column with what guards
// it, ends with the reasons the set is incomplete or says it is complete, and
// has a --json form.
// @verifies graph:INV-7
func TestWritersCommand(t *testing.T) {
	repo := writersRepo(t, "", "", nil)
	var text bytes.Buffer
	io := &IO{Repo: repo, Out: &text, Err: &text}
	cmd := newWritersCommand(io)
	cmd.SetArgs([]string{"public.jobs"})
	cmd.SetOut(&text)
	cmd.SetErr(&text)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("%v\n%s", err, text.String())
	}
	for _, want := range []string{
		"public.jobs: 3 writer(s)",
		"sql.public.claim_jobs  update  attempts, status, updated_at  supabase/migrations/20260101000000_jobs.sql:12\n    unguarded for queue.jobs:INV-1",
		"sql.public.fail_job  update  status, updated_at  supabase/migrations/20260101000000_jobs.sql:28\n    guarded for queue.jobs:INV-1 by enforcer",
		"writer set complete",
	} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("text lacks %q:\n%s", want, text.String())
		}
	}

	out, err := run(t, repo, newWritersCommand, "jobs.run_after")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var rep struct {
		Contract string `json:"contract"`
		Table    string `json:"table"`
		Column   string `json:"column"`
		Complete bool   `json:"complete"`
		Writers  []struct {
			Kind      string   `json:"kind"`
			Name      string   `json:"name"`
			Columns   []string `json:"columns"`
			Line      int      `json:"line"`
			Unguarded []string `json:"unguarded"`
		} `json:"writers"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if rep.Contract != "lattice.writers/1" || rep.Table != "public.jobs" || rep.Column != "run_after" || !rep.Complete {
		t.Errorf("report = %+v", rep)
	}
	if len(rep.Writers) != 1 || rep.Writers[0].Name != "sql.public.fail_job" || rep.Writers[0].Kind != "function" {
		t.Errorf("only fail_job writes run_after: %+v", rep.Writers)
	}

	// An incomplete set ends with its reasons.
	repo = writersRepo(t, "", "", map[string]string{"supabase/migrations/20260301000000_dyn.sql": `create function public.bump(t text) returns void language plpgsql as $$
begin execute format('update %I set updated_at = now()', t); end $$;
`})
	out, err = run(t, repo, newWritersCommand, "public.jobs")
	if err != nil || !strings.Contains(out, `"complete": false`) || !strings.Contains(out, "builds its statement") && !strings.Contains(out, "names no table literally") {
		t.Errorf("incomplete: %v\n%s", err, out)
	}
}
