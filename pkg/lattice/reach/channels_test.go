package reach_test

import (
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/postgres"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/typescript"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

// The SQL half of an email pipeline: a function that enqueues a job, and a
// worker that reads the queue and calls the function that keeps a promise.
const emailsSQL = `create function public.enqueue_email(addr text) returns void language plpgsql as $$
begin
  perform pgmq.send('emails', jsonb_build_object('to', addr));
end;
$$;

create function public.work_emails() returns void language plpgsql as $$
declare m record;
begin
  select * into m from pgmq.read('emails', 30, 1);
  perform public.charge_fee();
end;
$$;

create function public.charge_fee() returns void language sql as $$ select 1; $$;
`

const emailsAPI = `import express from 'express';
const app = express();
function signup(req, res) { return supabase.rpc('enqueue_email', { addr: req.body.addr }); }
app.post('/api/signup', signup);
`

// channelGraph builds the graph and the classified result of the SQL emails
// pipeline, with the signup route as the only root.
func channelGraph(t *testing.T, sql string, ts map[string]string, eps ...schema.EntryPoint) (*reach.Graph, *reach.Result) {
	t.Helper()
	mods := parse(t, postgres.New(nil, nil), map[string]string{"db/emails.sql": sql})
	if len(ts) > 0 {
		mods = append(mods, parse(t, typescript.New(), ts)...)
	}
	g := reach.Build(mods, reach.Options{EntryPoints: eps})
	roots := g.Roots(reach.RootInput{EntryFiles: []reach.EntryFile{{File: "api/server.ts", Reason: "the api"}}})
	return g, reach.Classify(g, roots)
}

func chanCodes(vs []schema.Violation) []string {
	var out []string
	for _, v := range vs {
		out = append(out, v.Code+" "+v.Subject)
	}
	return out
}

// A publisher and a consumer that name the same literal queue are joined
// through a channel node: publish into it, consume out of it. Synchronous
// reach does not cross it: the worker and what it calls are not entry, and
// their count of channels crossed is reported apart.
// @verifies reachability:INV-13
func TestChannelJoinsPublisherToConsumer(t *testing.T) {
	g, r := channelGraph(t, emailsSQL, map[string]string{"api/server.ts": emailsAPI})
	if !hasEdge(g, "sql.public.enqueue_email", "chan.queue.emails") || !hasEdge(g, "chan.queue.emails", "sql.public.work_emails") {
		t.Fatalf("channel edges missing:%s", joined(edges(g, "publish", "consume")))
	}
	got := edges(g, "publish", "consume")
	want := []string{"chan.queue.emails -> sql.public.work_emails", "sql.public.enqueue_email -> chan.queue.emails"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("channel edges:%s\nwant:%s", joined(got), joined(want))
	}
	for _, e := range g.Edges {
		if e.Kind == "publish" && e.Via != "channel" || e.Kind == "consume" && e.Via != "channel" {
			t.Errorf("edge %+v: via should be channel", e)
		}
	}
	chans := g.Channels()
	if len(chans) != 1 || chans[0].FQN != "chan.queue.emails" || len(chans[0].Publishers) != 1 || len(chans[0].Consumers) != 1 {
		t.Fatalf("channels = %+v", chans)
	}
	if n, ok := g.NodeInfo("chan.queue.emails"); !ok || n.Kind != "channel" || n.Symbol {
		t.Errorf("channel node = %+v, %v", n, ok)
	}
	// The synchronous class is untouched by the channel.
	for fqn, wantHops := range map[string]int{"sql.public.work_emails": 1, "sql.public.charge_fee": 1} {
		if c := r.Class[fqn]; c == schema.ReachEntry {
			t.Errorf("%s: class entry; a channel must not make a symbol live", fqn)
		}
		if r.AsyncHops[fqn] != wantHops {
			t.Errorf("%s: async hops %d, want %d", fqn, r.AsyncHops[fqn], wantHops)
		}
	}
	if r.Class["sql.public.enqueue_email"] != schema.ReachEntry {
		t.Errorf("the publisher is reached through rpc: %q", r.Class["sql.public.enqueue_email"])
	}
	if _, ok := r.AsyncHops["sql.public.enqueue_email"]; ok {
		t.Error("a symbol with a synchronous path has no async hops")
	}
	if vs := reach.ChannelFindings(r); len(vs) != 0 {
		t.Errorf("a joined channel raises nothing: %v", chanCodes(vs))
	}
}

// Async reach is written onto the symbols apart from the class, and Reach
// stays what synchronous reach says, so the audit's live signal does not move.
// @verifies reachability:INV-13
func TestAsyncReachLeavesTheClassAlone(t *testing.T) {
	_, r := channelGraph(t, emailsSQL, map[string]string{"api/server.ts": emailsAPI})
	kg := schema.KnowledgeGraph{Symbols: []schema.GraphSymbol{
		{FQN: "sql.public.work_emails"}, {FQN: "sql.public.charge_fee"}, {FQN: "sql.public.enqueue_email"}}}
	reach.Annotate(&kg, r)
	for _, s := range kg.Symbols {
		switch s.FQN {
		case "sql.public.enqueue_email":
			if s.Reach != schema.ReachEntry || s.AsyncHops != 0 {
				t.Errorf("%s: reach %q async %d", s.FQN, s.Reach, s.AsyncHops)
			}
		default:
			if s.Reach != schema.ReachUnreached || s.AsyncHops != 1 {
				t.Errorf("%s: reach %q async %d, want unreached and 1", s.FQN, s.Reach, s.AsyncHops)
			}
		}
	}
	// The unreached finding names the channel so the cause is not hidden.
	var found bool
	for _, v := range reach.Findings(kg, r, nil) {
		if v.Code == schema.CodeSymbolUnreached && v.Subject == "sql.public.charge_fee" {
			found = strings.Contains(v.Message, "1 channel(s)")
		}
	}
	if !found {
		t.Error("SYMBOL_UNREACHED for an async-only symbol should say it is reached through a channel")
	}
}

// Renaming the queue at one end gives no edge across it, and both ends are
// named: a publish nothing consumes, a consumer nothing publishes to.
// @verifies reachability:INV-13
func TestChannelRenamedAtOneEnd(t *testing.T) {
	renamed := strings.Replace(emailsSQL, "pgmq.read('emails'", "pgmq.read('mails'", 1)
	g, r := channelGraph(t, renamed, map[string]string{"api/server.ts": emailsAPI})
	if hasEdge(g, "chan.queue.emails", "sql.public.work_emails") || hasEdge(g, "chan.queue.mails", "sql.public.enqueue_email") {
		t.Errorf("a renamed queue must join nothing:%s", joined(edges(g, "publish", "consume")))
	}
	if r.AsyncHops["sql.public.work_emails"] != 0 {
		t.Errorf("no async reach without a join: %v", r.AsyncHops)
	}
	got := chanCodes(reach.ChannelFindings(r))
	want := []string{"CHANNEL_NO_CONSUMER chan.queue.emails", "CHANNEL_NO_PUBLISHER chan.queue.mails"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("findings:%s\nwant:%s", joined(got), joined(want))
	}
	for _, v := range reach.ChannelFindings(r) {
		if v.Severity != schema.SeverityInfo {
			t.Errorf("%s is %s, want info", v.Code, v.Severity)
		}
	}
}

// A queue name that is computed makes no edge and no channel: the reference
// is unresolved (dynamic) and one CHANNEL_DYNAMIC per file counts it.
// @verifies reachability:INV-13
func TestChannelComputedNameIsDynamic(t *testing.T) {
	sql := `create function public.enqueue_any(q text) returns void language plpgsql as $$
begin
  perform pgmq.send(q, '{}'::jsonb);
  perform pgmq.send('prefix_' || q, '{}'::jsonb);
  perform pg_notify(format('ch_%s', q), '');
end;
$$;
create function public.work() returns void language plpgsql as $$
begin
  perform pgmq.read(some_setting(), 30, 1);
end;
$$;
`
	g, r := channelGraph(t, sql, nil)
	if got := edges(g, "publish", "consume"); len(got) != 0 {
		t.Errorf("a computed name makes no edge:%s", joined(got))
	}
	if len(g.Channels()) != 0 {
		t.Errorf("a computed name makes no channel: %+v", g.Channels())
	}
	var dyn int
	for _, u := range g.Unresolved {
		if u.Kind == ir.RefPublish || u.Kind == ir.RefConsume {
			if u.Why != "dynamic" {
				t.Errorf("%+v: why %q, want dynamic", u, u.Why)
			}
			dyn++
		}
	}
	if dyn != 4 {
		t.Errorf("%d dynamic channel ends, want 4: %v", dyn, unresolved(g))
	}
	got := chanCodes(reach.ChannelFindings(r))
	if len(got) != 1 || got[0] != "CHANNEL_DYNAMIC db/emails.sql" {
		t.Errorf("findings: %v, want one CHANNEL_DYNAMIC for the file", got)
	}
	// It is not an RPC finding either.
	for _, v := range reach.Findings(schema.KnowledgeGraph{}, r, nil) {
		if v.Code == schema.CodeRPCUnresolved {
			t.Errorf("a computed channel name is not an RPC: %+v", v)
		}
	}
}

// pg_notify and NOTIFY publish to a notify channel; LISTEN names a channel but
// no handler, so it is no consumer. A call quoted in a string or a comment, and
// a column called notify, are no ends.
// @verifies reachability:INV-13
func TestNotifyEnds(t *testing.T) {
	sql := `create function public.a() returns void language plpgsql as $$
begin
  perform pg_notify('orders_changed', '');
  NOTIFY Audit_Log, 'x';
  notify "Mixed Case";
  -- perform pg_notify('in_a_comment', '');
  raise notice 'call pg_notify(''in_a_string'')';
end;
$$;
create function public.b() returns void language plpgsql as $$
begin
  listen orders_changed;
end;
$$;
create table public.prefs (id int, notify boolean);
`
	g, r := channelGraph(t, sql, nil)
	var names []string
	for _, c := range g.Channels() {
		names = append(names, c.FQN)
		if len(c.Consumers) != 0 {
			t.Errorf("%s: LISTEN is no consumer", c.FQN)
		}
	}
	want := []string{"chan.notify.Mixed Case", "chan.notify.audit_log", "chan.notify.orders_changed"}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Errorf("channels:%s\nwant:%s", joined(names), joined(want))
	}
	if got := chanCodes(reach.ChannelFindings(r)); len(got) != 3 {
		t.Errorf("every notify channel has no consumer: %v", got)
	}
}

// BullMQ: new Queue('name') bound in the module, queue.add(...) and
// new Queue('name').add(...) publish; new Worker('name', handler) consumes,
// the named handler when there is one and the module's top otherwise. A queue
// held anywhere the module's own bindings do not show (a parameter, a
// property) is not followed.
// @verifies reachability:INV-13
func TestBullMQEnds(t *testing.T) {
	files := map[string]string{
		"jobs/producer.ts": `import { Queue } from 'bullmq';
const emails = new Queue('emails', { connection });
const reports = new Queue(process.env.REPORTS_QUEUE);
export function sendWelcome(to: string) { return emails.add('welcome', { to }); }
export function sendNow() { return new Queue('urgent').add('x', {}); }
export function nightly() { return reports.add('r', {}); }
export function viaParam(q: Queue) { return q.add('p', {}); }
`,
		"jobs/worker.ts": `import { Worker } from 'bullmq';
import { handleEmail } from './handlers';
function handleUrgent(job) {}
new Worker('emails', handleEmail, { connection });
new Worker('urgent', handleUrgent);
new Worker('inline', async (job) => { await audit(job); });
`,
		"jobs/handlers.ts": "export function handleEmail(job) {}\n",
		"jobs/other.ts":    "import { Queue } from './mine';\nconst q = new Queue('mine');\nexport function f() { return q.add('x'); }\n",
	}
	mods := parse(t, typescript.New(), files)
	g := reach.Build(mods, reach.Options{})
	got := edges(g, "publish", "consume")
	want := []string{
		"chan.queue.emails -> jobs.handlers.handleEmail",
		"chan.queue.inline -> jobs.worker.<top>",
		"chan.queue.urgent -> jobs.worker.handleUrgent",
		"jobs.producer.sendNow -> chan.queue.urgent",
		"jobs.producer.sendWelcome -> chan.queue.emails",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("channel edges:%s\nwant:%s", joined(got), joined(want))
	}
	dyn := map[string]bool{}
	for _, u := range g.Unresolved {
		if u.Kind == ir.RefPublish && u.Why == "dynamic" {
			dyn[u.From] = true
		}
	}
	if !dyn["jobs.producer.nightly"] || len(dyn) != 1 {
		t.Errorf("only the computed queue is dynamic: %v", dyn)
	}
}

// supabase.rpc('pgmq_send', { queue_name: 'q' }) publishes to a literal queue;
// a computed queue_name, or none, is dynamic. pgmq_read and pgmq_pop consume.
// @verifies reachability:INV-13
func TestPgmqRPCFromTypeScript(t *testing.T) {
	mods := parse(t, typescript.New(), map[string]string{
		"fn/index.ts": `export function a() { return supabase.rpc('pgmq_send', { queue_name: 'emails', msg: {} }); }
export function b(q: string) { return supabase.rpc('pgmq_send', { queue_name: q, msg: {} }); }
export function c() { return supabase.schema('pgmq_public').rpc('send', { queue_name: 'emails', message: {} }); }
export function d() { return supabase.rpc('pgmq_pop', { queue_name: 'emails' }); }
export function e() { return supabase.rpc('send', { queue_name: 'emails' }); }
`,
	})
	g := reach.Build(mods, reach.Options{})
	got := edges(g, "publish", "consume")
	want := []string{"chan.queue.emails -> fn.index.d", "fn.index.a -> chan.queue.emails", "fn.index.c -> chan.queue.emails"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("channel edges:%s\nwant:%s", joined(got), joined(want))
	}
	if u := unresolved(g); !contains(u, "fn.index.b  dynamic") {
		t.Errorf("b is dynamic:%s", joined(u))
	}
}

// A queue or event_consumer entry point whose trigger names a channel is that
// channel's consumer, and its handler the consumer node.
// @verifies reachability:INV-13
func TestEntryPointConsumers(t *testing.T) {
	eps := []schema.EntryPoint{
		{ID: "ep.queue.emails", Kind: schema.EntryPointKindQueue, Trigger: schema.Trigger{Queue: "emails"}, Handler: schema.Handler{Symbol: "sql.public.work_emails"}},
		{ID: "ep.event.paid", Kind: schema.EntryPointKindEventConsumer, Trigger: schema.Trigger{Event: "order.paid"}, Handler: schema.Handler{Symbol: "sql.public.charge_fee"}},
		{ID: "ep.queue.none", Kind: schema.EntryPointKindQueue, Handler: schema.Handler{Symbol: "sql.public.charge_fee"}},
		{ID: "ep.queue.ghost", Kind: schema.EntryPointKindQueue, Trigger: schema.Trigger{Queue: "ghost"}, Handler: schema.Handler{Symbol: "sql.public.missing"}},
	}
	sql := strings.Replace(emailsSQL, "pgmq.read('emails', 30, 1)", "now()", 1)
	g, r := channelGraph(t, sql, map[string]string{"api/server.ts": emailsAPI}, eps...)
	got := edges(g, "consume")
	want := []string{"chan.event.order.paid -> sql.public.charge_fee", "chan.queue.emails -> sql.public.work_emails"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("consume edges:%s\nwant:%s", joined(got), joined(want))
	}
	// The publisher joins the entry point's channel, and the event has no
	// publisher.
	if !hasEdge(g, "sql.public.enqueue_email", "chan.queue.emails") {
		t.Error("the publisher should reach the entry point's channel")
	}
	got = chanCodes(reach.ChannelFindings(r))
	if len(got) != 1 || got[0] != "CHANNEL_NO_PUBLISHER chan.event.order.paid" {
		t.Errorf("findings: %v", got)
	}
}

// A declared wrapper is an end like a built-in one: the call is read by its
// trailing segments, the channel name must be a literal at the declared
// argument, and a consume wrapper's handler argument names the consumer.
// @verifies reachability:INV-13
func TestDeclaredWrappers(t *testing.T) {
	a := typescript.New()
	a.SetChannelWrappers([]ir.ChannelWrapper{
		{Call: "jobs.enqueue", Arg: 0, Kind: ir.ChannelQueue, HandlerArg: -1},
		{Call: "jobs.register", Arg: 0, Kind: ir.ChannelQueue, Consume: true, HandlerArg: 1},
		{Call: "bus.emit", Arg: 1, Kind: ir.ChannelEvent, HandlerArg: -1},
	})
	mods := parse(t, a, map[string]string{
		"app/pub.ts": `import * as jobs from './jobs';
export function a() { return jobs.enqueue('emails', {}); }
export function b(n: string) { return jobs.enqueue(n, {}); }
export function c() { return app.jobs.enqueue(` + "`emails`" + `); }
export function d() { return other.enqueue('emails'); }
export function e() { return bus.emit({}, 'order.paid'); }
export function f() { return bus.emit({}); }
`,
		"app/sub.ts": `import * as jobs from './jobs';
function onEmail(job) {}
jobs.register('emails', onEmail);
jobs.register('orders', (job) => {});
`,
	})
	g := reach.Build(mods, reach.Options{})
	got := edges(g, "publish", "consume")
	want := []string{
		"app.pub.a -> chan.queue.emails",
		"app.pub.c -> chan.queue.emails",
		"app.pub.e -> chan.event.order.paid",
		"chan.queue.emails -> app.sub.onEmail",
		"chan.queue.orders -> app.sub.<top>",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("channel edges:%s\nwant:%s", joined(got), joined(want))
	}
	u := unresolved(g)
	for _, from := range []string{"app.pub.b", "app.pub.f"} {
		if !contains(u, from+"  dynamic") {
			t.Errorf("%s should be dynamic:%s", from, joined(u))
		}
	}
	// Without the declaration the same code makes no channel.
	bare := parse(t, typescript.New(), map[string]string{"app/pub.ts": "export function a() { return jobs.enqueue('emails', {}); }\n"})
	if len(reach.Build(bare, reach.Options{}).Channels()) != 0 {
		t.Error("an undeclared wrapper must not make a channel")
	}
}

// A named handler that is no node of the workspace is unresolved (missing),
// never replaced by the module's top.
// @verifies reachability:INV-13
func TestConsumeHandlerMissing(t *testing.T) {
	a := typescript.New()
	a.SetChannelWrappers([]ir.ChannelWrapper{{Call: "jobs.register", Arg: 0, Kind: ir.ChannelQueue, Consume: true, HandlerArg: 1}})
	mods := parse(t, a, map[string]string{"app/sub.ts": "import { handler } from 'elsewhere';\njobs.register('emails', handler);\n"})
	g := reach.Build(mods, reach.Options{})
	if got := edges(g, "consume"); len(got) != 0 {
		t.Errorf("an unresolvable handler makes no edge:%s", joined(got))
	}
	if u := unresolved(g); !contains(u, "app.sub.<top> queue:emails missing") {
		t.Errorf("unresolved:%s", joined(u))
	}
}
