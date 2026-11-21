package postgres

import (
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema/ir"
)

func httpRefsOf(mod ir.Module) []string {
	var got []string
	add := func(refs []ir.Ref) {
		for _, r := range refs {
			if r.Kind == ir.RefHTTP {
				got = append(got, r.Name+"|"+r.Qualifier)
			}
		}
	}
	for _, s := range mod.Symbols {
		add(s.Refs)
	}
	for _, n := range mod.Nodes {
		add(n.Refs)
	}
	return got
}

// net.http_post, net.http_get and net.http_request record an http ref keyed
// by the URL's literal path; a URL built from a declared setting origin and a
// literal is path-only, and any other computed part is dynamic.
// @verifies reachability:INV-11
func TestNetHTTPRefs(t *testing.T) {
	src := `select cron.schedule('a', '0 3 * * *', $$
  select net.http_post(url := current_setting('app.api_url') || '/functions/v1/a', body := '{}'::jsonb);
  select net.http_get(url => 'https://svc.example.com/api/b?x=1');
  select net.http_post('/functions/v1/c');
  select net.http_post(url := current_setting('app.api_url', true) || '/functions/v1/' || 'd');
  select net.http_post(url := current_setting('app.other') || '/functions/v1/e');
  select net.http_post(url := current_setting('app.api_url') || '/x/' || fn_name || '/y');
  select net.http_post(url := base_url);
  select net.http_request(method := 'PUT', url := current_setting('app.api_url') || '/api/f');
  select net.http_request(method := m, url := '/api/g');
$$);
create function public.ping() returns void language sql as $$
  select net.http_post(url := (current_setting('APP.api_url')::text) || '/functions/v1/h');
  select 'net.http_post(url := ''/in/a/string'')';
$$;
`
	want := []string{
		"POST /functions/v1/h|", // the function's, listed before the cron node's
		"POST /functions/v1/a|",
		"GET /api/b|https://svc.example.com",
		"POST /functions/v1/c|",
		"POST /functions/v1/d|",
		"|", // a setting nothing declares
		"|", // a computed part inside the path
		"|", // a computed URL
		"PUT /api/f|",
		"|", // a computed method
	}
	a := New(nil, nil)
	a.SetHTTPOrigins([]string{"https://app.example.com", "setting:app.api_url", "env:IGNORED"})
	got := httpRefsOf(parse(t, a, "db/jobs.sql", src))
	if len(got) != len(want) {
		t.Fatalf("refs %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ref %d = %q, want %q", i, got[i], want[i])
		}
	}
	// Without a declared origin, the same calls keep a literal URL and read
	// every setting-built one as computed.
	for i, r := range httpRefsOf(parse(t, New(nil, nil), "db/jobs.sql", src)) {
		if (i == 0 || i == 1) && r != "|" {
			t.Errorf("undeclared setting: ref %d = %q, want computed", i, r)
		}
	}
}
