package slice

import (
	"reflect"
	"strings"
	"testing"
)

// edgeFixture is a root that calls a client, which sends a request to a
// handler (an http hop), and a second client whose request is ambiguous.
func edgeFixture() (*Graph, Record) {
	g := (&fixture{layers: map[string]string{"app.client": "ui", "api.handler": "api", "app.other": "ui", "api.a": "api", "api.b": "api"}}).
		root("app.main", "the page").
		edge("app.main", "call", "app.client").
		edge("app.client", "http", "api.handler").
		edge("app.main", "call", "app.other").
		ambiguous("app.other", "http", "api.a", "api.b").
		graph()
	return g, Compute(g, "f:INV-1", enf("api.handler", "api.a"))
}

func hopOf(rec Record, from, to string) *Hop {
	for pi := range rec.Paths {
		for hi := range rec.Paths[pi].Hops {
			if h := &rec.Paths[pi].Hops[hi]; h.From == from && h.To == to {
				return h
			}
		}
	}
	return nil
}

// A hop is break-tested only when an edge break of it was killed, by the
// verifiers that failed. A survived or unknown run, a hop with no run, and an
// unknown hop are never marked, and without any edge run the record is left
// as it was.
// @verifies breaktest:INV-19
func TestMarkBreakTested(t *testing.T) {
	_, rec := edgeFixture()
	MarkBreakTested(&rec, nil)
	for _, p := range rec.Paths {
		if p.BreakTestedHops != nil {
			t.Error("break_tested_hops is set with no edge run")
		}
	}

	MarkBreakTested(&rec, []EdgeRun{
		{From: "app.client", To: "api.handler", Kind: "http", Killed: true, By: []string{"v.b", "v.a"}},
		{From: "app.other", To: "api.a", Kind: "http", Killed: true, By: []string{"v.c"}}, // an unknown hop: never marked
		{From: "app.main", To: "app.client", Kind: "call", Killed: false, By: []string{"v.d"}},
	})
	if h := hopOf(rec, "app.client", "api.handler"); h == nil || !h.BreakTested || !reflect.DeepEqual(h.BreakTestedBy, []string{"v.a", "v.b"}) {
		t.Errorf("killed hop = %+v", h)
	}
	if h := hopOf(rec, "app.other", "api.a"); h == nil || h.Truth != TruthUnknown || h.BreakTested || h.BreakTestedBy != nil {
		t.Errorf("unknown hop = %+v", h)
	}
	if h := hopOf(rec, "app.main", "app.client"); h == nil || h.BreakTested {
		t.Errorf("a hop whose run did not kill = %+v", h)
	}
	for _, p := range rec.Paths {
		if p.BreakTestedHops == nil {
			t.Fatalf("break_tested_hops missing on %+v", p)
		}
		if p.Enforcer == "api.handler" && *p.BreakTestedHops != 1 {
			t.Errorf("break_tested_hops = %d, want 1", *p.BreakTestedHops)
		}
	}
}

// A break-tested hop reads kind* in the text, with a legend; the count is on
// the path.
// @verifies breaktest:INV-19
func TestBreakTestedText(t *testing.T) {
	_, rec := edgeFixture()
	MarkBreakTested(&rec, []EdgeRun{{From: "app.client", To: "api.handler", Kind: "http", Killed: true, By: []string{"v.a"}}})
	var b strings.Builder
	WriteText(&b, rec)
	for _, want := range []string{"-http*-> api.handler", "break-tested 1 of 2", "kind* marks a break-tested hop"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("text lacks %q:\n%s", want, b.String())
		}
	}
}
