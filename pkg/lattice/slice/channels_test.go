package slice

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/adapters/postgres"
	"github.com/salahmyn/lattice/pkg/lattice/adapters/typescript"
	"github.com/salahmyn/lattice/pkg/lattice/config"
	"github.com/salahmyn/lattice/pkg/lattice/coverage"
	"github.com/salahmyn/lattice/pkg/lattice/reach"
	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

const (
	chanAPI = `import express from 'express';
const app = express();
function signup(req, res) { return supabase.rpc('enqueue_email', { addr: req.body.addr }); }
app.post('/api/signup', signup);
`
	chanSQL = `create function public.enqueue_email(addr text) returns void language plpgsql as $$
begin
  perform pgmq.send('emails', jsonb_build_object('to', addr));
end;
$$;
create function public.work_emails() returns void language plpgsql as $$
begin
  perform pgmq.read('emails', 30, 1);
  perform public.charge_fee();
end;
$$;
create function public.charge_fee() returns void language sql as $$ select 1; $$;
`
)

// channelSlice slices an API route that enqueues a job through a SQL function
// and a SQL worker that reads the queue, with layers by path.
func channelSlice(t *testing.T, sql string, api string) (*Graph, schema.KnowledgeGraph) {
	t.Helper()
	mods := parseFiles(t, typescript.New(), map[string]string{"api/server.ts": api})
	mods = append(mods, parseFiles(t, postgres.New(nil, nil), map[string]string{"db/emails.sql": sql})...)
	rules := []schema.LayerRule{{Glob: "api/**", Layer: schema.LayerAPI}, {Glob: "db/**", Layer: schema.LayerData}}
	var kg schema.KnowledgeGraph
	for _, m := range mods {
		for _, s := range m.Symbols {
			kg.Symbols = append(kg.Symbols, schema.GraphSymbol{FQN: s.FQN, File: s.File, Layer: schema.LayerOfFile(rules, s.File)})
		}
	}
	rr := reach.Compute(reach.Input{Modules: mods,
		Declared: []config.CoverageRoot{{Symbol: "api.server.signup", Reason: "the route"}}})
	return NewGraph(rr, kg, rules), kg
}

func kinds(hops []Hop) []string {
	var out []string
	for _, h := range hops {
		out = append(out, h.Kind)
	}
	return out
}

// An HTTP handler that enqueues through a SQL function, and a function that
// reads the queue and calls the enforcer: the slice crosses the channel with a
// publish hop and a consume hop, both resolved, counts one async hop, and
// carries the promise through api, data, the async channel and data again.
// @verifies reachability:INV-13
func TestSliceThroughAChannel(t *testing.T) {
	g, _ := channelSlice(t, chanSQL, chanAPI)
	rec := Compute(g, "mail:INV-1", enf("sql.public.charge_fee"))
	if rec.Reading != ReadingComplete {
		t.Fatalf("reading %q: %s", rec.Reading, rec.Why)
	}
	var p *Path
	for i := range rec.Paths {
		if rec.Paths[i].Root.FQN == "api.server.signup" {
			p = &rec.Paths[i]
		}
	}
	if p == nil {
		t.Fatalf("no path from the route: %+v", rec.Paths)
	}
	if got, want := kinds(p.Hops), []string{"rpc", "publish", "consume", "call"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("hop kinds %v, want %v: %+v", got, want, p.Hops)
	}
	for _, h := range p.Hops {
		if h.Truth != TruthResolved {
			t.Errorf("hop %+v is not resolved", h)
		}
	}
	if p.Hops[1].To != "chan.queue.emails" || p.Hops[1].ToLayer != "async" || p.Hops[2].From != "chan.queue.emails" || p.Hops[2].To != "sql.public.work_emails" {
		t.Errorf("channel hops = %+v %+v", p.Hops[1], p.Hops[2])
	}
	if p.AsyncHops != 1 {
		t.Errorf("async hops = %d, want 1", p.AsyncHops)
	}
	if want := []string{"api", "data", "async", "data"}; !reflect.DeepEqual(p.CarriedBy, want) {
		t.Errorf("carried by %v, want %v", p.CarriedBy, want)
	}
	var out bytes.Buffer
	WriteText(&out, rec)
	if !strings.Contains(out.String(), "-publish-> chan.queue.emails [async] -consume-> sql.public.work_emails [data]") ||
		!strings.Contains(out.String(), "1 async hop(s)") {
		t.Errorf("text shows no channel crossing:\n%s", out.String())
	}
	raw, _ := json.Marshal(rec.Paths[0])
	if !strings.Contains(string(raw), `"async_hops":1`) {
		t.Errorf("JSON lacks async_hops: %s", raw)
	}
}

// A path that crosses no channel is preferred to a shorter one that does, so
// a slice reports async hops only when the promise is reached no other way.
// @verifies reachability:INV-13
func TestSynchronousPathBeatsAChannel(t *testing.T) {
	sql := `create function public.enqueue_fee() returns void language plpgsql as $$
begin
  perform pgmq.send('fees', '{}'::jsonb);
end;
$$;
create function public.charge_fee() returns void language plpgsql as $$
begin
  perform pgmq.pop('fees');
end;
$$;
create function public.h1() returns void language sql as $$ select public.h2(); $$;
create function public.h2() returns void language sql as $$ select public.h3(); $$;
create function public.h3() returns void language sql as $$ select public.charge_fee(); $$;
`
	api := `import express from 'express';
const app = express();
function signup(req, res) { supabase.rpc('enqueue_fee'); return supabase.rpc('h1'); }
app.post('/api/signup', signup);
`
	g, _ := channelSlice(t, sql, api)
	rec := Compute(g, "mail:INV-1", enf("sql.public.charge_fee"))
	if len(rec.Paths) != 1 {
		t.Fatalf("paths = %+v", rec.Paths)
	}
	p := rec.Paths[0]
	if p.AsyncHops != 0 || len(p.Hops) != 4 || strings.Contains(strings.Join(kinds(p.Hops), " "), "consume") {
		t.Errorf("want the 4-hop synchronous path (the channel path has 3), got %+v", p)
	}
}

// A publisher with no consumer reaches only the channel node: an enforcer
// behind a queue nobody reads is not reached.
// @verifies reachability:INV-13
func TestSliceNeedsBothEnds(t *testing.T) {
	renamed := strings.Replace(chanSQL, "pgmq.read('emails'", "pgmq.read('mails'", 1)
	g, _ := channelSlice(t, renamed, chanAPI)
	rec := Compute(g, "mail:INV-1", enf("sql.public.charge_fee"))
	if rec.Reading != ReadingUnreached || !reflect.DeepEqual(rec.Unreached, []string{"sql.public.charge_fee"}) {
		t.Errorf("reading %q unreached %v: %s", rec.Reading, rec.Unreached, rec.Why)
	}
}

// The exercised rule needs no change for a channel: the channel is no symbol,
// so each side of a hop is stood for by the nearest symbol on that side, and a
// hop across it is exercised when one verifier ran the publisher and the
// consumer.
// @verifies reachability:INV-12, reachability:INV-13
func TestHopsAcrossAChannelCanBeExercised(t *testing.T) {
	g, kg := channelSlice(t, chanSQL, chanAPI)
	both := []string{"sql.public.enqueue_email", "sql.public.work_emails", "sql.public.charge_fee"}
	ix := coverage.NewIndex(coverage.Set{PerTest: map[string][]string{"mail.both": both, "mail.one": {"sql.public.enqueue_email"}}}, kg)
	g = g.WithCoverage(ix)
	rec := Compute(g, "mail:INV-1", enf("sql.public.charge_fee"))
	Exercise(&rec, g, []string{"mail.both", "mail.one"})
	p := rec.Paths[0]
	if len(p.Hops) != 4 {
		t.Fatalf("hops = %+v", p.Hops)
	}
	for _, i := range []int{1, 2, 3} { // publish, consume, call
		if h := p.Hops[i]; !h.Exercised || !reflect.DeepEqual(h.ExercisedBy, []string{"mail.both"}) {
			t.Errorf("hop %d (%s) exercised %v by %v, want by mail.both", i, h.Kind, h.Exercised, h.ExercisedBy)
		}
	}
	if p.Hops[0].Exercised { // the route handler ran in no test
		t.Errorf("rpc hop exercised by %v", p.Hops[0].ExercisedBy)
	}
}
