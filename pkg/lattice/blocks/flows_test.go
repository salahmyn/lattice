package blocks

import "testing"

func TestParseWalks(t *testing.T) {
	ws, err := ParseWalks([]byte(`[{"id":"w","title":"W","stop":"end","facts":{"a":"1"},"steps":[{"id":"x","label":"","when":[],"chosen":false}]}]`))
	if err != nil || len(ws) != 1 || ws[0].Steps[0].ID != "x" || ws[0].Facts["a"] != "1" {
		t.Fatalf("walks = %+v, %v", ws, err)
	}
	for _, bad := range []string{`{}`, `[{"steps":[]}]`, `[{"id":"w","steps":[{"label":"x"}]}]`,
		`[{"id":"w","steps":[]},{"id":"w","steps":[]}]`, `[{"id":"w","steps":[],"extra":1}]`} {
		if _, err := ParseWalks([]byte(bad)); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
}

func TestWalkFlows(t *testing.T) {
	in := shopInputs(t)
	in.Sources[0].Walks = []Walk{
		{ID: "buy", Title: "Buy", Stop: "loop", Facts: map[string]string{"k": "v"}, Steps: []WalkStep{
			{ID: "cart.items"},
			{ID: "not.a.node"},
			{ID: "cart.checkout", Label: "checkout", When: []string{"cart.ready"}, Chosen: true},
			{ID: "cart.checkout.retry"},
			{ID: "cart.checkout", Label: "again"},
			{ID: "cart.items"},
		}},
		{ID: "look", Stop: "ask", Steps: []WalkStep{{ID: "cart.items"}}},
	}
	r := Build(in)
	if len(r.Flows) != 2 {
		t.Fatalf("flows = %+v", r.Flows)
	}
	f := r.Flows[0]
	type st struct{ from, to, kind, label string }
	want := []st{
		{"", "fe:cart.items", "start", ""},
		{"fe:cart.items", "fe:cart.checkout", "flow", "checkout"},
		{"fe:cart.checkout", "be:orders.create", "api", "POST /checkout"},
		{"be:orders.create", "fe:cart.checkout", "reply", "response"},
		{"fe:cart.checkout", "fe:cart.checkout.retry", "flow", ""},
		{"fe:cart.checkout.retry", "fe:cart.checkout", "loop", "again"},
	}
	if len(f.Steps) != len(want) {
		t.Fatalf("steps = %+v", f.Steps)
	}
	for i, w := range want {
		s := f.Steps[i]
		if s.From != w.from || s.To != w.to || s.Kind != w.kind || s.Label != w.label {
			t.Errorf("step %d = %+v want %+v", i, s, w)
		}
	}
	if f.End != "loops back" || f.Title != "Buy" || f.Facts["k"] != "v" || !f.Steps[1].Chosen || f.Steps[1].When[0] != "cart.ready" {
		t.Errorf("flow = %+v", f)
	}
	if r.Flows[1].End != "ask" {
		t.Errorf("end = %q", r.Flows[1].End)
	}
}

func TestRequestKey(t *testing.T) {
	if requestKey(&CatHTTP{Method: "post", Path: "/a/{id}/b/"}) != "POST /a/{}/b" {
		t.Error(requestKey(&CatHTTP{Method: "post", Path: "/a/{id}/b/"}))
	}
}
