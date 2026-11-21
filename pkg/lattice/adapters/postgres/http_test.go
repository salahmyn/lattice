package postgres

import (
	"strings"
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

// A URL read whole from Vault takes its key from the route a person declared
// for the secret (http.secrets): a variable set by select ... into, by :=
// (declared or not, with a cast), or the read itself as a subquery, alone or
// followed by literals. The name may sit anywhere in the WHERE clause.
// @verifies reachability:INV-11
func TestVaultSecretURLs(t *testing.T) {
	src := `create function public.a() returns void language plpgsql as $$
declare v_url text;
begin
  select decrypted_secret into v_url from vault.decrypted_secrets where name = 'a_url';
  perform net.http_post(url := v_url, body := '{}'::jsonb);
end $$;
create function public.b() returns void language plpgsql as $$
declare v_url text := (select decrypted_secret from vault.decrypted_secrets where name = 'b_url');
begin
  perform net.http_post(url := v_url::text || '/run', body := '{}'::jsonb);
end $$;
create function public.c() returns void language plpgsql as $$
declare u text;
begin
  u := (select ds.decrypted_secret from vault.decrypted_secrets ds where ds.updated_at is not null and ds.name = 'a_url' limit 1);
  perform net.http_post(url := u);
  perform net.http_get(url := u || '/status');
end $$;
create function public.d() returns void language sql as $$
  select net.http_post(url := (select decrypted_secret from vault.decrypted_secrets where name = 'a_url'));
$$;
`
	want := []string{
		"POST /functions/v1/a|",
		"POST /functions/v1/b/run|",
		"POST /functions/v1/a|",
		"GET /functions/v1/a/status|",
		"POST /functions/v1/a|",
	}
	a := New(nil, nil)
	a.SetHTTPSecrets(map[string]string{"a_url": "/functions/v1/a", "b_url": "/functions/v1/b"})
	got := httpRefsOf(parse(t, a, "db/fns.sql", src))
	if len(got) != len(want) {
		t.Fatalf("refs %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ref %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// A secret nothing declares, a variable assigned twice, a variable assigned
// after the request, a secret whose name is computed or one of several, and a
// URL with a computed part all stay dynamic. An undeclared secret's ref names
// it, so the finding can say which one to declare.
// @verifies reachability:INV-11
func TestVaultSecretURLsStayDynamic(t *testing.T) {
	src := `create function public.undeclared() returns void language plpgsql as $$
declare v_url text;
begin
  select decrypted_secret into v_url from vault.decrypted_secrets where name = 'other_url';
  perform net.http_post(url := v_url);
end $$;
create function public.twice() returns void language plpgsql as $$
declare v_url text;
begin
  select decrypted_secret into v_url from vault.decrypted_secrets where name = 'a_url';
  v_url := 'https://elsewhere.example.com';
  perform net.http_post(url := v_url);
end $$;
create function public.twice_into() returns void language plpgsql as $$
declare v_url text;
begin
  select decrypted_secret into v_url from vault.decrypted_secrets where name = 'a_url';
  select decrypted_secret into v_url from vault.decrypted_secrets where name = 'b_url';
  perform net.http_post(url := v_url);
end $$;
create function public.late() returns void language plpgsql as $$
declare v_url text;
begin
  perform net.http_post(url := v_url);
  select decrypted_secret into v_url from vault.decrypted_secrets where name = 'a_url';
end $$;
create function public.computed_name(n text) returns void language plpgsql as $$
declare v_url text;
begin
  select decrypted_secret into v_url from vault.decrypted_secrets where name = n;
  perform net.http_post(url := v_url);
end $$;
create function public.either() returns void language plpgsql as $$
declare v_url text;
begin
  select decrypted_secret into v_url from vault.decrypted_secrets where name = 'a_url' or name = 'b_url';
  perform net.http_post(url := v_url);
end $$;
create function public.computed_suffix(p text) returns void language plpgsql as $$
declare v_url text;
begin
  select decrypted_secret into v_url from vault.decrypted_secrets where name = 'a_url';
  perform net.http_post(url := v_url || p);
end $$;
`
	a := New(nil, nil)
	a.SetHTTPSecrets(map[string]string{"a_url": "/functions/v1/a", "b_url": "/functions/v1/b"})
	mod := parse(t, a, "db/fns.sql", src)
	var secrets []string
	for _, s := range mod.Symbols {
		for _, r := range s.Refs {
			if r.Kind != ir.RefHTTP {
				continue
			}
			if r.Name != "" {
				t.Errorf("%s: ref %q must stay computed", s.Name, r.Name)
			}
			secrets = append(secrets, strings.TrimPrefix(s.Name, "public.")+"="+r.Secret)
		}
	}
	want := []string{"undeclared=other_url", "twice=", "twice_into=", "late=", "computed_name=", "either=", "computed_suffix="}
	if strings.Join(secrets, ",") != strings.Join(want, ",") {
		t.Errorf("refs %v, want %v", secrets, want)
	}
}

// Without a declaration, every secret URL stays computed, and the adapter
// ignores an entry whose path does not start with a slash.
// @verifies reachability:INV-11
func TestVaultSecretNeedsADeclaration(t *testing.T) {
	src := "create function public.a() returns void language plpgsql as $$\ndeclare v_url text;\nbegin\n" +
		"  select decrypted_secret into v_url from vault.decrypted_secrets where name = 'a_url';\n  perform net.http_post(url := v_url);\nend $$;\n"
	for _, secrets := range []map[string]string{nil, {"a_url": "functions/v1/a"}, {"": "/x"}} {
		a := New(nil, nil)
		a.SetHTTPSecrets(secrets)
		if got := httpRefsOf(parse(t, a, "db/fns.sql", src)); len(got) != 1 || got[0] != "|" {
			t.Errorf("secrets %v: refs %q, want one computed ref", secrets, got)
		}
	}
}
