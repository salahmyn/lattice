package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
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
		// fail_job tests attempts (SELECT attempts INTO v ... IF v >= 3), so
		// attempts is the promise's column; the status and updated_at that
		// both functions merely assign are not.
		const file = "supabase/migrations/20260101000000_jobs.sql:12"
		for _, want := range []string{"attempts (" + file + ")", "public.jobs by update", "queue.jobs:INV-1", "columns from conditions"} {
			if !strings.Contains(f.Message, want) {
				t.Errorf("message %q lacks %q", f.Message, want)
			}
		}
		if strings.Contains(f.Message, "status (") || strings.Contains(f.Message, "updated_at (") || strings.Contains(f.Message, "run_after (") {
			t.Errorf("columns that are only assigned are not the promise's: %q", f.Message)
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
	roles := "sql:\n  api_roles: [anon, authenticated]\n  default_write: []\n"
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
		"sql.public.fail_job  update  status, updated_at  supabase/migrations/20260101000000_jobs.sql:28\n\n",
		"unguarded for queue.jobs:INV-1 (columns from conditions)",
		"writer set not known to be complete",
		"sql.api_roles is not declared",
	} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("text lacks %q:\n%s", want, text.String())
		}
	}
	// With no API roles declared, unnamed roles were not checked, so the set is
	// never called complete.
	if strings.Contains(text.String(), "writer set complete") {
		t.Errorf("text calls the writer set complete with roles unchecked:\n%s", text.String())
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

// roleFiles is the example with claim_jobs an enforcer and the migrations in
// extra, applied after it in the order of their names.
func roleFiles(extra map[string]string) map[string]string {
	out := map[string]string{"supabase/migrations/20260201000000_fix.sql": "-- @enforces queue.jobs:INV-1\n" + writersFix}
	for k, v := range extra {
		out["supabase/migrations/"+k] = v
	}
	return out
}

func roleSubjects(got []schema.Violation) string {
	var out []string
	for _, f := range got {
		out = append(out, f.Code+" "+f.Subject)
	}
	return strings.Join(out, ", ")
}

const (
	apiDeclared = "sql:\n  api_roles: [anon, authenticated]\n"
	rlsOn       = "alter table public.jobs enable row level security;\n"
	updPolicy   = "create policy jobs_update on public.jobs for update to authenticated using (true);\n"
)

// Row-level security decides role writes: a role that holds the privilege
// still needs a permissive policy for the command that reaches it.
// @verifies graph:INV-7
func TestRoleWriterRowLevelSecurity(t *testing.T) {
	roles := apiDeclared + "  default_write: []\n"
	run := func(extra map[string]string) string {
		got, _ := validateWriters(t, writersRepo(t, roles, "", roleFiles(extra)))
		return roleSubjects(got)
	}
	grant := map[string]string{"20260301000000_grant.sql": writersGrant}

	if got := run(grant); got != "UNGUARDED_WRITER sql.role.authenticated" {
		t.Errorf("privilege alone: %q", got)
	}
	if got := run(map[string]string{"20260301000000_grant.sql": writersGrant + rlsOn}); got != "" {
		t.Errorf("RLS enabled with no policy: %q, want no role writer", got)
	}
	if got := run(map[string]string{"20260301000000_grant.sql": writersGrant + rlsOn + updPolicy}); got != "UNGUARDED_WRITER sql.role.authenticated" {
		t.Errorf("RLS with an untagged update policy: %q", got)
	}
	tagged := "-- @enforces queue.jobs:INV-1\n" + updPolicy
	if got := run(map[string]string{"20260301000000_grant.sql": writersGrant + rlsOn + tagged}); got != "" {
		t.Errorf("the same policy tagged: %q, want none", got)
	}
	// A policy for another command, or only a restrictive one, allows no update.
	insertPolicy := "create policy jobs_insert on public.jobs for insert to authenticated with check (true);\n" +
		"create policy jobs_cap on public.jobs as restrictive for update to authenticated using (true);\n"
	if got := run(map[string]string{"20260301000000_grant.sql": writersGrant + rlsOn + insertPolicy}); got != "" {
		t.Errorf("insert and restrictive policies: %q, want no role writer", got)
	}
	// A policy for another role allows nothing for this one; PUBLIC allows all.
	other := "create policy jobs_update on public.jobs for update to anon using (true);\n"
	if got := run(map[string]string{"20260301000000_grant.sql": writersGrant + rlsOn + other}); got != "" {
		t.Errorf("a policy for another role: %q", got)
	}
	pub := "create policy jobs_update on public.jobs for all using (true);\n"
	if got := run(map[string]string{"20260301000000_grant.sql": writersGrant + rlsOn + pub}); got != "UNGUARDED_WRITER sql.role.authenticated" {
		t.Errorf("a policy to PUBLIC: %q", got)
	}
	// A later DISABLE re-opens the table to the privilege alone.
	files := map[string]string{"20260301000000_grant.sql": writersGrant + rlsOn, "20260401000000_off.sql": "alter table public.jobs disable row level security;\n"}
	if got := run(files); got != "UNGUARDED_WRITER sql.role.authenticated" {
		t.Errorf("RLS disabled later: %q", got)
	}
	// A policy created in dynamic SQL leaves the table's role writers unknown.
	dyn := "create function public.mk() returns void language plpgsql as $$ begin execute 'create policy p on public.jobs for update using (true)'; end $$;\n"
	if got := run(map[string]string{"20260301000000_grant.sql": writersGrant + rlsOn, "20260401000000_dyn.sql": dyn}); !strings.Contains(got, "WRITERS_UNKNOWN") {
		t.Errorf("a dynamic policy: %q, want WRITERS_UNKNOWN", got)
	}
}

// sql.default_write decides an API role no statement names: unset is unknown,
// [] is none, and a listed role is a writer that RLS can still block.
// @verifies graph:INV-7
func TestDefaultWrite(t *testing.T) {
	files := roleFiles(nil)
	got, _ := validateWriters(t, writersRepo(t, apiDeclared, "", files))
	if len(got) != 1 || got[0].Code != schema.CodeWritersUnknown || got[0].Subject != "queue.jobs:INV-1" ||
		!strings.Contains(got[0].Message, "no statement names authenticated on public.jobs, and sql.default_write is not declared") {
		t.Fatalf("default_write unset: %+v", got)
	}
	if got, _ := validateWriters(t, writersRepo(t, apiDeclared+"  default_write: []\n", "", files)); len(got) != 0 {
		t.Errorf("default_write: []: %+v, want no role writer", got)
	}
	got, _ = validateWriters(t, writersRepo(t, apiDeclared+"  default_write: [authenticated]\n", "", files))
	if len(got) != 1 || got[0].Subject != "sql.role.authenticated" || !strings.Contains(got[0].Message, "granted by the platform default (sql.default_write)") {
		t.Errorf("default_write listing the role: %+v", got)
	}
	blocked := roleFiles(map[string]string{"20260301000000_rls.sql": rlsOn})
	if got, _ := validateWriters(t, writersRepo(t, apiDeclared+"  default_write: [authenticated]\n", "", blocked)); len(got) != 0 {
		t.Errorf("RLS with no policy blocks the default: %+v", got)
	}
	// The writers command never calls an unknown set complete.
	out, err := run(t, writersRepo(t, apiDeclared, "", files), newWritersCommand, "public.jobs")
	if err != nil || !strings.Contains(out, `"complete": false`) || !strings.Contains(out, "sql.default_write is not declared") {
		t.Errorf("writers: %v\n%s", err, out)
	}
}

// writersFeatureColumns is writersFeature with the invariant's columns
// declared.
func writersFeatureColumns(columns ...string) string {
	body := strings.TrimRight(writersFeature, "\n") + "\n    columns:\n"
	for _, c := range columns {
		body += "      - " + c + "\n"
	}
	return body
}

// Declared columns replace the columns the enforcers' conditions name. The
// enforcer tests attempts, so claim_jobs, which increments it, is reported when
// nothing is declared. Declaring
// the promise's columns as run_after alone ties it to the writers of run_after,
// and claim_jobs, which never writes it, is no longer reported; declaring
// attempts reports it for attempts, a column no enforcer writes.
// @verifies graph:INV-8
func TestDeclaredColumnsReplaceDerived(t *testing.T) {
	jobs := func(columns ...string) map[string]string {
		if len(columns) == 0 {
			return nil
		}
		return map[string]string{"lattice/features/queue/jobs.yaml": writersFeatureColumns(columns...)}
	}
	t.Run("from conditions", func(t *testing.T) {
		got, _ := validateWriters(t, writersRepo(t, "", "", nil))
		if len(got) != 1 || !strings.Contains(got[0].Message, "from conditions") || !strings.Contains(got[0].Message, "attempts") {
			t.Fatalf("findings = %+v, want one finding on the columns from conditions", got)
		}
	})
	t.Run("declared and not written by the writer", func(t *testing.T) {
		got, _ := validateWriters(t, writersRepo(t, "", "", jobs("public.jobs.run_after")))
		if len(got) != 0 {
			t.Errorf("findings = %+v, want none: claim_jobs does not write run_after", got)
		}
	})
	t.Run("declared and written by the writer", func(t *testing.T) {
		got, _ := validateWriters(t, writersRepo(t, "", "", jobs("public.jobs.attempts")))
		if len(got) != 1 || got[0].Subject != "sql.public.claim_jobs" {
			t.Fatalf("findings = %+v, want claim_jobs", got)
		}
		m := got[0].Message
		if !strings.Contains(m, "declares") || !strings.Contains(m, "attempts") || strings.Contains(m, "status") || strings.Contains(m, "derived") || strings.Contains(m, "from conditions") {
			t.Errorf("message = %q: want the declared column attempts only", m)
		}
	})
	t.Run("case is folded", func(t *testing.T) {
		got, _ := validateWriters(t, writersRepo(t, "", "", jobs("Public.Jobs.Attempts")))
		if len(got) != 1 {
			t.Errorf("findings = %+v, want one: names fold to lower case", got)
		}
	})
}

// The writers command says, for each promise a writer is guarded or unguarded
// for, whether its columns were declared or derived.
// @verifies graph:INV-8
func TestWritersReportsColumnSource(t *testing.T) {
	var rep struct {
		Writers []struct {
			Name        string            `json:"name"`
			Unguarded   []string          `json:"unguarded"`
			ColumnsFrom map[string]string `json:"columns_from"`
		} `json:"writers"`
	}
	source := func(files map[string]string) string {
		out, err := run(t, writersRepo(t, "", "", files), newWritersCommand, "public.jobs.status")
		if err != nil {
			t.Fatalf("writers: %v\n%s", err, out)
		}
		if err := json.Unmarshal([]byte(out), &rep); err != nil {
			t.Fatal(err)
		}
		for _, w := range rep.Writers {
			if w.Name == "sql.public.claim_jobs" {
				return w.ColumnsFrom["queue.jobs:INV-1"]
			}
		}
		t.Fatalf("claim_jobs is not a writer of status: %s", out)
		return ""
	}
	if got := source(nil); got != "conditions" {
		t.Errorf("columns_from = %q, want conditions", got)
	}
	if got := source(map[string]string{"lattice/features/queue/jobs.yaml": writersFeatureColumns("public.jobs.status")}); got != "declared" {
		t.Errorf("columns_from = %q, want declared", got)
	}
}

// A declared column the migrations do not leave is an error, never a column
// that silently matches nothing: a malformed entry, a table the migrations do
// not create and a column a known table does not have.
// @verifies graph:INV-8
func TestDeclaredColumnsAreChecked(t *testing.T) {
	for _, tc := range []struct{ entry, want string }{
		{"jobs.status", "is not <schema>.<table>.<column>"},
		{"public.jobs.*", "is not <schema>.<table>.<column>"},
		{"public.tasks.status", "table public.tasks, which the migrations do not create"},
		{"public.jobs.statuss", "column statuss, which table public.jobs does not have"},
	} {
		t.Run(tc.entry, func(t *testing.T) {
			repo := writersRepo(t, "", "", map[string]string{"lattice/features/queue/jobs.yaml": writersFeatureColumns(tc.entry)})
			out, _ := run(t, repo, newValidateCommand)
			var rep validateReport
			if err := json.Unmarshal([]byte(out), &rep); err != nil {
				t.Fatal(err)
			}
			var found *schema.Violation
			for i, f := range rep.Violations {
				if f.Code == schema.CodeValidatePolicy && strings.Contains(f.Message, "queue.jobs:INV-1") {
					found = &rep.Violations[i]
				}
			}
			if found == nil || found.Severity != schema.SeverityError || !strings.Contains(found.Message, tc.want) {
				t.Fatalf("want a VALIDATE_POLICY error containing %q, got %+v", tc.want, rep.Violations)
			}
			if rep.OK {
				t.Errorf("a bad declared column must fail validate")
			}
		})
	}
}

// The finding on declared columns is info like the rest, so a workspace that
// fails on warnings stays green.
// @verifies graph:INV-8
func TestDeclaredColumnWriterNeverFailsByDefault(t *testing.T) {
	const failOnWarning = "validate:\n  fail_on: warning\n  severity:\n    FEATURE_NO_BRD: off\n"
	got, rep := validateWriters(t, writersRepo(t, "", failOnWarning,
		map[string]string{"lattice/features/queue/jobs.yaml": writersFeatureColumns("public.jobs.attempts")}))
	if len(got) != 1 || got[0].Severity != schema.SeverityInfo || !rep.OK {
		t.Errorf("findings = %+v, ok = %v: want one info finding and a green gate", got, rep.OK)
	}
}

// A second promise on the same table whose enforcer assigns status and
// updated_at but tests only owner_id, and a function that touches updated_at
// alone. The assigned columns are bookkeeping, not what the promise is about.
const writersOwner = `alter table public.jobs add column owner_id uuid;

-- @enforces queue.jobs:INV-2
create function public.reassign_job(p_id bigint, p_owner uuid) returns text
language plpgsql security definer set search_path = '' as $$
declare v_owner uuid;
begin
  select j.owner_id into v_owner from public.jobs j where j.id = p_id for update;
  if v_owner is not null then
    return 'owned';
  end if;
  update public.jobs j set status = 'pending', updated_at = now() where j.id = p_id;
  return 'reassigned';
end $$;

create function public.touch_job(p_id bigint) returns void
language sql security definer set search_path = '' as $$
  update public.jobs set updated_at = now() where id = p_id;
$$;

-- Renumbering writes id, which the other functions only use to pick a row.
create function public.renumber_job(p_id bigint, p_new bigint) returns void
language sql security definer set search_path = '' as $$
  update public.jobs set id = p_new where id = p_id;
$$;

create function public.assign_owner(p_id bigint, p_owner uuid) returns void
language sql security definer set search_path = '' as $$
  update public.jobs set owner_id = p_owner where id = p_id;
$$;
`

const writersOwnerFeature = `    verifiable_by:
      - test
  - id: INV-2
    statement: A job that has an owner is never handed to another one.
    verifiable_by:
      - test
`

const writersOwnerTest = `begin;
select plan(1);
-- @verifies queue.jobs:INV-2
select is(public.reassign_job(1, null), 'owned', 'an owned job is not reassigned');
select * from finish();
rollback;
`

func writersOwnerRepo(t *testing.T, configExtra string) string {
	t.Helper()
	feature := strings.Replace(writersFeature, "    verifiable_by:\n      - test\n", writersOwnerFeature, 1)
	return writersRepo(t, "", configExtra, map[string]string{
		"supabase/migrations/20260115000000_owner.sql": writersOwner,
		"supabase/tests/02_owner.sql":                  writersOwnerTest,
		"lattice/features/queue/jobs.yaml":             feature,
	})
}

// Two promises on one table. INV-1 is about attempts (fail_job tests it) and
// INV-2 about owner_id (reassign_job tests it); both enforcers assign status and
// updated_at. Only a writer of a promise's own column is reported for it:
// claim_jobs, which increments attempts, for INV-1; assign_owner for INV-2.
// touch_job writes updated_at alone and renumber_job writes id, which the
// enforcers only use as a row selector (WHERE id = p_id); neither is linked. Counted by
// columns derived from writes (what the enforcers assign) and from conditions.
// @verifies graph:INV-11
func TestWritersColumnsFromConditionsExample(t *testing.T) {
	got, _ := validateWriters(t, writersOwnerRepo(t, ""))
	var pairs []string
	for _, f := range got {
		if f.Code == schema.CodeUnguardedWriter {
			pairs = append(pairs, f.Subject+" "+f.InvariantID)
			if !strings.Contains(f.Message, "columns from conditions") {
				t.Errorf("%s: message lacks the source: %q", f.Subject, f.Message)
			}
		}
	}
	want := "sql.public.assign_owner INV-2, sql.public.claim_jobs INV-1"
	if g := strings.Join(sorted(pairs), ", "); g != want {
		t.Errorf("unguarded writers = %s, want %s", g, want)
	}
	// claim_jobs still breaks the attempts promise.
	for _, f := range got {
		if f.Subject == "sql.public.claim_jobs" && !strings.Contains(f.Message, "attempts") {
			t.Errorf("claim_jobs must stay unguarded on attempts: %q", f.Message)
		}
	}
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// The finding is info and always on: a gate that fails on warnings stays green
// with condition-derived columns.
// @verifies graph:INV-11
func TestColumnsFromConditionsStayInfo(t *testing.T) {
	const failOnWarning = "validate:\n  fail_on: warning\n  severity:\n    FEATURE_NO_BRD: off\n"
	got, rep := validateWriters(t, writersOwnerRepo(t, failOnWarning))
	if len(got) == 0 || !rep.OK {
		t.Fatalf("findings = %d, ok = %v: want findings and a green gate: %+v", len(got), rep.OK, rep.Violations)
	}
	for _, f := range got {
		if f.Severity != schema.SeverityInfo {
			t.Errorf("%s %s is %s, want info", f.Code, f.Subject, f.Severity)
		}
	}
}

// An enforcer whose only condition is a row selector (WHERE id = p_id) has no
// condition read, so its columns are the ones it writes, and the message says
// so; the id it selects by is not a column of the promise.
// @verifies graph:INV-11
func TestRowSelectorOnlyFallsBackToWrites(t *testing.T) {
	const only = `-- @enforces queue.jobs:INV-2
create function public.reassign_job(p_id bigint) returns void
language sql security definer set search_path = '' as $$
  update public.jobs set status = 'pending' where id = p_id;
$$;
create function public.renumber_job(p_id bigint, p_new bigint) returns void
language sql security definer set search_path = '' as $$
  update public.jobs set id = p_new where id = p_id;
$$;
create function public.park_job(p_id bigint) returns void
language sql security definer set search_path = '' as $$
  update public.jobs set status = 'parked' where id = p_id;
$$;
`
	feature := strings.Replace(writersFeature, "    verifiable_by:\n      - test\n", writersOwnerFeature, 1)
	repo := writersRepo(t, "", "", map[string]string{
		"supabase/migrations/20260115000000_owner.sql": only,
		"supabase/tests/02_owner.sql":                  strings.Replace(writersOwnerTest, "reassign_job(1, null)", "reassign_job(1)", 1),
		"lattice/features/queue/jobs.yaml":             feature,
	})
	got, _ := validateWriters(t, repo)
	var inv2 []string
	for _, f := range got {
		if f.InvariantID == "INV-2" {
			inv2 = append(inv2, f.Subject)
			if !strings.Contains(f.Message, "columns derived from writes") || strings.Contains(f.Message, "id (") {
				t.Errorf("%s: message = %q", f.Subject, f.Message)
			}
		}
	}
	// park_job writes status, which the enforcer writes; renumber_job writes only id.
	if g := strings.Join(sorted(inv2), ", "); !strings.Contains(g, "sql.public.park_job") || strings.Contains(g, "renumber_job") {
		t.Errorf("INV-2 writers = %s, want park_job and not renumber_job", g)
	}
}

// writersInv2Repo adds a second promise, INV-2, enforced by the functions in
// migration, to the job-queue example.
func writersInv2Repo(t *testing.T, migration string) string {
	t.Helper()
	feature := strings.Replace(writersFeature, "    verifiable_by:\n      - test\n", writersOwnerFeature, 1)
	return writersRepo(t, "", "", map[string]string{
		"supabase/migrations/20260115000000_inv2.sql": migration,
		"supabase/tests/02_inv2.sql":                  writersOwnerTest,
		"lattice/features/queue/jobs.yaml":            feature,
	})
}

// unguardedFor lists the writers reported for one invariant, with each message.
func unguardedFor(got []schema.Violation, inv string) map[string]string {
	out := map[string]string{}
	for _, f := range got {
		if f.Code == schema.CodeUnguardedWriter && f.InvariantID == inv {
			out[f.Subject] = f.Message
		}
	}
	return out
}

// A promise enforced by a function that only reads (a query that filters on a
// column) is about that column: a function that flips it is reported, and one
// that writes other columns of the table is not.
// @verifies graph:INV-12
func TestReadOnlyEnforcerLinksWriterOfItsFilter(t *testing.T) {
	const mig = `create table public.videos (
  id bigint primary key,
  title text,
  ai_pending boolean not null default true,
  updated_at timestamptz not null default now()
);
-- @enforces queue.jobs:INV-2
create function public.visible_videos() returns setof public.videos
language sql stable security definer set search_path = '' as $$
  select v.* from public.videos v where not v.ai_pending;
$$;
create function public.finish_enrich(p_id bigint) returns void
language sql security definer set search_path = '' as $$
  update public.videos set ai_pending = false, updated_at = now() where id = p_id;
$$;
create function public.rename_video(p_id bigint, p_title text) returns void
language sql security definer set search_path = '' as $$
  update public.videos set title = p_title, updated_at = now() where id = p_id;
$$;
`
	got, rep := validateWriters(t, writersInv2Repo(t, mig))
	inv2 := unguardedFor(got, "INV-2")
	if len(inv2) != 1 || !strings.Contains(inv2["sql.public.finish_enrich"], "columns from conditions") ||
		!strings.Contains(inv2["sql.public.finish_enrich"], "ai_pending (") {
		t.Fatalf("INV-2 writers = %v, want only finish_enrich, on ai_pending from conditions", inv2)
	}
	if !rep.OK {
		t.Errorf("an info finding must not fail validate")
	}
}

// A row selector is not a condition read when the other side is a field of a
// record variable or a hash of a parameter; what the function tests with now()
// still is. A writer of the selector column is not linked; a writer of the
// tested one is.
// @verifies adapters.postgres:INV-13
func TestRecordAndHashSelectorsAreNotColumns(t *testing.T) {
	const mig = `create table public.devices (
  id bigint primary key,
  token_hash text,
  name text,
  last_seen_at timestamptz
);
-- @enforces queue.jobs:INV-2
create function public.device_ping(p_token text) returns void
language plpgsql security definer set search_path = '' as $$
declare v_dev public.devices;
begin
  select * into v_dev from public.devices d
   where d.token_hash = encode(sha256(convert_to(p_token,'UTF8')),'hex');
  update public.devices d set last_seen_at = now()
   where d.id = v_dev.id
     and (d.last_seen_at is null or d.last_seen_at < now() - interval '1 minute');
end $$;
create function public.renumber_device(p_id bigint, p_new bigint) returns void
language sql security definer set search_path = '' as $$
  update public.devices set id = p_new where id = p_id;
$$;
create function public.rotate_token(p_id bigint, p_hash text) returns void
language sql security definer set search_path = '' as $$
  update public.devices set token_hash = p_hash where id = p_id;
$$;
create function public.mark_seen(p_id bigint) returns void
language sql security definer set search_path = '' as $$
  update public.devices set last_seen_at = null where id = p_id;
$$;
`
	got, _ := validateWriters(t, writersInv2Repo(t, mig))
	inv2 := unguardedFor(got, "INV-2")
	var names []string
	for n := range inv2 {
		names = append(names, n)
	}
	if g := strings.Join(sorted(names), ", "); g != "sql.public.mark_seen" {
		t.Fatalf("INV-2 writers = %s, want only mark_seen: id and token_hash only pick the row", g)
	}
	if m := inv2["sql.public.mark_seen"]; !strings.Contains(m, "columns from conditions") || !strings.Contains(m, "last_seen_at (") {
		t.Errorf("message = %q", m)
	}
}

// Two tables that share column names: an enforcer that reads invites is not
// linked to a writer of family_invites, whether it names the table by alias,
// by its schema-qualified name or not at all.
// @verifies adapters.postgres:INV-14
func TestSameColumnsOnTwoTablesAreNotLinked(t *testing.T) {
	const mig = `create table public.invites (id bigint primary key, email text, expires_at timestamptz);
create table public.family_invites (id bigint primary key, email text, expires_at timestamptz, accepted boolean);
create table public.family_members (id bigint primary key, email text, expires_at timestamptz);
-- @enforces queue.jobs:INV-2
create function public.accept_invite(p_id bigint, p_email text) returns boolean
language plpgsql security definer set search_path = '' as $$
declare v_email text;
begin
  select email into v_email from public.invites where id = p_id and expires_at > now();
  if v_email is distinct from p_email then
    return false;
  end if;
  if exists (select 1 from public.invites i where i.id = p_id and i.expires_at < now()) then
    return false;
  end if;
  return true;
end $$;
create function public.create_invite(p_email text) returns void
language sql security definer set search_path = '' as $$
  insert into public.family_invites (email, expires_at) values (p_email, now() + interval '7 days');
$$;
create function public.family_accept_invite(p_id bigint) returns void
language plpgsql security definer set search_path = '' as $$
begin
  update public.family_invites fi set accepted = true, email = fi.email, expires_at = now() where fi.id = p_id;
  insert into public.family_members (email, expires_at) select email, expires_at from public.family_invites where id = p_id;
end $$;
`
	got, _ := validateWriters(t, writersInv2Repo(t, mig))
	if inv2 := unguardedFor(got, "INV-2"); len(inv2) != 0 {
		t.Errorf("INV-2 writers = %v, want none: the enforcer reads invites only", inv2)
	}
}

// The same alias on two tables in one function names each in its own
// statement. A read of invites through i is not a read of family_invites, so a
// writer of family_invites.email and expires_at is not linked to a promise
// whose enforcer only tests invites and sets accepted.
// @verifies adapters.postgres:INV-14
func TestAliasReusedAcrossStatementsIsNotLinked(t *testing.T) {
	const mig = `create table public.invites (id bigint primary key, email text, expires_at timestamptz);
create table public.family_invites (id bigint primary key, email text, expires_at timestamptz, accepted boolean);
-- @enforces queue.jobs:INV-2
create function public.accept_invite(p_id bigint) returns boolean
language plpgsql security definer set search_path = '' as $$
declare v_exp timestamptz;
begin
  select i.expires_at into v_exp from public.invites i where i.id = p_id;
  if v_exp < now() then
    return false;
  end if;
  update public.family_invites i set accepted = true where i.id = p_id;
  return true;
end $$;
create function public.create_invite(p_email text) returns void
language sql security definer set search_path = '' as $$
  insert into public.family_invites (email, expires_at) values (p_email, now() + interval '7 days');
$$;
`
	got, _ := validateWriters(t, writersInv2Repo(t, mig))
	if inv2 := unguardedFor(got, "INV-2"); len(inv2) != 0 {
		t.Errorf("INV-2 writers = %v, want none", inv2)
	}
}

const ownershipDevices = `create table public.devices (
  id bigint primary key,
  parent_id uuid not null,
  kind text not null default 'phone',
  last_seen_at timestamptz
);
create function public.current_family() returns uuid language sql stable as $$ select auth.uid() $$;

-- The creator of a row: it names whose row it is, and no row exists yet.
create function public.register_device(p_id bigint, p_kind text) returns void
language sql security definer set search_path = '' as $$
  insert into public.devices (id, parent_id, kind) values (p_id, auth.uid(), p_kind);
$$;
-- A function that can reassign an existing row.
create function public.transfer_device(p_id bigint, p_parent uuid) returns void
language sql security definer set search_path = '' as $$
  update public.devices set parent_id = p_parent where id = p_id;
$$;
create function public.upsert_device(p_id bigint, p_parent uuid) returns void
language sql security definer set search_path = '' as $$
  insert into public.devices (id, parent_id) values (p_id, p_parent)
  on conflict (id) do update set parent_id = excluded.parent_id;
$$;
create function public.rename_kind(p_id bigint, p_kind text) returns void
language sql security definer set search_path = '' as $$
  update public.devices set kind = p_kind where id = p_id;
$$;
create function public.add_kind(p_id bigint, p_kind text) returns void
language sql security definer set search_path = '' as $$
  insert into public.devices (id, parent_id, kind) values (p_id, null, p_kind);
$$;
`

// ownershipWith is the devices migration with one enforcer of INV-2.
func ownershipWith(enforcer string) string {
	return ownershipDevices + "\n-- @enforces queue.jobs:INV-2\n" + enforcer
}

// A column an enforcer compares with the caller's identity is a column of
// ownership: it links only the writers that UPDATE it (also ON CONFLICT DO
// UPDATE), not the one that INSERTs a new row. The comparison is auth.uid(), a
// variable assigned only from it, or a helper that reads it. The finding names
// the column as ownership and where it is compared; --json columns_from stays
// conditions; and a plain column keeps its insert writers.
// @verifies graph:INV-13
func TestOwnershipColumnLinksOnlyUpdaters(t *testing.T) {
	for _, tc := range []struct{ name, enforcer, via string }{
		{"auth.uid()", `create function public.my_devices() returns setof public.devices
language sql stable security definer set search_path = '' as $$
  select d.* from public.devices d where d.parent_id = auth.uid();
$$;
`, "auth.uid()"},
		{"variable", `create function public.my_devices() returns setof public.devices
language plpgsql stable security definer set search_path = '' as $$
declare v_uid uuid := auth.uid();
begin
  return query select d.* from public.devices d where d.parent_id = v_uid;
end $$;
`, "v_uid, assigned only from auth.uid()"},
		{"identity helper", `create function public.my_devices() returns setof public.devices
language sql stable security definer set search_path = '' as $$
  select d.* from public.devices d where d.parent_id = public.current_family();
$$;
`, "public.current_family() → auth.uid()"},
		{"variable from an identity helper", `create function public.my_devices() returns setof public.devices
language plpgsql stable security definer set search_path = '' as $$
declare v_family uuid := public.current_family();
begin
  return query select d.* from public.devices d where d.parent_id = v_family;
end $$;
`, "v_family, assigned only from public.current_family() → auth.uid()"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, rep := validateWriters(t, writersInv2Repo(t, ownershipWith(tc.enforcer)))
			inv2 := unguardedFor(got, "INV-2")
			var names []string
			for n := range inv2 {
				names = append(names, n)
			}
			if g := strings.Join(sorted(names), ", "); g != "sql.public.transfer_device, sql.public.upsert_device" {
				t.Fatalf("INV-2 writers = %s, want transfer_device and upsert_device, not the insert-only register_device", g)
			}
			want := "parent_id (ownership: compared with " + tc.via + " at supabase/migrations/20260115000000_inv2.sql:"
			for n, msg := range inv2 {
				if !strings.Contains(msg, want) || !strings.Contains(msg, "columns from conditions") {
					t.Errorf("%s: message %q lacks %q", n, msg, want)
				}
			}
			if !rep.OK {
				t.Errorf("an info finding must not fail validate")
			}
		})
	}
}

// A column compared with something other than the caller's identity is not
// ownership: its INSERT writers still link, and the writers report says so.
// @verifies graph:INV-13
func TestPlainConditionColumnStillLinksInserts(t *testing.T) {
	got, _ := validateWriters(t, writersInv2Repo(t, ownershipWith(`create function public.phones() returns setof public.devices
language sql stable security definer set search_path = '' as $$
  select d.* from public.devices d where d.kind = 'phone';
$$;
`)))
	inv2 := unguardedFor(got, "INV-2")
	var names []string
	for n, msg := range inv2 {
		names = append(names, n)
		if strings.Contains(msg, "ownership") {
			t.Errorf("%s: kind is not an ownership column: %q", n, msg)
		}
	}
	if g := strings.Join(sorted(names), ", "); g != "sql.public.add_kind, sql.public.register_device, sql.public.rename_kind" {
		t.Errorf("INV-2 writers = %s, want every writer of kind (add_kind and register_device insert it)", g)
	}
}

// A column compared with a field of another row is not ownership, even in a
// guard: the owner it is compared with is that row's, not the caller's. Its
// INSERT writers still link.
// @verifies graph:INV-13
func TestColumnComparedWithRecordFieldIsNotOwnership(t *testing.T) {
	got, _ := validateWriters(t, writersInv2Repo(t, ownershipWith(`create function public.same_family(p_a bigint, p_b bigint) returns void
language plpgsql stable security definer set search_path = '' as $$
declare v_dev public.devices;
begin
  select * into v_dev from public.devices d where d.id = p_a;
  if not exists (select 1 from public.devices k where k.id = p_b and k.parent_id = v_dev.parent_id) then
    raise exception 'another family';
  end if;
end $$;
`)))
	inv2 := unguardedFor(got, "INV-2")
	msg, ok := inv2["sql.public.register_device"]
	if !ok {
		t.Fatalf("register_device inserts parent_id, compared with another row's owner, and must link: %v", inv2)
	}
	if strings.Contains(msg, "ownership") {
		t.Errorf("parent_id compared with v_dev.parent_id is not ownership: %q", msg)
	}
}

// `lattice writers` shows (ownership) beside an ownership column, on the
// insert writer that is not linked through it as on the updater that is.
// @verifies graph:INV-13
func TestWritersCommandMarksOwnership(t *testing.T) {
	repo := writersInv2Repo(t, ownershipWith(`create function public.my_devices() returns setof public.devices
language sql stable security definer set search_path = '' as $$
  select d.* from public.devices d where d.parent_id = auth.uid();
$$;
`))
	var out bytes.Buffer
	io := &IO{Repo: repo, Out: &out, Err: &out, Actor: "test"}
	cmd := newWritersCommand(io)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs([]string{"devices"})
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("writers: %v\n%s", err, out.String())
	}
	text := out.String()
	for _, want := range []string{"sql.public.register_device  insert  id, kind, parent_id (ownership)", "sql.public.transfer_device  update  parent_id (ownership)"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "rename_kind  update  kind (ownership)") {
		t.Errorf("kind is not ownership:\n%s", text)
	}
	i := strings.Index(text, "register_device")
	j := strings.Index(text, "transfer_device")
	if i < 0 || j < 0 || strings.Contains(text[i:j], "unguarded for") {
		t.Errorf("register_device is linked through its ownership column:\n%s", text)
	}
}

const guardedPieces = `create table public.admins (user_id uuid primary key);
create table public.settings (id int primary key, locked boolean not null default false);
create function public.is_admin() returns boolean language sql stable as $$
  select exists (select 1 from public.admins a where a.user_id = auth.uid())
$$;
create function public.ping_device(p_id bigint) returns void
language sql security definer set search_path = '' as $$
  update public.devices set last_seen_at = now() where id = p_id;
$$;
create function public.lock_settings() returns void
language sql security definer set search_path = '' as $$
  update public.settings set locked = true where id = 1;
$$;
create function public.hide_device(p_id bigint) returns void
language sql security definer set search_path = '' as $$
  update public.devices set kind = 'hidden' where id = p_id;
$$;
`

const guardedDevices = `create table public.devices (id bigint primary key, kind text not null default 'phone', last_seen_at timestamptz);
`

// guardedWith is the pieces with one enforcer of INV-2.
func guardedWith(enforcer string) string {
	return guardedDevices + guardedPieces + "\n-- @enforces queue.jobs:INV-2\n" + enforcer
}

// A read-only enforcer with an explicit guard is about its guard: the filter of
// its statistics query does not link the writers of the column it filters on. A
// guard that reads only identity leaves the invariant with no column, which is
// said, not passed in silence; a guard that reads a column counts that column; a
// read-only enforcer with no guard keeps counting its filters.
// @verifies graph:INV-14
func TestGuardedReadOnlyEnforcerCountsOnlyItsGuard(t *testing.T) {
	const stats = `if not public.is_admin() then raise exception 'forbidden'; end if;
  return (select count(*) from public.devices d where d.last_seen_at > now() - interval '7 days');
`
	t.Run("identity guard leaves no column, and says so", func(t *testing.T) {
		got, rep := validateWriters(t, writersInv2Repo(t, guardedWith(`create function public.admin_stats() returns bigint
language plpgsql stable security definer set search_path = '' as $$
begin
  `+stats+`end $$;
`)))
		if inv2 := unguardedFor(got, "INV-2"); len(inv2) != 0 {
			t.Errorf("the statistic filter links writers: %v", inv2)
		}
		var note string
		for _, f := range got {
			if f.Code == schema.CodeWritersUnknown && f.InvariantID == "INV-2" {
				note = f.Message
			}
		}
		for _, want := range []string{"queue.jobs:INV-2 ties no column to its writers", "sql.public.admin_stats", "(its guard reads no column Lattice can place on a table"} {
			if !strings.Contains(note, want) {
				t.Errorf("fallback note %q lacks %q", note, want)
			}
		}
		if !rep.OK {
			t.Errorf("an info finding must not fail validate")
		}
	})

	t.Run("a guard that reads a column counts it", func(t *testing.T) {
		got, _ := validateWriters(t, writersInv2Repo(t, guardedWith(`create function public.admin_stats() returns bigint
language plpgsql stable security definer set search_path = '' as $$
begin
  if (select s.locked from public.settings s where s.id = 1) then raise exception 'locked'; end if;
  return (select count(*) from public.devices d where d.last_seen_at > now() - interval '7 days');
end $$;
`)))
		inv2 := unguardedFor(got, "INV-2")
		if len(inv2) != 1 || !strings.Contains(inv2["sql.public.lock_settings"], "locked (") {
			t.Fatalf("INV-2 writers = %v, want only lock_settings, on locked", inv2)
		}
	})

	t.Run("an IF that neither raises nor returns is not a guard", func(t *testing.T) {
		got, _ := validateWriters(t, writersInv2Repo(t, guardedWith(`create function public.visible_devices(p_kind text) returns setof public.devices
language plpgsql stable security definer set search_path = '' as $$
begin
  if p_kind is null then p_kind := 'phone'; end if;
  return query select d.* from public.devices d where d.kind <> 'hidden';
end $$;
`)))
		inv2 := unguardedFor(got, "INV-2")
		if len(inv2) != 1 || !strings.Contains(inv2["sql.public.hide_device"], "kind (") {
			t.Fatalf("INV-2 writers = %v, want hide_device, on kind", inv2)
		}
	})
}

// Each column an UNGUARDED_WRITER links through names where it came from: the
// enforcer and line that read it in a condition, compared it with the caller,
// or wrote it. A column one enforcer compares with the caller and another reads
// otherwise is an ordinary column, and both sources are named.
// @verifies graph:INV-15
func TestUnguardedWriterNamesColumnSources(t *testing.T) {
	const file = "supabase/migrations/20260115000000_inv2.sql"
	t.Run("condition and ownership readers of one column", func(t *testing.T) {
		got, rep := validateWriters(t, writersInv2Repo(t, ownershipWith(`create function public.my_devices() returns setof public.devices
language sql stable security definer set search_path = '' as $$
  select d.* from public.devices d where d.parent_id = auth.uid();
$$;
-- @enforces queue.jobs:INV-2
create function public.same_family(p_a bigint, p_b bigint) returns void
language plpgsql stable security definer set search_path = '' as $$
declare v_dev public.devices;
begin
  select * into v_dev from public.devices d where d.id = p_a;
  if not exists (select 1 from public.devices k where k.id = p_b and k.parent_id = v_dev.parent_id) then
    raise exception 'another family';
  end if;
end $$;
`)))
		msg, ok := unguardedFor(got, "INV-2")["sql.public.register_device"]
		if !ok {
			t.Fatalf("register_device must link through parent_id, an ordinary column: %v", unguardedFor(got, "INV-2"))
		}
		for _, want := range []string{
			"compared with the caller by sql.public.my_devices at " + file + ":",
			"read by sql.public.same_family at " + file + ":",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message lacks %q: %q", want, msg)
			}
		}
		if strings.Contains(msg, "(ownership:") {
			t.Errorf("parent_id is not ownership here: %q", msg)
		}
		if !rep.OK {
			t.Errorf("an info finding must not fail validate")
		}
	})
	t.Run("written by an enforcer with no condition read", func(t *testing.T) {
		got, _ := validateWriters(t, writersInv2Repo(t, ownershipWith(`create function public.retag(p_id bigint) returns void
language sql security definer set search_path = '' as $$
  update public.devices set kind = 'tv' where id = p_id;
$$;
`)))
		msg, ok := unguardedFor(got, "INV-2")["sql.public.add_kind"]
		if !ok {
			t.Fatalf("add_kind writes kind, written by the enforcer: %v", unguardedFor(got, "INV-2"))
		}
		if want := "written by sql.public.retag at " + file + ":"; !strings.Contains(msg, want) {
			t.Errorf("message lacks %q: %q", want, msg)
		}
	})
}

// `lattice writers` lists, under each unguarded invariant, where each shared
// column came from, and --json carries the same as column_sources.
// @verifies graph:INV-15
func TestWritersCommandNamesColumnSources(t *testing.T) {
	repo := writersInv2Repo(t, ownershipWith(`create function public.phones() returns setof public.devices
language sql stable security definer set search_path = '' as $$
  select d.* from public.devices d where d.kind = 'phone';
$$;
`))
	run := func(json bool, args ...string) string {
		var out bytes.Buffer
		io := &IO{Repo: repo, Out: &out, Err: &out, Actor: "test", JSON: json}
		cmd := newWritersCommand(io)
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		cmd.SetArgs(args)
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("writers: %v\n%s", err, out.String())
		}
		return out.String()
	}
	text := run(false, "devices")
	if want := "      kind: read by sql.public.phones at supabase/migrations/20260115000000_inv2.sql:"; !strings.Contains(text, want) {
		t.Errorf("output lacks %q:\n%s", want, text)
	}
	js := run(true, "devices")
	for _, want := range []string{`"column_sources"`, `"enforcer": "sql.public.phones"`, `"how": "condition"`} {
		if !strings.Contains(js, want) {
			t.Errorf("--json lacks %s:\n%s", want, js)
		}
	}
}

// A variable assigned only from an identity helper is an ownership comparison
// also when another declaration with a value comes first in the DECLARE section.
// @verifies adapters.postgres:INV-18
func TestOwnershipVariableAfterAnotherDeclaration(t *testing.T) {
	got, _ := validateWriters(t, writersInv2Repo(t, ownershipWith(`create function public.lock_device(p_device_id bigint) returns void
language plpgsql security definer set search_path = '' as $$
declare
  v_uid uuid := auth.uid();
  v_family uuid := public.current_family();
  v_dev public.devices;
begin
  select * into v_dev from public.devices d where d.id = p_device_id and d.parent_id = v_family for update;
  update public.devices set kind = 'locked' where id = v_dev.id;
end $$;
`)))
	inv2 := unguardedFor(got, "INV-2")
	if msg, ok := inv2["sql.public.register_device"]; ok {
		t.Errorf("register_device only inserts parent_id, an ownership column here, and must not link: %q", msg)
	}
	if msg, ok := inv2["sql.public.transfer_device"]; !ok || !strings.Contains(msg, "v_family, assigned only from public.current_family() → auth.uid()") {
		t.Errorf("transfer_device updates parent_id and links through ownership: %q (%v)", msg, inv2)
	}
}

// A comparison with the caller's identity that counts or tests the existence
// of the caller's rows is about how many there are, and an INSERT changes that:
// it is an ordinary read, so the insert-only writer links. A comparison that
// picks the row the enforcer acts on stays ownership.
// @verifies adapters.postgres:INV-19
func TestCountOfCallersRowsIsNotOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, enforcer string
		insertLinks    bool
	}{
		{"a count loaded into a variable and tested", `create function public.add_device(p_id bigint) returns void
language plpgsql security definer set search_path = '' as $$
declare
  v_uid uuid := auth.uid();
  v_family uuid := public.current_family();
  v_n int;
begin
  select count(*) into v_n from public.devices d where d.parent_id = v_family;
  if v_n >= 3 then raise exception 'at most 3 devices'; end if;
  update public.devices set kind = 'tv' where id = p_id;
end $$;
`, true},
		{"a read-only count", `create function public.device_count() returns bigint
language sql stable security definer set search_path = '' as $$
  select count(*) from public.devices d where d.parent_id = auth.uid();
$$;
`, true},
		{"the row the enforcer acts on", `create function public.retag(p_id bigint) returns void
language plpgsql security definer set search_path = '' as $$
declare
  v_family uuid := public.current_family();
  v_dev public.devices;
begin
  select * into v_dev from public.devices d where d.id = p_id and d.parent_id = v_family for update;
  update public.devices set kind = 'tv' where id = v_dev.id;
end $$;
`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, rep := validateWriters(t, writersInv2Repo(t, ownershipWith(tc.enforcer)))
			inv2 := unguardedFor(got, "INV-2")
			msg, linked := inv2["sql.public.register_device"]
			if linked != tc.insertLinks {
				t.Fatalf("register_device (insert of parent_id) linked = %v, want %v: %v", linked, tc.insertLinks, inv2)
			}
			if linked && strings.Contains(msg, "(ownership:") {
				t.Errorf("a counted column is not ownership: %q", msg)
			}
			if !rep.OK {
				t.Errorf("an info finding must not fail validate")
			}
		})
	}
}

// A guard that tests a value an EXECUTE of a string loaded says so: the value is
// not readable, which is why the enforcer adds no column. It never claims the
// guard tests only the caller's identity.
// @verifies graph:INV-16
// @verifies adapters.postgres:INV-20
func TestGuardOnDynamicSQLSaysItCannotBeRead(t *testing.T) {
	got, rep := validateWriters(t, writersInv2Repo(t, guardedWith(`create function public.enforce_quota() returns trigger
language plpgsql as $$
declare v_count int;
begin
  execute format('select count(*) from public.%I where kind = $1', tg_table_name) into v_count using new.kind;
  if v_count >= 3 then raise exception 'quota'; end if;
  return new;
end $$;
create trigger t_quota before insert on public.devices for each row execute function public.enforce_quota();
`)))
	var note string
	for _, f := range got {
		if f.Code == schema.CodeWritersUnknown && f.InvariantID == "INV-2" {
			note = f.Message
		}
	}
	want := "sql.public.enforce_quota (its guard tests v_count, loaded by EXECUTE of a string at supabase/migrations/20260115000000_inv2.sql:"
	if !strings.Contains(note, want) || !strings.Contains(note, "which Lattice cannot read") {
		t.Errorf("note %q lacks %q", note, want)
	}
	if strings.Contains(note, "caller's identity or a function") {
		t.Errorf("the note claims an identity-only guard: %q", note)
	}
	if !rep.OK {
		t.Errorf("an info finding must not fail validate")
	}
}
