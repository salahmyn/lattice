package reach_test

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/postgres"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/typescript"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

const sqlSchema = `create or replace function public.debit(account_id text, amount numeric(12, 2))
returns void language plpgsql as $$
begin
  perform public.audit('debit');
  insert into ledger(account, amount) values (account_id, amount);
end;
$$;

create function public.audit(what text) returns void language sql as $$
  select log_line(what);
$$;

create function log_line(what text) returns void language sql as $$
  select 1;
$$;

create function public.is_owner(acct text) returns boolean language sql as $$
  select true;
$$;

create function public.touch() returns trigger language plpgsql as $$
begin
  new.updated_at := now();
  return new;
end;
$$;

create function public.nightly() returns void language sql as $$ select 1; $$;

create function public.debit_v1(account_id text) returns void language sql as $$ select 1; $$;

create policy "owner reads" on public.accounts for select using (public.is_owner(id));

create trigger accounts_touch before update on public.accounts
  for each row execute function public.touch();

select cron.schedule('nightly-job', '0 3 * * *', $$ select public.nightly() $$);
`

// Each SQL edge kind: .rpc('name') from app code, .from('table') to the
// table's policies and triggers, a trigger to its function, a function
// body's calls (perform, select, a bare call; a column list after insert
// into is no call), a policy expression's calls and a cron.schedule body.
// A non-literal .rpc(x) is unresolved, never matched to a function.
// @verifies reachability:INV-1, reachability:INV-2, reachability:INV-3
func TestSQLEdges(t *testing.T) {
	mods := parse(t, postgres.New(nil, nil), map[string]string{"db/schema.sql": sqlSchema})
	mods = append(mods, parse(t, typescript.New(), map[string]string{
		"app/api.ts": `export async function pay(client: any, fn: string) {
  await client.rpc('debit', { account_id: 'a', amount: 1 });
  await client.from('accounts').select('*');
  await client.rpc(fn);
  Array.from('abc');
}
`,
	})...)
	g := reach.Build(mods, reach.Options{})
	for _, e := range [][2]string{
		{"app.api.pay", "sql.public.debit"},                                     // .rpc('debit')
		{"app.api.pay", "sql.table.public.accounts"},                            // .from('accounts')
		{"sql.table.public.accounts", "sql.policy.public.accounts.owner_reads"}, // table -> policy
		{"sql.table.public.accounts", "sql.trigger.public.accounts.accounts_touch"},
		{"sql.trigger.public.accounts.accounts_touch", "sql.public.touch"}, // execute function
		{"sql.public.debit", "sql.public.audit"},                           // perform
		{"sql.public.audit", "sql.public.log_line"},                        // select f(), unqualified
		{"sql.policy.public.accounts.owner_reads", "sql.public.is_owner"},  // using (...)
		{"sql.cron.nightly_job", "sql.public.nightly"},                     // cron.schedule body
	} {
		if !hasEdge(g, e[0], e[1]) {
			t.Errorf("want edge %s -> %s, have%s", e[0], e[1], joined(edges(g)))
		}
	}
	for _, e := range [][2]string{
		{"sql.public.debit", "sql.public.ledger"}, // insert into ledger(...)
		{"app.api.pay", "sql.table.public.abc"},   // Array.from builds an array
	} {
		if hasEdge(g, e[0], e[1]) {
			t.Errorf("unexpected edge %s -> %s", e[0], e[1])
		}
	}
	if !contains(unresolved(g), "app.api.pay  dynamic") {
		t.Errorf("a computed .rpc(fn) must be unresolved (dynamic), have%s", joined(unresolved(g)))
	}

	roots := g.Roots(reach.RootInput{})
	var names []string
	for _, r := range roots {
		names = append(names, r.FQN+" "+r.Reason)
	}
	for _, want := range []string{
		"sql.policy.public.accounts.owner_reads policy",
		"sql.trigger.public.accounts.accounts_touch trigger",
		"sql.cron.nightly_job cron",
	} {
		if !contains(names, want) {
			t.Errorf("want root %q, have%s", want, joined(names))
		}
	}
	r := reach.Classify(g, roots)
	for fqn, want := range map[string]string{
		"sql.public.touch":    schema.ReachEntry,     // the trigger runs it
		"sql.public.is_owner": schema.ReachEntry,     // the policy runs it
		"sql.public.nightly":  schema.ReachEntry,     // the cron job runs it
		"sql.public.debit_v1": schema.ReachUnreached, // nothing calls it
		"sql.public.debit":    schema.ReachUnreached, // only app code no root reaches
	} {
		if got := r.Class[fqn]; got != want {
			t.Errorf("%s: class %q, want %q", fqn, got, want)
		}
	}
}

// A job rescheduled in a later migration keeps only its last body, and
// cron.unschedule removes it.
// @verifies reachability:INV-3
func TestSQLCronAcrossMigrations(t *testing.T) {
	mods := parse(t, postgres.New(nil, nil), map[string]string{
		"db/migrations/001.sql": "create function public.a() returns void language sql as $$ select 1 $$;\n" +
			"create function public.b() returns void language sql as $$ select 1 $$;\n" +
			"select cron.schedule('job', '* * * * *', 'select public.a()');\n" +
			"select cron.schedule('gone', '* * * * *', 'select public.a()');\n",
		"db/migrations/002.sql": "select cron.schedule('job', '* * * * *', 'select public.b()');\n" +
			"select cron.unschedule('gone');\n",
	})
	g := reach.Build(mods, reach.Options{})
	if hasEdge(g, "sql.cron.job", "sql.public.a") || !hasEdge(g, "sql.cron.job", "sql.public.b") {
		t.Errorf("the later schedule wins:%s", joined(edges(g)))
	}
	for _, r := range g.Roots(reach.RootInput{}) {
		if r.FQN == "sql.cron.gone" {
			t.Error("an unscheduled job is no root")
		}
	}
}
