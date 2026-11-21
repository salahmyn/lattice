package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salahmyn/lattice/pkg/lattice/breaktest"
)

// fakePlaywright stands in for `npx playwright test <specs> -g <filter>
// --reporter=junit`. It reads the spec files it is given (relative to the
// directory it runs in), keeps the tests whose "<file> <describe> <title>"
// matches the -g regular expression, and writes a JUnit report in the shape
// Playwright's reporter uses to $PLAYWRIGHT_JUNIT_OUTPUT_NAME. A journey's
// outcome is read from the comment on the line after its test( line:
//
//	requests: <fn>  passes while src/api.ts still calls invoke('<fn>'), as a
//	                journey that fakes /functions/v1/<fn> by route does;
//	stubs           always passes, as a journey that stubs the API module does;
//	fails           always fails.
const fakePlaywright = `#!/bin/sh
files=""; re=""
while [ $# -gt 0 ]; do
  case "$1" in
    -g) re="$2"; shift 2 ;;
    --reporter=*) shift ;;
    *) files="$files $1"; shift ;;
  esac
done
out="$PLAYWRIGHT_JUNIT_OUTPUT_NAME"
[ -n "$out" ] || { echo "PLAYWRIGHT_JUNIT_OUTPUT_NAME is not set"; exit 2; }
fails=0; total=0; suites=""
for f in $files; do
  [ -f "$f" ] || { echo "no such spec file: $f"; exit 2; }
  desc=$(sed -n "s/^test\.describe('\(.*\)', () => {.*/\1/p" "$f" | head -1)
  cases=""; n=0; nf=0
  while IFS="$(printf '\t')" read -r title marker; do
    name="$title"; full="$f $title"
    [ -n "$desc" ] && { name="$desc › $title"; full="$f $desc $title"; }
    printf '%s' "$full" | grep -Eiq -- "$re" || continue
    n=$((n + 1))
    ok=1
    case "$marker" in
      fails) ok=0 ;;
      requests:*) grep -qF "invoke('${marker#requests: }')" src/api.ts || ok=0 ;;
    esac
    if [ $ok = 1 ]; then
      cases="$cases<testcase name=\"$name\" classname=\"$f\" time=\"0.1\">
</testcase>
"
    else
      nf=$((nf + 1))
      cases="$cases<testcase name=\"$name\" classname=\"$f\" time=\"0.1\">
<failure message=\"$f:1:1 $title\" type=\"FAILURE\">
<![CDATA[  Error: expected the request to be answered
]]>
</failure>
</testcase>
"
    fi
  done <<EOT
$(awk '/^ *test\(/ { t=$0; sub(/^ *test\(\047/,"",t); sub(/\047, async.*/,"",t); getline; m=$0; sub(/^ *\/\/ */,"",m); print t "\t" m }' "$f")
EOT
  [ $n = 0 ] && continue
  total=$((total + n)); fails=$((fails + nf))
  suites="$suites<testsuite name=\"$f\" timestamp=\"2026-01-01T00:00:00.000Z\" hostname=\"chromium\" tests=\"$n\" failures=\"$nf\" skipped=\"0\" time=\"0.3\" errors=\"0\">
$cases</testsuite>
"
done
[ $total = 0 ] && { echo "Error: No tests found"; exit 1; }
printf '<testsuites id="" name="" tests="%s" failures="%s" skipped="0" errors="0" time="0.5">\n%s</testsuites>\n' "$total" "$fails" "$suites" > "$out"
[ $fails = 0 ]
`

const playwrightSpec = `/** @module-feature orders */
import { test } from '@playwright/test';

test.describe('orders', () => {
  /** @verifies orders:INV-1 */
  test('lists orders', async ({ page }) => {
    // requests: list-orders
  });

  /** @verifies orders:INV-1 */
  test('syncs v2.1 (beta)', async ({ page }) => {
    // requests: list-orders
  });

  test('syncs v2x1 (beta)', async ({ page }) => {
    // fails
  });
});
`

// playwrightFixture is a client in apps/client with a Playwright runner whose
// command is the one the user guide shows, with the fake in place of npx
// playwright. mutate edits the files and the runner command.
func playwrightFixture(t *testing.T, mutate func(files map[string]string, cmd *string)) string {
	t.Helper()
	cmd := `[env, "PLAYWRIGHT_JUNIT_OUTPUT_NAME={junit}", sh, ./fake-playwright.sh, "{files}", -g, "{filter}", --reporter=junit]`
	return edgeFixture(t, func(files map[string]string) {
		for _, f := range []string{"api/routes.ts", "web/main.ts", "web/client.ts", "web/orders.ui.test.ts", "web/refunds.e2e.test.ts"} {
			delete(files, f)
		}
		files["index.html"] = "<!doctype html>\n<script type=\"module\" src=\"/apps/client/src/main.ts\"></script>\n"
		files["apps/client/src/main.ts"] = "/** @module-feature orders */\n\nimport { loadOrders } from './api';\n\nloadOrders();\n"
		files["apps/client/src/api.ts"] = "/** @module-feature orders */\n\nexport function loadOrders() {\n  return supabase.functions.invoke('list-orders');\n}\n"
		files["apps/client/journeys/orders.spec.ts"] = playwrightSpec
		files["apps/client/fake-playwright.sh"] = fakePlaywright
		files["supabase/functions/list-orders/index.ts"] = "/** @module-feature orders */\n\nimport { createHandler } from './handler.ts';\n\nDeno.serve(createHandler({}));\n"
		files["supabase/functions/list-orders/handler.ts"] = "/** @module-feature orders */\n\n/** @enforces orders:INV-1 */\nexport function createHandler(deps: unknown) {\n  return async (req: Request) => new Response('[]');\n}\n"
		files["lattice/entry-points/ep.orders.list.yaml"] = "id: ep.orders.list\nversion: 1\nstatus: production\nkind: http_route\npurpose: Lists orders\nowners:\n  business: nobody\n  engineering: nobody\n" +
			"trigger:\n  method: POST\n  path: /functions/v1/list-orders\nhandler:\n  symbol: supabase.functions.list-orders.handler.createHandler\n  file: supabase/functions/list-orders/handler.ts\nflow:\n  - feature: orders\n"
		if mutate != nil {
			mutate(files, &cmd)
		}
		files["lattice/config.yaml"] = "break:\n  edges: true\n  runners:\n    - name: journeys\n      match: [\"apps/client/journeys/**/*.spec.ts\"]\n      dir: apps/client\n      command: " + cmd + "\n"
	})
}

// A browser journey, run by a Playwright-shaped runner from a subdirectory,
// is a break verifier: the edge break renames the literal the client calls,
// the journey that fakes the route by that name fails, and the break is
// killed. The tests inside a describe block are found in the JUnit report by
// their "describe › title" names, and the filter, built from the titles,
// selects a title with parentheses and a dot exactly (the neighbouring test
// whose title differs at the dot is not run, or the baseline would fail).
// @verifies breaktest:INV-19
// @verifies breaktest:INV-4
func TestBreakPlaywrightJourneysKillEdgeBreak(t *testing.T) {
	dir := playwrightFixture(t, nil)
	rep, out, err := runBreak(t, dir, "--edges", "--promise", "orders:INV-1")
	if err != nil {
		t.Fatalf("break --edges: %v\n%s", err, out)
	}
	edges := edgeRuns(rep)["orders:INV-1"]
	if len(edges) != 1 {
		t.Fatalf("edge runs = %+v\n%s", edges, out)
	}
	r := edges[0]
	if r.Hop == nil || r.Hop.Kind != "http" || r.Hop.From != "apps.client.src.api.loadOrders" {
		t.Errorf("hop = %+v", r.Hop)
	}
	if r.Baseline != "pass" || r.Outcome != breaktest.Killed || r.Finding != "" {
		t.Fatalf("outcome %s baseline %q finding %q (%s), want killed over a passing baseline", r.Outcome, r.Baseline, r.Finding, r.Reason)
	}
	if len(r.RanVerifiers) != 2 || len(r.NotRunVerifiers) != 0 {
		t.Errorf("ran %v, not run %v; both journeys, the one in the describe block with the odd title included, must have run", r.RanVerifiers, r.NotRunVerifiers)
	}
	if len(r.FailedTests) != 2 || !strings.Contains(strings.Join(r.FailedTests, "|"), "orders › syncs v2.1 (beta)") {
		t.Errorf("failed tests = %v", r.FailedTests)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "apps", "client", "src", "api.ts")); strings.Contains(string(got), "lattice-break") {
		t.Error("the working tree was edited")
	}
}

// A journey that stubs the API module passes with the request renamed, so
// the edge break survives and EDGE_BREAK_SURVIVED names the hop.
// @verifies breaktest:INV-19
func TestBreakPlaywrightStubbedJourneySurvives(t *testing.T) {
	dir := playwrightFixture(t, func(files map[string]string, _ *string) {
		files["apps/client/journeys/orders.spec.ts"] = strings.ReplaceAll(playwrightSpec, "// requests: list-orders", "// stubs")
	})
	rep, out, err := runBreak(t, dir, "--edges", "--promise", "orders:INV-1")
	if err != nil {
		t.Fatalf("break --edges: %v\n%s", err, out)
	}
	edges := edgeRuns(rep)["orders:INV-1"]
	if len(edges) != 1 {
		t.Fatalf("edge runs = %+v\n%s", edges, out)
	}
	if r := edges[0]; r.Outcome != breaktest.Survived || r.Finding != breaktest.CodeEdgeBreakSurvived || len(r.RanVerifiers) != 2 {
		t.Errorf("outcome %s finding %q ran %v (%s), want survived with both journeys run", r.Outcome, r.Finding, r.RanVerifiers, r.Reason)
	}
}

// A journey the runner's filter does not select is not run, so a break the
// others do not notice reads unknown (partial), never survived.
// @verifies breaktest:INV-4
// @verifies breaktest:INV-7
func TestBreakPlaywrightUnselectedJourneyIsPartial(t *testing.T) {
	dir := playwrightFixture(t, func(files map[string]string, cmd *string) {
		// A fixed -g in place of {filter}, as a runner narrowed to a tag or a
		// title would have; the journey "syncs v2.1 (beta)" is never selected.
		*cmd = `[env, "PLAYWRIGHT_JUNIT_OUTPUT_NAME={junit}", sh, ./fake-playwright.sh, "{files}", -g, "lists orders", --reporter=junit]`
		files["apps/client/journeys/orders.spec.ts"] = strings.Replace(playwrightSpec, "// requests: list-orders", "// stubs", 1)
	})
	rep, out, err := runBreak(t, dir, "--edges", "--promise", "orders:INV-1")
	if err != nil {
		t.Fatalf("break --edges: %v\n%s", err, out)
	}
	edges := edgeRuns(rep)["orders:INV-1"]
	if len(edges) != 1 {
		t.Fatalf("edge runs = %+v\n%s", edges, out)
	}
	r := edges[0]
	if r.Outcome != breaktest.Unknown || !breaktest.IsPartial(r.Reason) || r.Finding != "" {
		t.Fatalf("outcome %s finding %q reason %q, want unknown and partial", r.Outcome, r.Finding, r.Reason)
	}
	if len(r.RanVerifiers) != 1 || len(r.NotRunVerifiers) != 1 || !strings.Contains(r.NotRunVerifiers[0], "syncs-v2-1-beta") {
		t.Errorf("ran %v, not run %v", r.RanVerifiers, r.NotRunVerifiers)
	}
}
