package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
	"github.com/salahmyn/lattice/pkg/lattice/slice"
)

const channelsFeature = `id: media
version: 1
status: production
purpose: |
  Makes thumbnails of uploaded images.
owners:
  business: nobody
  engineering: nobody
invariants:
  - id: INV-1
    statement: A thumbnail never exceeds the size limit.
`

// channelsFixture is a client module that enqueues a job through a wrapper
// the workspace declares, and a worker module that registers its handler for
// the same queue through another. The handler calls the function that keeps
// the promise. Nothing calls the handler: only the channel does.
func channelsFixture(t *testing.T, workspaceYAML, enqueueName string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"lattice/config.yaml":         "{}\n",
		"lattice/adapters.yaml":       "adapters:\n  typescript:\n    enabled: true\n",
		"lattice/workspace.yaml":      workspaceYAML,
		"lattice/features/media.yaml": channelsFeature,
		"index.html":                  "<!doctype html>\n<script type=\"module\" src=\"/web/main.ts\"></script>\n",
		"web/main.ts":                 "/** @module-feature media */\n\nimport * as jobs from '../lib/jobs';\n\nexport function upload() {\n  return jobs.enqueue(" + enqueueName + ", {});\n}\nupload();\n",
		"lib/jobs.ts":                 "/** @module-feature media */\n\nexport function enqueue(name: string, payload: object) {}\nexport function register(name: string, handler: (job: object) => void) {}\n",
		"workers/thumbs.ts":           "/** @module-feature media */\n\nimport * as jobs from '../lib/jobs';\n\nfunction makeThumb(job: object) {\n  clamp(job);\n}\n\n/**\n * @enforces media:INV-1\n */\nfunction clamp(job: object) {}\n\njobs.register('thumbs', makeThumb);\n",
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const channelsWorkspace = `mode: embedded
layers:
  - glob: "web/**"
    layer: ui
  - glob: "workers/**"
    layer: service
channels:
  publish:
    - call: "jobs.enqueue"
      arg: 0
      kind: queue
  consume:
    - call: "jobs.register"
      arg: 0
      handler_arg: 1
      kind: queue
`

// Declared wrappers join a client that enqueues and a worker that registers a
// handler: the slice from the entry file to the enforcer crosses the channel
// with one async hop, and the enforcer stays unreached for audit's live signal.
// @verifies reachability:INV-13
func TestSliceThroughDeclaredChannelWrappers(t *testing.T) {
	dir := channelsFixture(t, channelsWorkspace, "'thumbs'")
	rec := sliceJSON(t, dir, "media:INV-1")
	if rec.Reading != slice.ReadingComplete {
		t.Fatalf("reading %q: %s", rec.Reading, rec.Why)
	}
	var p *slice.Path
	for i := range rec.Paths {
		if rec.Paths[i].AsyncHops > 0 {
			p = &rec.Paths[i]
		}
	}
	if p == nil {
		t.Fatalf("no path crosses the channel: %+v", rec.Paths)
	}
	var kinds []string
	for _, h := range p.Hops {
		kinds = append(kinds, h.Kind)
	}
	if !strings.Contains(strings.Join(kinds, " "), "publish consume call") || p.AsyncHops != 1 {
		t.Errorf("hops %v async %d: %+v", kinds, p.AsyncHops, p.Hops)
	}
	if want := []string{"ui", "async", "service"}; !reflect.DeepEqual(p.CarriedBy, want) {
		t.Errorf("carried by %v, want %v", p.CarriedBy, want)
	}
	if len(rec.Enforcers) != 1 || rec.Enforcers[0].Reach == schema.ReachEntry || rec.Enforcers[0].AsyncHops != 1 {
		t.Errorf("the enforcer is reached only through the channel: reach %q async %d (it must not read entry)",
			rec.Enforcers[0].Reach, rec.Enforcers[0].AsyncHops)
	}
}

// A queue name that is computed at the publisher makes no edge, so the
// enforcer is not reached; the channel has a consumer and no publisher.
// @verifies reachability:INV-13
func TestComputedChannelNameMakesNoEdge(t *testing.T) {
	dir := channelsFixture(t, channelsWorkspace, "queueName()")
	rec := sliceJSON(t, dir, "media:INV-1")
	if rec.Reading != slice.ReadingUnreached {
		t.Errorf("reading %q: %s", rec.Reading, rec.Why)
	}
	out, _ := run(t, dir, newValidateCommand)
	for _, want := range []string{"CHANNEL_DYNAMIC", "CHANNEL_NO_PUBLISHER"} {
		if !strings.Contains(out, want) {
			t.Errorf("validate lacks %s:\n%s", want, out)
		}
	}
}

// A wrapper entry that cannot be read is a VALIDATE_POLICY error, like a bad
// layers: rule, and the adapters ignore it.
// @verifies reachability:INV-13
func TestValidateReportsBadChannelWrapper(t *testing.T) {
	ws := "mode: embedded\nchannels:\n  publish:\n    - call: \"jobs.enqueue\"\n      arg: -1\n      kind: queue\n" +
		"    - call: \"\"\n      arg: 0\n      kind: queue\n    - call: \"a.b\"\n      arg: 0\n      kind: carrier-pigeon\n"
	dir := channelsFixture(t, ws, "'thumbs'")
	out, err := run(t, dir, newValidateCommand)
	if err == nil {
		t.Fatalf("validate passed with bad channel wrappers:\n%s", out)
	}
	for _, want := range []string{"channels.publish[0]", "channels.publish[1]", "channels.publish[2]", "carrier-pigeon"} {
		if !strings.Contains(out, want) {
			t.Errorf("validate lacks %q:\n%s", want, out)
		}
	}
}

const vaultFeature = `id: enrich
version: 1
status: production
purpose: |
  Enriches records on a schedule.
owners:
  business: nobody
  engineering: nobody
invariants:
  - id: INV-1
    statement: A record is enriched at most once.
`

// vaultFixture is a cron job that runs a SQL function, which reads the URL of
// an edge function whole from a Vault secret and posts to it. The handler
// keeps the promise.
func vaultFixture(t *testing.T, workspaceYAML string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"lattice/config.yaml":                  "{}\n",
		"lattice/adapters.yaml":                "adapters:\n  typescript:\n    enabled: true\n  postgres:\n    enabled: true\n",
		"lattice/workspace.yaml":               workspaceYAML,
		"lattice/features/enrich.yaml":         vaultFeature,
		"supabase/functions/enrich/index.ts":   "/** @module-feature enrich */\n\nimport { createHandler } from './handler.ts';\n\nDeno.serve(createHandler({}));\n",
		"supabase/functions/enrich/handler.ts": "/** @module-feature enrich */\n\n/** @enforces enrich:INV-1 */\nexport function createHandler(deps: unknown) {\n  return async (req: Request) => new Response('ok');\n}\n",
		"supabase/migrations/001_enrich.sql": "create function public.call_enrich() returns void language plpgsql as $$\ndeclare v_url text;\nbegin\n" +
			"  select decrypted_secret into v_url from vault.decrypted_secrets where name = 'enrich_url';\n" +
			"  perform net.http_post(url := v_url, body := '{}'::jsonb);\nend $$;\n" +
			"select cron.schedule('enrich-job', '*/5 * * * *', $$ select public.call_enrich() $$);\n",
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// A secret declared in workspace.yaml http.secrets gives the request that
// reads its URL whole the declared route: the cron job reaches the edge
// function's handler through resolved hops, and the slice of the handler's
// promise has a path from the job. Without the declaration the URL is
// computed and the job reaches nothing.
// @verifies reachability:INV-11
func TestSliceThroughAVaultSecretURL(t *testing.T) {
	declared := "mode: embedded\nhttp:\n  secrets:\n    enrich_url: /functions/v1/enrich\n"
	rec := sliceJSON(t, vaultFixture(t, declared), "enrich:INV-1")
	if rec.Reading != slice.ReadingComplete {
		t.Fatalf("declared secret: reading %q: %s", rec.Reading, rec.Why)
	}
	fromCron := func(rec slice.Record) (http bool, found bool) {
		for _, p := range rec.Paths {
			if p.Root.FQN != "sql.cron.enrich_job" {
				continue
			}
			found = true
			for _, h := range p.Hops {
				if h.Kind == "http" && h.Truth == slice.TruthResolved {
					http = true
				}
			}
		}
		return http, found
	}
	if http, found := fromCron(rec); !found || !http {
		t.Errorf("declared secret: want a path from the cron job with a resolved http hop: %+v", rec.Paths)
	}
	undeclared := sliceJSON(t, vaultFixture(t, "mode: embedded\n"), "enrich:INV-1")
	if _, found := fromCron(undeclared); found {
		t.Errorf("undeclared secret: the cron job must not reach the handler: %+v", undeclared.Paths)
	}
}
